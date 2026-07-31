package executor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
	"golang.org/x/text/unicode/norm"
)

const journalVersion = 1

type Service struct {
	journalDir  string
	now         func() time.Time
	removeFile  func(string) error
	openSource  func(string) (*os.File, error)
	operationMu sync.Mutex
}

type Journal struct {
	Version    int                `json:"version"`
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	TargetRoot string             `json:"targetRoot"`
	CreatedAt  time.Time          `json:"createdAt"`
	UpdatedAt  time.Time          `json:"updatedAt"`
	Error      string             `json:"error,omitempty"`
	Operations []JournalOperation `json:"operations"`
}

type JournalOperation struct {
	ProposalID          string `json:"proposalId"`
	Action              string `json:"action"`
	Category            string `json:"category"`
	Source              string `json:"source"`
	Target              string `json:"target"`
	DuplicateOf         string `json:"duplicateOf,omitempty"`
	Size                int64  `json:"size"`
	SourceModifiedNanos int64  `json:"sourceModifiedNanos,omitempty"`
	SHA256              string `json:"sha256,omitempty"`

	// SourceRetained records the safe copy-only fallback used when the target
	// was verified but the source filesystem refused deletion.
	SourceRetained    bool      `json:"sourceRetained,omitempty"`
	SourceRemoveError string    `json:"sourceRemoveError,omitempty"`
	Status            string    `json:"status"`
	UpdatedAt         time.Time `json:"updatedAt,omitempty"`
}

func New(journalDir string) *Service {
	return &Service{journalDir: journalDir, now: time.Now, removeFile: os.Remove, openSource: os.Open}
}

func (s *Service) Execute(ctx context.Context, plan domain.OperationPlan) (domain.ExecutionResult, error) {
	return s.ExecuteWithProgress(ctx, plan, nil)
}

