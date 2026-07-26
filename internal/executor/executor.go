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
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

const journalVersion = 1

type Service struct {
	journalDir string
	now        func() time.Time
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
	ProposalID string    `json:"proposalId"`
	Action     string    `json:"action"`
	Category   string    `json:"category"`
	Source     string    `json:"source"`
	Target     string    `json:"target"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256,omitempty"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updatedAt,omitempty"`
}

func New(journalDir string) *Service {
	return &Service{journalDir: journalDir, now: time.Now}
}

func (s *Service) Execute(ctx context.Context, plan domain.OperationPlan) (domain.ExecutionResult, error) {
	return s.ExecuteWithProgress(ctx, plan, nil)
}

func (s *Service) ExecuteWithProgress(ctx context.Context, plan domain.OperationPlan, report func(domain.ExecutionProgress)) (domain.ExecutionResult, error) {
	if !plan.Executable || len(plan.Operations) == 0 {
		return domain.ExecutionResult{}, fmt.Errorf("der Operationsplan ist nicht ausführbar")
	}
	totalBytes := plan.TotalBytes
	if totalBytes == 0 {
		for _, operation := range plan.Operations {
			totalBytes += operation.Size
		}
	}
	emitProgress(report, domain.ExecutionProgress{Status: "checking", Total: len(plan.Operations), TotalBytes: totalBytes})
	validatedPlan, preflightWarnings, err := preflightSources(plan)
	if err != nil {
		return domain.ExecutionResult{
			Status: "failed", Total: len(plan.Operations), Error: err.Error(), Warnings: preflightWarnings,
		}, err
	}
	plan = validatedPlan
	emitProgress(report, domain.ExecutionProgress{Status: "moving", Total: len(plan.Operations), TotalBytes: totalBytes})
	journal, err := s.newJournal(plan)
	if err != nil {
		return domain.ExecutionResult{}, err
	}
	if err := s.save(journal); err != nil {
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
		checksum, size, moveErr := transfer(ctx, op.Source, op.Target, op.Size)
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
	for index := range plan.Operations {
		operation := &plan.Operations[index]
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
		operation.Source = resolved
		if recovered {
			warnings = append(warnings, "Unicode-normalisierten Quellpfad wiederaufgelöst: "+resolved)
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
	return strings.NewReplacer(
		"a\u0308", "ä", "o\u0308", "ö", "u\u0308", "ü", "A\u0308", "Ä", "O\u0308", "Ö", "U\u0308", "Ü",
		"e\u0301", "é", "e\u0300", "è", "a\u0301", "á", "a\u0300", "à", "c\u0327", "ç", "n\u0303", "ñ",
	).Replace(value)
}

func withWarnings(result domain.ExecutionResult, warnings []string) domain.ExecutionResult {
	result.Warnings = append(result.Warnings, warnings...)
	return result
}

func (s *Service) Undo(ctx context.Context, journalID string) (domain.ExecutionResult, error) {
	journal, err := s.load(journalID)
	if err != nil {
		return domain.ExecutionResult{}, err
	}
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
		if op.Status == "transferring" {
			if err := reconcileInterrupted(ctx, op); err != nil {
				journal.Status = "undo_failed"
				journal.Error = err.Error()
				journal.UpdatedAt = s.now()
				_ = s.save(journal)
				return resultFromJournal(journal), err
			}
		}
		if op.Status != "completed" {
			continue
		}
		actual, _, hashErr := checksumFile(ctx, op.Target)
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
		if _, _, moveErr := transfer(ctx, op.Target, op.Source, op.Size); moveErr != nil {
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
		if action == "remove" {
			target, err = s.quarantineTarget(id, index, op.Source)
			if err != nil {
				return nil, err
			}
		}
		journal.Operations[index] = JournalOperation{
			ProposalID: op.ProposalID, Action: action, Category: op.Category,
			Source: op.Source, Target: target, Size: op.Size, Status: "pending",
		}
	}
	return journal, nil
}

func (s *Service) quarantineTarget(journalID string, index int, source string) (string, error) {
	directory, err := s.directory()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%04d-%s", index+1, filepath.Base(source))
	return filepath.Join(filepath.Dir(directory), "quarantine", journalID, name), nil
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
	directory, err := s.directory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
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
	if err := os.Rename(temporaryName, finalName); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	if err := os.Remove(finalName); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(temporaryName, finalName)
}

func (s *Service) load(journalID string) (*Journal, error) {
	if journalID == "" || strings.ContainsAny(journalID, `/\\`) || filepath.Base(journalID) != journalID {
		return nil, fmt.Errorf("ungültige Journal-ID")
	}
	directory, err := s.directory()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(directory, journalID+".json"))
	if err != nil {
		return nil, fmt.Errorf("Journal laden: %w", err)
	}
	var journal Journal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("Journal lesen: %w", err)
	}
	if journal.Version != journalVersion || journal.ID != journalID {
		return nil, fmt.Errorf("Journal ist ungültig oder inkompatibel")
	}
	return &journal, nil
}

func transfer(ctx context.Context, source, target string, expectedSize int64) (string, int64, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return "", 0, fmt.Errorf("Quelle prüfen: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("Quelle ist keine reguläre Datei: %s", source)
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return "", 0, fmt.Errorf("Quelldatei wurde seit dem Plan verändert: %s", source)
	}
	if _, err := os.Lstat(target); err == nil {
		return "", 0, fmt.Errorf("Zieldatei existiert bereits: %s", target)
	} else if !os.IsNotExist(err) {
		return "", 0, fmt.Errorf("Ziel prüfen: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", 0, fmt.Errorf("Zielordner anlegen: %w", err)
	}

	sourceFile, err := os.Open(source)
	if err != nil {
		return "", 0, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".myfilesorter-*.part")
	if err != nil {
		sourceFile.Close()
		return "", 0, err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	hash := sha256.New()
	written, copyErr := copyWithContext(ctx, io.MultiWriter(temporary, hash), sourceFile)
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	if closeErr := temporary.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if closeErr := sourceFile.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return "", 0, fmt.Errorf("Datei kopieren: %w", copyErr)
	}
	if written != info.Size() {
		return "", 0, fmt.Errorf("unvollständige Kopie: %s", target)
	}
	expectedHash := hex.EncodeToString(hash.Sum(nil))
	actualHash, actualSize, err := checksumFile(ctx, temporaryName)
	if err != nil {
		return "", 0, fmt.Errorf("Kopie prüfen: %w", err)
	}
	if expectedHash != actualHash || actualSize != written {
		return "", 0, fmt.Errorf("Prüfsummenvergleich fehlgeschlagen: %s", target)
	}
	if err := os.Chmod(temporaryName, info.Mode().Perm()); err != nil {
		return "", 0, err
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return "", 0, fmt.Errorf("Zieldatei finalisieren: %w", err)
	}
	if err := os.Remove(source); err != nil {
		rollbackErr := os.Remove(target)
		if rollbackErr != nil {
			return "", 0, fmt.Errorf("Quelle entfernen: %v; Ziel-Rollback fehlgeschlagen: %w", err, rollbackErr)
		}
		return "", 0, fmt.Errorf("Quelle entfernen: %w", err)
	}
	return expectedHash, written, nil
}

func checksumFile(ctx context.Context, path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := copyWithContext(ctx, hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
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
	for _, operation := range journal.Operations {
		if operation.Status == "completed" || operation.Status == "undone" {
			result.Completed++
			result.TotalBytes += operation.Size
		}
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
		return fmt.Errorf("unterbrochene Operation ist mehrdeutig; Quelle und Ziel existieren: %s", operation.Source)
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

// Journals returns newest journals first and is intentionally kept small for a later history UI.
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
	result := make([]Journal, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		journal, loadErr := s.load(strings.TrimSuffix(entry.Name(), ".json"))
		if loadErr == nil {
			result = append(result, *journal)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].CreatedAt.After(result[right].CreatedAt) })
	return result, nil
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
			if operation.Status == "completed" {
				run.Completed++
				run.CanUndo = true
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