func (s *Service) ExecuteWithProgress(ctx context.Context, plan domain.OperationPlan, report func(domain.ExecutionProgress)) (domain.ExecutionResult, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	if !plan.Executable || len(plan.Operations) == 0 || len(plan.Operations) > maxJournalOperations {
		return domain.ExecutionResult{}, fmt.Errorf("der Operationsplan ist nicht ausführbar")
	}
	totalBytes := int64(0)
	for _, operation := range plan.Operations {
		if operation.Size < 0 || operation.Size > math.MaxInt64-totalBytes {
			return domain.ExecutionResult{}, fmt.Errorf("der Operationsplan enthält eine ungültige Gesamtgröße")
		}
		totalBytes += operation.Size
	}
	lock, err := s.acquireExecutionLock(planLockScope(plan))
	if err != nil {
		return domain.ExecutionResult{Status: "failed", Total: len(plan.Operations), TotalBytes: totalBytes, Error: err.Error()}, err
	}
	defer lock.Release()

	emitProgress(report, domain.ExecutionProgress{Status: "checking", Total: len(plan.Operations), TotalBytes: totalBytes})
	validatedPlan, preflightWarnings, err := preflightSources(plan)
	if err != nil {
		return domain.ExecutionResult{
			Status: "failed", Total: len(plan.Operations), Error: err.Error(), Warnings: preflightWarnings,
		}, err
	}
	plan = validatedPlan
	if err := s.preflightTargets(plan); err != nil {
		return domain.ExecutionResult{
			Status: "failed", Total: len(plan.Operations), TotalBytes: totalBytes, Error: err.Error(), Warnings: preflightWarnings,
		}, err
	}
	deduplicateExpectations, err := preflightDeduplicates(ctx, plan)
	if err != nil {
		return domain.ExecutionResult{
			Status: "failed", Total: len(plan.Operations), TotalBytes: totalBytes, Error: err.Error(), Warnings: preflightWarnings,
		}, err
	}
	emitProgress(report, domain.ExecutionProgress{Status: "moving", Total: len(plan.Operations), TotalBytes: totalBytes})
	journal, err := s.newJournal(plan)
	if err != nil {
		return domain.ExecutionResult{}, err
	}
	if err := s.saveNew(journal); err != nil {
		return domain.ExecutionResult{}, fmt.Errorf("Journal anlegen: %w", err)
	}

	completedBytes := int64(0)
	for index := range journal.Operations {
		op := &journal.Operations[index]
		emitProgress(report, domain.ExecutionProgress{
			Status: "moving", Completed: index, Total: len(journal.Operations),
			CompletedBytes: completedBytes, TotalBytes: totalBytes,
			CurrentSource: op.Source, CurrentTarget: op.Target,
		})
		op.Status = "transferring"
		op.UpdatedAt = s.now()
		journal.UpdatedAt = s.now()
		if err := s.save(journal); err != nil {
			return withWarnings(resultFromJournal(journal), preflightWarnings), fmt.Errorf("Journal vorbereiten: %w", err)
		}
		allowedRoot := journal.TargetRoot
		if op.Action == "remove" || op.Action == "deduplicate" {
			allowedRoot, err = s.quarantineRoot(journal.ID)
			if err != nil {
				return withWarnings(resultFromJournal(journal), preflightWarnings), err
			}
		}
		lastProgressAt := time.Time{}
		lastProgressBytes := int64(0)
		options := transferOptions{
			allowedTargetRoot:           allowedRoot,
			privateTarget:               op.Action == "remove" || op.Action == "deduplicate",
			expectedSourceModifiedNanos: op.SourceModifiedNanos,
			reportBytes: func(operationBytes int64) {
				now := s.now()
				if operationBytes-lastProgressBytes < 16*1024*1024 && !lastProgressAt.IsZero() && now.Sub(lastProgressAt) < 250*time.Millisecond {
					return
				}
				lastProgressAt = now
				lastProgressBytes = operationBytes
				lock.Touch()
				emitProgress(report, domain.ExecutionProgress{
					Status: "moving", Completed: index, Total: len(journal.Operations),
					CompletedBytes: completedBytes + operationBytes, TotalBytes: totalBytes,
					CurrentSource: op.Source, CurrentTarget: op.Target,
				})
			},
		}
		if op.Action == "deduplicate" {
			expectation, found := deduplicateExpectations[portablePathKey(op.Source)]
			if !found {
				verifyErr := fmt.Errorf("kein bestätigter Duplikatvergleich für %s vorhanden", op.Source)
				op.Status = "failed"
				op.UpdatedAt = s.now()
				journal.Status = "failed"
				journal.Error = verifyErr.Error()
				journal.UpdatedAt = s.now()
				if saveErr := s.save(journal); saveErr != nil {
					return withWarnings(resultFromJournal(journal), preflightWarnings), fmt.Errorf("%v; Journal aktualisieren: %w", verifyErr, saveErr)
				}
				return withWarnings(resultFromJournal(journal), preflightWarnings), verifyErr
			}
			options.requiredSHA256 = expectation.hash
			var verifiedDuplicate os.FileInfo
			options.beforePublish = func() error {
				verified, verifyErr := verifyDeduplicateTarget(ctx, op.DuplicateOf, expectation.hash, expectation.size, journal.TargetRoot)
				if verifyErr == nil {
					verifiedDuplicate = verified
				}
				return verifyErr
			}
			options.beforeSourceDelete = func() error {
				current, statErr := os.Lstat(op.DuplicateOf)
				if statErr != nil || !sameStableFile(verifiedDuplicate, current) {
					if statErr == nil {
						statErr = fmt.Errorf("Duplikatziel wurde unmittelbar vor dem Entfernen der Quelle ausgetauscht oder verändert: %s", op.DuplicateOf)
					}
					return statErr
				}
				return nil
			}
		}
		checksum, size, sourceRemoveError, moveErr := s.transfer(ctx, op.Source, op.Target, op.Size, options)
		if moveErr != nil {
			op.Status = "failed"
			op.UpdatedAt = s.now()
			journal.Status = "failed"
			journal.Error = moveErr.Error()
			journal.UpdatedAt = s.now()
			if saveErr := s.save(journal); saveErr != nil {
				return withWarnings(resultFromJournal(journal), preflightWarnings), fmt.Errorf("%v; Journal aktualisieren: %w", moveErr, saveErr)
			}
			return withWarnings(resultFromJournal(journal), preflightWarnings), moveErr
		}
		op.SHA256 = checksum
		op.Size = size
		op.SourceRetained = sourceRemoveError != ""
		op.SourceRemoveError = sourceRemoveError
		op.Status = "completed"
		op.UpdatedAt = s.now()
		journal.UpdatedAt = s.now()
		completedBytes += size
		emitProgress(report, domain.ExecutionProgress{
			Status: "moving", Completed: index + 1, Total: len(journal.Operations),
			CompletedBytes: completedBytes, TotalBytes: totalBytes,
			CurrentSource: op.Source, CurrentTarget: op.Target,
		})
		if err := s.save(journal); err != nil {
			return withWarnings(resultFromJournal(journal), preflightWarnings), fmt.Errorf("Journal aktualisieren: %w", err)
		}
	}

	journal.Status = "completed"
	journal.UpdatedAt = s.now()
	if err := s.save(journal); err != nil {
		return withWarnings(resultFromJournal(journal), preflightWarnings), fmt.Errorf("Journal abschließen: %w", err)
	}
	emitProgress(report, domain.ExecutionProgress{
		Status: "completed", Completed: len(journal.Operations), Total: len(journal.Operations),
		CompletedBytes: completedBytes, TotalBytes: totalBytes,
	})
	return withWarnings(resultFromJournal(journal), preflightWarnings), nil
}

func emitProgress(report func(domain.ExecutionProgress), progress domain.ExecutionProgress) {
	if report != nil {
		report(progress)
	}
}

func preflightSources(plan domain.OperationPlan) (domain.OperationPlan, []string, error) {
	warnings := make([]string, 0)
	seenSources := make(map[string]string, len(plan.Operations))
	proposalRoots := make(map[string]string)
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Action != "" && operation.Action != "move" && operation.Action != "remove" && operation.Action != "deduplicate" {
			return plan, warnings, fmt.Errorf("ungültige Aktion im Operationsplan: %q", operation.Action)
		}
		if !isCleanAbsolutePath(operation.Source) {
			return plan, warnings, fmt.Errorf("Quellpfad ist nicht absolut und normalisiert: %s", operation.Source)
		}
		physicalRoot, err := validatePhysicalSourceRoot(operation.SourceRoot)
		if err != nil {
			return plan, warnings, fmt.Errorf("gebundenen Quellordner prüfen: %w", err)
		}
		if operation.ProposalID != "" {
			if previous, exists := proposalRoots[operation.ProposalID]; exists && portablePathKey(previous) != portablePathKey(physicalRoot) {
				return plan, warnings, fmt.Errorf("Vorschlag %q verwendet mehrere Quellordner", operation.ProposalID)
			}
			proposalRoots[operation.ProposalID] = physicalRoot
		}
		resolved, info, recovered, err := resolveSource(operation.Source)
		if err != nil {
			return plan, warnings, fmt.Errorf("Quelle seit dem Scan nicht mehr erreichbar: %s: %w. Bitte den Quellordner neu scannen", operation.Source, err)
		}
		if !info.Mode().IsRegular() {
			return plan, warnings, fmt.Errorf("Quelle ist keine reguläre Datei: %s", resolved)
		}
		if operation.Size > 0 && info.Size() != operation.Size {
			return plan, warnings, fmt.Errorf("Quelldatei wurde seit dem Scan verändert: %s. Bitte den Quellordner neu scannen", resolved)
		}
		// Zero remains readable for plans created by older app versions. All new
		// plans bind this value, so both size and modification time must match.
		if operation.SourceModifiedNanos != 0 && info.ModTime().UnixNano() != operation.SourceModifiedNanos {
			return plan, warnings, fmt.Errorf("Änderungszeit der Quelldatei stimmt nicht mehr mit dem Plan überein: %s. Bitte den Quellordner neu scannen", resolved)
		}
		physicalSource, err := filepath.EvalSymlinks(resolved)
		if err != nil {
			return plan, warnings, fmt.Errorf("Quelldatei physisch auflösen: %s: %w", resolved, err)
		}
		physicalSource = filepath.Clean(physicalSource)
		if !pathInsideRoot(physicalRoot, physicalSource) {
			return plan, warnings, fmt.Errorf("Quelldatei liegt nicht mehr im gebundenen Quellordner: %s", physicalSource)
		}
		if portablePathKey(physicalSource) != portablePathKey(resolved) {
			return plan, warnings, fmt.Errorf("Quellpfad wurde seit der Planerstellung über einen Symlink oder Reparse-Point umgeleitet: %s", resolved)
		}
		operation.SourceRoot = physicalRoot
		operation.Source = physicalSource
		key := portablePathKey(physicalSource)
		if previous, exists := seenSources[key]; exists {
			return plan, warnings, fmt.Errorf("Quelldatei ist mehrfach im Operationsplan enthalten: %s und %s", previous, physicalSource)
		}
		seenSources[key] = physicalSource
		if recovered {
			warnings = append(warnings, "Unicode-normalisierten Quellpfad wiederaufgelöst: "+physicalSource)
		}
	}
	return plan, warnings, nil
}

func resolveSource(source string) (string, os.FileInfo, bool, error) {
	info, err := os.Lstat(source)
	if err == nil {
		return source, info, false, nil
	}
	if !os.IsNotExist(err) {
		return "", nil, false, err
	}
	resolved, err := resolveEquivalentPath(source)
	if err != nil {
		return "", nil, false, err
	}
	info, err = os.Lstat(resolved)
	if err != nil {
		return "", nil, false, err
	}
	return resolved, info, resolved != source, nil
}

func resolveEquivalentPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	volume := filepath.VolumeName(cleaned)
	root := volume + string(filepath.Separator)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("Quellpfad ist nicht absolut")
	}
	relative := strings.TrimPrefix(cleaned, root)
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		candidate := filepath.Join(current, component)
		if _, err := os.Lstat(candidate); err == nil {
			current = candidate
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return "", err
		}
		canonical := canonicalPathName(component)
		matches := make([]string, 0, 1)
		for _, entry := range entries {
			if canonicalPathName(entry.Name()) == canonical {
				matches = append(matches, entry.Name())
			}
		}
		if len(matches) == 0 {
			return "", os.ErrNotExist
		}
		if len(matches) > 1 {
			return "", fmt.Errorf("mehrdeutige Unicode-Pfadvarianten für %q", component)
		}
		current = filepath.Join(current, matches[0])
	}
	return current, nil
}

func canonicalPathName(value string) string {
	return norm.NFC.String(value)
}

func withWarnings(result domain.ExecutionResult, warnings []string) domain.ExecutionResult {
	result.Warnings = append(result.Warnings, warnings...)
	return result
}

func (s *Service) Undo(ctx context.Context, journalID string) (domain.ExecutionResult, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	journalLock, err := s.acquireExecutionLock("journal:" + journalID)
	if err != nil {
		return domain.ExecutionResult{}, err
	}
	defer journalLock.Release()

	journal, err := s.load(journalID)
	if err != nil {
		return domain.ExecutionResult{}, err
	}
	targetLock, err := s.acquireExecutionLock(journalTargetLockScope(journal))
	if err != nil {
		return domain.ExecutionResult{}, err
	}
	defer targetLock.Release()
	if journal.Status == "undone" {
		return resultFromJournal(journal), fmt.Errorf("dieser Import wurde bereits rückgängig gemacht")
	}

	journal.Status = "undoing"
	journal.Error = ""
	journal.UpdatedAt = s.now()
	if err := s.save(journal); err != nil {
		return domain.ExecutionResult{}, err
	}
	for index := len(journal.Operations) - 1; index >= 0; index-- {
		op := &journal.Operations[index]
		if op.Status == "transferring" || op.Status == "failed" {
			if err := reconcileInterrupted(ctx, op); err != nil {
				journal.Status = "undo_failed"
				journal.Error = err.Error()
				journal.UpdatedAt = s.now()
				_ = s.save(journal)
				return resultFromJournal(journal), err
			}
		}
		if op.Status == "pending" {
			op.Status = "undone"
			op.UpdatedAt = s.now()
			journal.UpdatedAt = s.now()
			if err := s.save(journal); err != nil {
				return resultFromJournal(journal), err
			}
			continue
		}
		if op.Status != "completed" {
			continue
		}
		allowedTargetRoot := journal.TargetRoot
		if op.Action == "remove" || op.Action == "deduplicate" {
			allowedTargetRoot, err = s.quarantineRoot(journal.ID)
			if err != nil {
				return resultFromJournal(journal), err
			}
		}
		if pathErr := validateExistingFileWithin(allowedTargetRoot, op.Target); pathErr != nil {
			hashErr := fmt.Errorf("Undo-Ziel validieren: %w", pathErr)
			journal.Status = "undo_failed"
			journal.Error = hashErr.Error()
			journal.UpdatedAt = s.now()
			_ = s.save(journal)
			return resultFromJournal(journal), hashErr
		}
		actual, _, verifiedTarget, hashErr := checksumFileWithIdentity(ctx, op.Target)
		if hashErr != nil || actual != op.SHA256 {
			if hashErr == nil {
				hashErr = fmt.Errorf("Zieldatei wurde nach dem Import verändert: %s", op.Target)
			}
			journal.Status = "undo_failed"
			journal.Error = hashErr.Error()
			journal.UpdatedAt = s.now()
			_ = s.save(journal)
			return resultFromJournal(journal), hashErr
		}
		if op.SourceRetained {
			current, identityErr := os.Lstat(op.Target)
			if identityErr != nil || !sameStableFile(verifiedTarget, current) {
				if identityErr == nil {
					identityErr = fmt.Errorf("Undo-Ziel wurde vor dem Entfernen ausgetauscht oder verändert: %s", op.Target)
				}
				journal.Status = "undo_failed"
				journal.Error = identityErr.Error()
				journal.UpdatedAt = s.now()
				_ = s.save(journal)
				return resultFromJournal(journal), identityErr
			}
			if removeErr := s.removeFile(op.Target); removeErr != nil {
				journal.Status = "undo_failed"
				journal.Error = fmt.Sprintf("kopierte Zieldatei beim Undo entfernen: %v", removeErr)
				journal.UpdatedAt = s.now()
				_ = s.save(journal)
				return resultFromJournal(journal), errors.New(journal.Error)
			}
		} else if _, _, _, moveErr := s.transfer(ctx, op.Target, op.Source, op.Size, transferOptions{allowedTargetRoot: filepath.Dir(op.Source)}); moveErr != nil {
			journal.Status = "undo_failed"
			journal.Error = moveErr.Error()
			journal.UpdatedAt = s.now()
			_ = s.save(journal)
			return resultFromJournal(journal), moveErr
		}
		op.Status = "undone"
		op.UpdatedAt = s.now()
		journal.UpdatedAt = s.now()
		if err := s.save(journal); err != nil {
			return resultFromJournal(journal), err
		}
	}
	journal.Status = "undone"
	journal.UpdatedAt = s.now()
	if err := s.save(journal); err != nil {
		return resultFromJournal(journal), err
	}
	return resultFromJournal(journal), nil
}

func (s *Service) newJournal(plan domain.OperationPlan) (*Journal, error) {
	id, err := randomID(s.now())
	if err != nil {
		return nil, fmt.Errorf("Journal-ID erzeugen: %w", err)
	}
	journal := &Journal{
		Version: journalVersion, ID: id, Status: "running", TargetRoot: plan.TargetRoot,
		CreatedAt: s.now(), UpdatedAt: s.now(), Operations: make([]JournalOperation, len(plan.Operations)),
	}
	for index, op := range plan.Operations {
		target := op.Target
		action := op.Action
		if action == "" {
			action = "move"
		}
		duplicateOf := ""
		if action == "remove" || action == "deduplicate" {
			if action == "deduplicate" {
				duplicateOf = target
			}
			target, err = s.quarantineTarget(id, index, op.Source)
			if err != nil {
				return nil, err
			}
		}
		journal.Operations[index] = JournalOperation{
			ProposalID: op.ProposalID, Action: action, Category: op.Category,
			Source: op.Source, Target: target, DuplicateOf: duplicateOf, Size: op.Size,
			SourceModifiedNanos: op.SourceModifiedNanos, Status: "pending",
		}
	}
	return journal, nil
}

func (s *Service) quarantineTarget(journalID string, index int, source string) (string, error) {
	root, err := s.quarantineRoot(journalID)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%04d-%s", index+1, filepath.Base(source))
	return filepath.Join(root, name), nil
}

func (s *Service) quarantineRoot(journalID string) (string, error) {
	directory, err := s.directory()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(directory), "quarantine", journalID), nil
}

func (s *Service) directory() (string, error) {
	if s.journalDir != "" {
		return s.journalDir, nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("Konfigurationsordner bestimmen: %w", err)
	}
	return filepath.Join(config, "MyFileSorter", "journals"), nil
}

func (s *Service) save(journal *Journal) error {
	return s.persistJournal(journal, true)
}

func (s *Service) saveNew(journal *Journal) error {
	return s.persistJournal(journal, false)
}

func (s *Service) persistJournal(journal *Journal, replaceExisting bool) error {
	if journal == nil {
		return fmt.Errorf("leeres Journal kann nicht gespeichert werden")
	}
	if err := s.validateJournal(journal, journal.ID); err != nil {
		return fmt.Errorf("Journal vor dem Speichern validieren: %w", err)
	}
	directory, err := s.directory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := ensurePrivateDirectory(directory); err != nil {
		return err
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".journal-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	finalName := filepath.Join(directory, journal.ID+".json")
	if replaceExisting {
		if info, statErr := os.Lstat(finalName); statErr == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("Journalziel ist keine reguläre Datei: %s", finalName)
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		if err := replaceFile(temporaryName, finalName); err != nil {
			return err
		}
	} else if err := installFileNoReplace(temporaryName, finalName); err != nil {
		return fmt.Errorf("neues Journal atomar anlegen: %w", err)
	}
	return syncDirectory(directory)
}

func (s *Service) load(journalID string) (*Journal, error) {
	if journalID == "" || strings.ContainsAny(journalID, `/\\`) || filepath.Base(journalID) != journalID {
		return nil, fmt.Errorf("ungültige Journal-ID")
	}
	directory, err := s.directory()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, journalID+".json")
	data, err := readLimitedFile(path, maxJournalBytes)
	if err != nil {
		return nil, fmt.Errorf("Journal laden: %w", err)
	}
	var journal Journal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("Journal lesen: %w", err)
	}
	if err := s.validateJournal(&journal, journalID); err != nil {
		return nil, err
	}
	return &journal, nil
}

type transferOptions struct {
	allowedTargetRoot           string
	privateTarget               bool
	requiredSHA256              string
	expectedSourceModifiedNanos int64
	beforePublish               func() error
	beforeSourceDelete          func() error
	reportBytes                 func(int64)
}

func (s *Service) transfer(ctx context.Context, source, target string, expectedSize int64, options transferOptions) (string, int64, string, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return "", 0, "", fmt.Errorf("Quelle prüfen: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, "", fmt.Errorf("Quelle ist keine reguläre Datei: %s", source)
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return "", 0, "", fmt.Errorf("Quelldatei wurde seit dem Plan verändert: %s", source)
	}
	if options.expectedSourceModifiedNanos != 0 && info.ModTime().UnixNano() != options.expectedSourceModifiedNanos {
		return "", 0, "", fmt.Errorf("Änderungszeit der Quelldatei stimmt nicht mehr mit dem Plan überein: %s", source)
	}
	openSource := s.openSource
	if openSource == nil {
		openSource = os.Open
	}
	sourceFile, err := openSource(source)
	if err != nil {
		return "", 0, "", err
	}
	sourceOpen := true
	defer func() {
		if sourceOpen {
			_ = sourceFile.Close()
		}
	}()
	openedInfo, err := sourceFile.Stat()
	if err != nil {
		return "", 0, "", fmt.Errorf("geöffnete Quelle prüfen: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return "", 0, "", fmt.Errorf("Quelldatei wurde während der Prüfung ausgetauscht: %s", source)
	}
	if expectedSize > 0 && openedInfo.Size() != expectedSize {
		return "", 0, "", fmt.Errorf("Quelldatei wurde seit dem Plan verändert: %s", source)
	}
	if err := prepareTargetDestination(options.allowedTargetRoot, target, options.privateTarget); err != nil {
		return "", 0, "", err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".myfilesorter-*.part")
	if err != nil {
		return "", 0, "", err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	hash := sha256.New()
	written, copyErr := copyWithContextProgress(ctx, io.MultiWriter(temporary, hash), sourceFile, options.reportBytes)
	postCopyInfo, statErr := sourceFile.Stat()
	if copyErr == nil && statErr != nil {
		copyErr = statErr
	}
	if copyErr == nil && !sameStableFile(openedInfo, postCopyInfo) {
		copyErr = fmt.Errorf("Quelldatei wurde während des Kopierens verändert: %s", source)
	}
	if closeErr := sourceFile.Close(); copyErr == nil {
		copyErr = closeErr
	}
	sourceOpen = false
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	if copyErr != nil {
		_ = temporary.Close()
		return "", 0, "", fmt.Errorf("Datei kopieren: %w", copyErr)
	}
	if written != openedInfo.Size() {
		_ = temporary.Close()
		return "", 0, "", fmt.Errorf("unvollständige Kopie: %s", target)
	}
	expectedHash := hex.EncodeToString(hash.Sum(nil))
	if options.requiredSHA256 != "" && expectedHash != options.requiredSHA256 {
		_ = temporary.Close()
		return "", 0, "", fmt.Errorf("Quelle stimmt nicht mehr mit der bestätigten Duplikatdatei überein: %s", source)
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		_ = temporary.Close()
		return "", 0, "", fmt.Errorf("Kopie zum Prüfen öffnen: %w", err)
	}
	actualHasher := sha256.New()
	actualSize, err := copyWithContext(ctx, actualHasher, temporary)
	actualHash := hex.EncodeToString(actualHasher.Sum(nil))
	if err != nil {
		_ = temporary.Close()
		return "", 0, "", fmt.Errorf("Kopie prüfen: %w", err)
	}
	if expectedHash != actualHash || actualSize != written {
		_ = temporary.Close()
		return "", 0, "", fmt.Errorf("Prüfsummenvergleich fehlgeschlagen: %s", target)
	}
	if err := temporary.Chmod(openedInfo.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return "", 0, "", err
	}
	temporaryInfo, err := temporary.Stat()
	if err != nil {
		_ = temporary.Close()
		return "", 0, "", fmt.Errorf("temporäre Zieldatei prüfen: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", 0, "", err
	}
	if err := temporary.Close(); err != nil {
		return "", 0, "", err
	}
	if err := validateTemporaryTarget(options.allowedTargetRoot, temporaryName, temporaryInfo); err != nil {
		return "", 0, "", err
	}
	if options.beforePublish != nil {
		if err := options.beforePublish(); err != nil {
			return "", 0, "", err
		}
	}
	if err := installFileNoReplace(temporaryName, target); err != nil {
		return "", 0, "", fmt.Errorf("Zieldatei finalisieren: %w", err)
	}
	installedInfo, err := os.Lstat(target)
	if err != nil || !installedInfo.Mode().IsRegular() || !os.SameFile(temporaryInfo, installedInfo) {
		if err == nil {
			err = fmt.Errorf("finalisiertes Ziel wurde ausgetauscht: %s", target)
		}
		return "", 0, "", fmt.Errorf("Zieldatei finalisieren: %w", err)
	}
	if err := syncDirectory(filepath.Dir(target)); err != nil {
		return expectedHash, written, fmt.Sprintf("Zielordner konnte nicht dauerhaft synchronisiert werden; Quelle wurde beibehalten: %v", err), nil
	}
	if options.beforeSourceDelete != nil {
		if err := options.beforeSourceDelete(); err != nil {
			return expectedHash, written, "", err
		}
	}
	currentSource, identityErr := os.Lstat(source)
	if identityErr != nil || !currentSource.Mode().IsRegular() || !os.SameFile(openedInfo, currentSource) || !sameStableFile(openedInfo, currentSource) {
		if identityErr == nil {
			identityErr = fmt.Errorf("Quelle wurde vor dem Entfernen ausgetauscht oder verändert")
		}
		return expectedHash, written, identityErr.Error(), nil
	}
	if err := s.removeFile(source); err != nil {
		// The target is already complete and hash-verified. Keep it instead of
		// discarding a successful transfer merely because a read-only share or
		// restrictive ACL does not permit deleting the source.
		return expectedHash, written, err.Error(), nil
	}
	_ = syncDirectory(filepath.Dir(source))
	return expectedHash, written, "", nil
}

func sameStableFile(before, after os.FileInfo) bool {
	if before == nil || after == nil || !before.Mode().IsRegular() || !after.Mode().IsRegular() {
		return false
	}
	return os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func checksumFile(ctx context.Context, path string) (string, int64, error) {
	hash, size, _, err := checksumFileWithIdentity(ctx, path)
	return hash, size, err
}

func checksumFileWithIdentity(ctx context.Context, path string) (string, int64, os.FileInfo, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return "", 0, nil, err
	}
	if !pathInfo.Mode().IsRegular() {
		return "", 0, nil, fmt.Errorf("Pfad ist keine reguläre Datei: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		if err == nil {
			err = fmt.Errorf("Datei wurde beim Öffnen ausgetauscht: %s", path)
		}
		return "", 0, nil, err
	}
	hash := sha256.New()
	size, err := copyWithContext(ctx, hash, file)
	if err != nil {
		return "", 0, nil, err
	}
	postInfo, err := file.Stat()
	if err != nil {
		return "", 0, nil, err
	}
	if !sameStableFile(openedInfo, postInfo) || size != openedInfo.Size() {
		return "", 0, nil, fmt.Errorf("Datei wurde während der Prüfsummenbildung verändert: %s", path)
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || !sameStableFile(openedInfo, currentInfo) {
		if err == nil {
			err = fmt.Errorf("Datei wurde nach der Prüfsummenbildung ausgetauscht: %s", path)
		}
		return "", 0, nil, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, currentInfo, nil
}

type deduplicateExpectation struct {
	hash string
	size int64
}

func preflightDeduplicates(ctx context.Context, plan domain.OperationPlan) (map[string]deduplicateExpectation, error) {
	expectations := make(map[string]deduplicateExpectation)
	operationSources := make(map[string]struct{}, len(plan.Operations))
	for _, operation := range plan.Operations {
		operationSources[portablePathKey(operation.Source)] = struct{}{}
	}
	for _, operation := range plan.Operations {
		if operation.Action != "deduplicate" {
			continue
		}
		if _, usedAsSource := operationSources[portablePathKey(operation.Target)]; usedAsSource {
			return nil, fmt.Errorf("Duplikatziel wird im selben Plan als Quelle verwendet: %s", operation.Target)
		}
		hash, size, err := verifyDeduplicatePair(ctx, operation.Source, operation.Target, operation.Size, plan.TargetRoot)
		if err != nil {
			return nil, err
		}
		expectations[portablePathKey(operation.Source)] = deduplicateExpectation{hash: hash, size: size}
	}
	return expectations, nil
}

func verifyDeduplicatePair(ctx context.Context, source, duplicate string, expectedSize int64, targetRoot string) (string, int64, error) {
	if portablePathKey(source) == portablePathKey(duplicate) {
		return "", 0, fmt.Errorf("Duplikatquelle und bestehendes Ziel sind identisch: %s", source)
	}
	if strings.TrimSpace(targetRoot) == "" {
		targetRoot = filepath.Dir(duplicate)
	}
	if err := validateExistingFileWithin(targetRoot, duplicate); err != nil {
		return "", 0, fmt.Errorf("bestehendes Duplikatziel prüfen: %w", err)
	}
	sourceHash, sourceSize, _, err := checksumFileWithIdentity(ctx, source)
	if err != nil {
		return "", 0, fmt.Errorf("Duplikatquelle prüfen: %w", err)
	}
	if expectedSize > 0 && sourceSize != expectedSize {
		return "", 0, fmt.Errorf("Duplikatquelle wurde seit dem Plan verändert: %s", source)
	}
	targetHash, targetSize, _, err := checksumFileWithIdentity(ctx, duplicate)
	if err != nil {
		return "", 0, fmt.Errorf("bestehendes Duplikatziel prüfen: %w", err)
	}
	if sourceHash != targetHash || sourceSize != targetSize {
		return "", 0, fmt.Errorf("Quelle und bestehendes Ziel sind keine identischen Duplikate: %s", source)
	}
	return sourceHash, sourceSize, nil
}

func verifyDeduplicateTarget(ctx context.Context, duplicate, expectedHash string, expectedSize int64, targetRoot string) (os.FileInfo, error) {
	if strings.TrimSpace(targetRoot) == "" {
		targetRoot = filepath.Dir(duplicate)
	}
	if err := validateExistingFileWithin(targetRoot, duplicate); err != nil {
		return nil, fmt.Errorf("Duplikatziel unmittelbar vor der Quarantäne prüfen: %w", err)
	}
	actualHash, actualSize, identity, err := checksumFileWithIdentity(ctx, duplicate)
	if err != nil {
		return nil, fmt.Errorf("Duplikatziel unmittelbar vor der Quarantäne prüfen: %w", err)
	}
	if actualHash != expectedHash || actualSize != expectedSize {
		return nil, fmt.Errorf("Duplikatziel wurde vor der Quarantäne verändert: %s", duplicate)
	}
	return identity, nil
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	return copyWithContextProgress(ctx, destination, source, nil)
}

func copyWithContextProgress(ctx context.Context, destination io.Writer, source io.Reader, report func(int64)) (int64, error) {
	buffer := make([]byte, 1024*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != count {
				return total, io.ErrShortWrite
			}
			if report != nil {
				report(total)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func resultFromJournal(journal *Journal) domain.ExecutionResult {
	result := domain.ExecutionResult{
		JournalID: journal.ID, Status: journal.Status, Total: len(journal.Operations), Error: journal.Error,
	}
	retainedSources := make([]string, 0)
	proposalTotals := make(map[string]int)
	proposalCompleted := make(map[string]int)
	proposalFailed := make(map[string]bool)
	seenProposals := make(map[string]bool)
	for _, operation := range journal.Operations {
		proposalTotals[operation.ProposalID]++
		if !seenProposals[operation.ProposalID] {
			seenProposals[operation.ProposalID] = true
			result.ProposalIDs = append(result.ProposalIDs, operation.ProposalID)
		}
		if operation.Status == "completed" || operation.Status == "undone" {
			result.Completed++
			result.TotalBytes += operation.Size
			proposalCompleted[operation.ProposalID]++
		} else if operation.Status == "failed" || operation.Status == "transferring" {
			proposalFailed[operation.ProposalID] = true
		}
		if operation.Status == "completed" && operation.SourceRetained {
			retainedSources = append(retainedSources, operation.Source)
		}
	}
	if len(retainedSources) > 0 {
		example := retainedSources[0]
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d Quelldatei(en) konnten wegen fehlender Löschrechte nicht entfernt werden. Die vollständig geprüften Zieldateien wurden beibehalten. Beispiel: %s",
			len(retainedSources), example,
		))
	}
	result.ProposalResults = make([]domain.ProposalExecutionResult, 0, len(result.ProposalIDs))
	for _, proposalID := range result.ProposalIDs {
		proposalResult := domain.ProposalExecutionResult{
			ProposalID: proposalID,
			Status:     "pending",
			Completed:  proposalCompleted[proposalID],
			Total:      proposalTotals[proposalID],
		}
		switch {
		case proposalResult.Total > 0 && proposalResult.Completed == proposalResult.Total:
			proposalResult.Status = "completed"
		case proposalResult.Completed > 0 || proposalFailed[proposalID]:
			proposalResult.Status = "failed"
			proposalResult.Error = journal.Error
		}
		result.ProposalResults = append(result.ProposalResults, proposalResult)
	}
	return result
}

func reconcileInterrupted(ctx context.Context, operation *JournalOperation) error {
	sourceExists, err := regularFileExists(operation.Source)
	if err != nil {
		return err
	}
	targetExists, err := regularFileExists(operation.Target)
	if err != nil {
		return err
	}
	switch {
	case sourceExists && !targetExists:
		operation.Status = "pending"
		return nil
	case !sourceExists && targetExists:
		checksum, size, err := checksumFile(ctx, operation.Target)
		if err != nil {
			return err
		}
		operation.SHA256 = checksum
		operation.Size = size
		operation.Status = "completed"
		return nil
	case sourceExists && targetExists:
		sourceHash, sourceSize, sourceErr := checksumFile(ctx, operation.Source)
		targetHash, targetSize, targetErr := checksumFile(ctx, operation.Target)
		if sourceErr != nil {
			return sourceErr
		}
		if targetErr != nil {
			return targetErr
		}
		if sourceHash != targetHash || sourceSize != targetSize {
			return fmt.Errorf("unterbrochene Operation ist mehrdeutig; Quelle und Ziel unterscheiden sich: %s", operation.Source)
		}
		operation.SHA256 = targetHash
		operation.Size = targetSize
		operation.SourceRetained = true
		operation.SourceRemoveError = "App wurde nach geprüfter Kopie beendet oder Quelle konnte nicht entfernt werden"
		operation.Status = "completed"
		return nil
	default:
		return fmt.Errorf("unterbrochene Operation ist unvollständig; Quelle und Ziel fehlen: %s", operation.Source)
	}
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("Pfad ist keine reguläre Datei: %s", path)
	}
	return true, nil
}

func randomID(now time.Time) (string, error) {
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(bytes), nil
}

// Journals returns a bounded newest-first window. All older journal files stay
// on disk (and remain directly undoable by ID), but opening the history can
// never deserialize an unbounded number of attacker-sized documents.
func (s *Service) Journals() ([]Journal, error) {
	directory, err := s.directory()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() > entries[right].Name() })
	result := make([]Journal, 0, min(len(entries), maxHistoryJournals))
	var loadedBytes int64
	inspected := 0
	for _, entry := range entries {
		if len(result) >= maxHistoryJournals || inspected >= maxHistoryEntriesInspected {
			break
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		inspected++
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxJournalBytes {
			continue
		}
		if loadedBytes > 0 && loadedBytes+info.Size() > maxHistoryBytes {
			break
		}
		journal, loadErr := s.load(strings.TrimSuffix(entry.Name(), ".json"))
		if loadErr == nil {
			result = append(result, *journal)
			loadedBytes += info.Size()
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].CreatedAt.After(result[right].CreatedAt) })
	return result, nil
}

// Result returns the validated result reconstructed from one persisted journal.
// It intentionally does not reconcile or mutate interrupted work; callers can
// safely use it during startup to compare durable executor state with UI state.
func (s *Service) Result(journalID string) (domain.ExecutionResult, error) {
	journal, err := s.load(journalID)
	if err != nil {
		return domain.ExecutionResult{}, err
	}
	return resultFromJournal(journal), nil
}

func (s *Service) History() ([]domain.ImportRun, error) {
	journals, err := s.Journals()
	if err != nil {
		return nil, err
	}
	history := make([]domain.ImportRun, 0, len(journals))
	for _, journal := range journals {
		run := domain.ImportRun{
			JournalID: journal.ID, Status: journal.Status, TargetRoot: journal.TargetRoot,
			CreatedAt: journal.CreatedAt, UpdatedAt: journal.UpdatedAt, Total: len(journal.Operations),
			Error: journal.Error,
		}
		for _, operation := range journal.Operations {
			run.TotalBytes += operation.Size
			if operation.Status == "completed" || operation.Status == "undone" {
				run.Completed++
				if operation.Status == "completed" {
					run.CanUndo = true
				}
			} else if operation.Status == "transferring" {
				run.CanUndo = true
			}
		}
		if journal.Status == "undone" {
			run.CanUndo = false
		}
		history = append(history, run)
	}
	return history, nil
}
