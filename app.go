package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dennis/myfilesorter/internal/applog"
	"github.com/dennis/myfilesorter/internal/domain"
	"github.com/dennis/myfilesorter/internal/executor"
	"github.com/dennis/myfilesorter/internal/llm"
	"github.com/dennis/myfilesorter/internal/metadata"
	"github.com/dennis/myfilesorter/internal/planner"
	"github.com/dennis/myfilesorter/internal/providers"
	"github.com/dennis/myfilesorter/internal/scanner"
	"github.com/dennis/myfilesorter/internal/workspace"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const planLifetime = 30 * time.Minute

type storedPlan struct {
	plan     domain.OperationPlan
	revision uint64
}

type storedMatches struct {
	revision   uint64
	candidates map[string]domain.MetadataCandidate
}

type storedAISuggestion struct {
	revision   uint64
	suggestion domain.AISuggestion
}

type App struct {
	ctx       context.Context
	scanner   *scanner.Scanner
	providers *providers.Registry
	executor  *executor.Service
	ai        *llm.Service
	logger    *applog.Logger
	workspace *workspace.Store
	mu        sync.RWMutex
	proposals []domain.BookProposal
	matches   map[string]storedMatches
	executed  map[string][]string
	aiResults map[string]storedAISuggestion
	plans     map[string]storedPlan
	revision  uint64

	jobMu      sync.Mutex
	jobStateMu sync.RWMutex
	activeJob  domain.ActiveJob
	jobCancel  context.CancelFunc
}

func NewApp() *App {
	return &App{
		scanner:   scanner.New(metadata.NewFFProbeReader()),
		providers: providers.NewRegistry(nil),
		executor:  executor.New(""),
		ai:        llm.New(""),
		logger:    applog.New(""),
		workspace: workspace.New(""),
		matches:   make(map[string]storedMatches),
		executed:  make(map[string][]string),
		aiResults: make(map[string]storedAISuggestion),
		plans:     make(map[string]storedPlan),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.logger.Info("app", "App-Sitzung gestartet", nil)
	proposals, err := a.workspace.Load()
	if err != nil {
		a.logger.Warn("workspace", "Gespeicherter Arbeitsstand konnte nicht geladen werden", map[string]string{"error": err.Error()})
		return
	}
	if len(proposals) == 0 {
		return
	}
	a.mu.Lock()
	a.proposals = cloneProposals(proposals)
	a.revision++
	a.mu.Unlock()
	a.logger.Info("workspace", "Gespeicherter Arbeitsstand wiederhergestellt", map[string]string{"proposals": strconv.Itoa(len(proposals))})
	a.reconcileWorkspaceFromJournals()
}

// reconcileWorkspaceFromJournals makes the durable file-operation journal the
// source of truth after a crash between moving files and saving the UI state.
// Only the newest journal for a proposal is considered, so an old Undo can
// never overwrite a later import.
func (a *App) reconcileWorkspaceFromJournals() {
	history, err := a.executor.History()
	if err != nil {
		a.logger.Warn("workspace", "Importjournale konnten beim Start nicht abgeglichen werden", map[string]string{"error": err.Error()})
		return
	}
	sort.SliceStable(history, func(left, right int) bool {
		if history[left].CreatedAt.Equal(history[right].CreatedAt) {
			return history[left].JournalID > history[right].JournalID
		}
		return history[left].CreatedAt.After(history[right].CreatedAt)
	})
	type durableExecution struct {
		run    domain.ImportRun
		result domain.ProposalExecutionResult
	}
	latest := make(map[string]durableExecution)
	for _, run := range history {
		result, resultErr := a.executor.Result(run.JournalID)
		if resultErr != nil {
			a.logger.Warn("workspace", "Importjournal konnte nicht rekonstruiert werden", map[string]string{"journalId": run.JournalID, "error": resultErr.Error()})
			continue
		}
		a.executed[run.JournalID] = append([]string(nil), result.ProposalIDs...)
		for _, proposalResult := range result.ProposalResults {
			if _, found := latest[proposalResult.ProposalID]; !found {
				latest[proposalResult.ProposalID] = durableExecution{run: run, result: proposalResult}
			}
		}
	}

	a.mu.Lock()
	changed := 0
	for index := range a.proposals {
		proposal := &a.proposals[index]
		durable, found := latest[proposal.ID]
		if !found {
			continue
		}
		beforeStatus := proposal.Status
		beforeJournal := proposal.ExecutionJournalID
		beforeWarnings := strings.Join(proposal.Warnings, "\x00")
		switch durable.run.Status {
		case "undone":
			if proposal.ExecutionJournalID == durable.run.JournalID || proposal.Status == domain.StatusImported || proposal.Status == domain.StatusError {
				proposal.Status = domain.StatusConfirmed
				proposal.ExecutionJournalID = ""
				clearExecutionWarning(proposal)
			}
		case "undoing", "undo_failed":
			if proposal.Status != domain.StatusReviewRequired && proposal.Status != domain.StatusExcluded {
				proposal.Status = domain.StatusError
				proposal.ExecutionJournalID = durable.run.JournalID
				proposal.Warnings = appendUnique(proposal.Warnings, "Undo wurde unterbrochen. Prüfe das Journal "+durable.run.JournalID+" und die betroffenen Dateien.")
			}
		default:
			if proposal.Status != domain.StatusReviewRequired && proposal.Status != domain.StatusExcluded {
				applyProposalExecutionResult(proposal, durable.result, durable.run.JournalID)
			}
		}
		if proposal.Status != beforeStatus || proposal.ExecutionJournalID != beforeJournal || strings.Join(proposal.Warnings, "\x00") != beforeWarnings {
			changed++
		}
	}
	if changed > 0 {
		a.invalidatePlansLocked()
		if persistErr := a.persistWorkspaceLocked(); persistErr != nil {
			a.logger.Warn("workspace", "Journalabgleich konnte nicht dauerhaft gespeichert werden", map[string]string{"error": persistErr.Error(), "proposals": strconv.Itoa(changed)})
		} else {
			a.logger.Info("workspace", "Arbeitsstand mit Importjournalen abgeglichen", map[string]string{"proposals": strconv.Itoa(changed)})
		}
	}
	a.mu.Unlock()
}

func (a *App) SelectDirectory(title string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
}

func (a *App) Scan(source string) (domain.ScanResult, error) {
	ctx, finish, err := a.beginJob("scan")
	if err != nil {
		return domain.ScanResult{}, err
	}
	defer finish()
	a.logger.Info("scan", "Lokaler Scan gestartet", map[string]string{"source": source})
	result, err := a.scanner.ScanWithProgress(ctx, source, func(progress domain.ScanProgress) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "scan:progress", progress)
		}
	})
	if err != nil {
		a.logger.Error("scan", "Lokaler Scan fehlgeschlagen", map[string]string{"source": source, "error": err.Error()})
		return domain.ScanResult{}, err
	}
	a.mu.Lock()
	a.proposals = cloneProposals(result.Proposals)
	a.matches = make(map[string]storedMatches)
	a.aiResults = make(map[string]storedAISuggestion)
	a.invalidatePlansLocked()
	if persistErr := a.persistWorkspaceLocked(); persistErr != nil {
		result.GlobalNotes = append(result.GlobalNotes, "Arbeitsstand konnte nicht dauerhaft gespeichert werden: "+persistErr.Error())
		a.logger.Warn("workspace", "Arbeitsstand konnte nach Scan nicht gespeichert werden", map[string]string{"error": persistErr.Error()})
	}
	a.mu.Unlock()
	a.logger.Info("scan", "Lokaler Scan abgeschlossen", map[string]string{
		"source": result.Source, "books": strconv.Itoa(result.Summary.Books), "audioFiles": strconv.Itoa(result.Summary.Files),
		"ebooks": strconv.Itoa(result.Summary.Ebooks), "sidecars": strconv.Itoa(result.Summary.Sidecars),
	})
	return result, nil
}

func (a *App) GetProposals() []domain.BookProposal {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneProposals(a.proposals)
}

func (a *App) UpdateProposal(id string, update domain.BookMetadata) (domain.BookProposal, error) {
	release, err := a.beginMutation()
	if err != nil {
		return domain.BookProposal{}, err
	}
	defer release()
	a.mu.Lock()
	defer a.mu.Unlock()
	proposal, err := a.findProposal(id)
	if err != nil {
		return domain.BookProposal{}, err
	}
	update.Title = strings.TrimSpace(update.Title)
	update.Author = strings.TrimSpace(update.Author)
	update.Series = strings.TrimSpace(update.Series)
	update.SeriesSequence = strings.TrimSpace(update.SeriesSequence)
	update.EditionInfo = strings.TrimSpace(update.EditionInfo)
	update.Narrator = strings.TrimSpace(update.Narrator)
	update.Language = strings.TrimSpace(update.Language)
	update.ASIN = strings.TrimSpace(update.ASIN)
	update.ISBN = strings.TrimSpace(update.ISBN)
	if err := validateMetadata(update); err != nil {
		return domain.BookProposal{}, err
	}
	if update.Title == "" || update.Author == "" {
		return domain.BookProposal{}, fmt.Errorf("Titel und Autor sind erforderlich")
	}
	if update.Evidence == nil {
		update.Evidence = make(map[string]domain.Evidence)
	}
	previous := cloneProposal(*proposal)
	markManualChanges(proposal.Metadata, &update)
	proposal.Metadata = update
	proposal.Status = domain.StatusReviewRequired
	proposal.Confidence = (update.Evidence["title"].Confidence + update.Evidence["author"].Confidence) / 2
	if err := a.persistWorkspaceLocked(); err != nil {
		*proposal = previous
		a.logger.Error("workspace", "Bearbeiteter Vorschlag konnte nicht dauerhaft gespeichert werden", map[string]string{"proposalId": id, "error": err.Error()})
		return domain.BookProposal{}, fmt.Errorf("Änderung konnte nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	a.logger.Info("review", "Metadatenvorschlag bearbeitet", map[string]string{"proposalId": id, "title": update.Title})
	return cloneProposal(*proposal), nil
}

func (a *App) SetProposalStatus(id string, status domain.ProposalStatus) (domain.BookProposal, error) {
	release, err := a.beginMutation()
	if err != nil {
		return domain.BookProposal{}, err
	}
	defer release()
	a.mu.Lock()
	defer a.mu.Unlock()
	proposal, err := a.findProposal(id)
	if err != nil {
		return domain.BookProposal{}, err
	}
	if status != domain.StatusConfirmed && status != domain.StatusExcluded && status != domain.StatusReviewRequired {
		return domain.BookProposal{}, fmt.Errorf("unsupported status %q", status)
	}
	if status == domain.StatusConfirmed {
		if strings.TrimSpace(proposal.Metadata.Title) == "" || strings.TrimSpace(proposal.Metadata.Author) == "" {
			return domain.BookProposal{}, fmt.Errorf("Titel und Autor müssen vor der Bestätigung gesetzt sein")
		}
		if proposal.Metadata.Series != "" && proposal.Metadata.SeriesSequence == "" {
			return domain.BookProposal{}, fmt.Errorf("Für ein Serienbuch ist eine Bandnummer erforderlich")
		}
		if countIncludedTracks(proposal.Files) == 0 {
			return domain.BookProposal{}, fmt.Errorf("Mindestens eine Audiodatei muss ausgewählt sein")
		}
	}
	previousStatus := proposal.Status
	proposal.Status = status
	if err := a.persistWorkspaceLocked(); err != nil {
		proposal.Status = previousStatus
		return domain.BookProposal{}, fmt.Errorf("Auswahlstatus konnte nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	a.logger.Info("review", "Auswahlstatus geändert", map[string]string{"proposalId": id, "status": string(status)})
	return cloneProposal(*proposal), nil
}

func (a *App) BulkSetProposalStatus(ids []string, status domain.ProposalStatus) ([]domain.BookProposal, error) {
	release, err := a.beginMutation()
	if err != nil {
		return nil, err
	}
	defer release()
	if status != domain.StatusConfirmed && status != domain.StatusExcluded && status != domain.StatusReviewRequired {
		return nil, fmt.Errorf("unsupported status %q", status)
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = true
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("mindestens einen Vorschlag auswählen")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	indexes := make([]int, 0, len(selected))
	for index := range a.proposals {
		proposal := a.proposals[index]
		if !selected[proposal.ID] {
			continue
		}
		if status == domain.StatusConfirmed {
			if strings.TrimSpace(proposal.Metadata.Title) == "" || strings.TrimSpace(proposal.Metadata.Author) == "" {
				return nil, fmt.Errorf("%s: Titel und Autor müssen vor der Bestätigung gesetzt sein", proposal.ID)
			}
			if proposal.Metadata.Series != "" && proposal.Metadata.SeriesSequence == "" {
				return nil, fmt.Errorf("%s: Für ein Serienbuch ist eine Bandnummer erforderlich", proposal.ID)
			}
			if countIncludedTracks(proposal.Files) == 0 {
				return nil, fmt.Errorf("%s: Mindestens eine Audiodatei muss ausgewählt sein", proposal.ID)
			}
		}
		indexes = append(indexes, index)
		delete(selected, proposal.ID)
	}
	if len(selected) != 0 {
		return nil, fmt.Errorf("mindestens ein Vorschlag wurde nicht gefunden")
	}
	updated := make([]domain.BookProposal, 0, len(indexes))
	previousStatuses := make([]domain.ProposalStatus, len(indexes))
	for _, index := range indexes {
		previousStatuses[len(updated)] = a.proposals[index].Status
		a.proposals[index].Status = status
		updated = append(updated, cloneProposal(a.proposals[index]))
	}
	if err := a.persistWorkspaceLocked(); err != nil {
		for offset, index := range indexes {
			a.proposals[index].Status = previousStatuses[offset]
		}
		return nil, fmt.Errorf("Sammelstatus konnte nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	a.logger.Info("review", "Auswahlstatus gesammelt geändert", map[string]string{"count": strconv.Itoa(len(updated)), "status": string(status)})
	return updated, nil
}

func (a *App) BuildPlan(target string, options domain.PlanOptions) (domain.OperationPlan, error) {
	release, err := a.beginMutation()
	if err != nil {
		return domain.OperationPlan{}, err
	}
	defer release()
	if err := validatePlanOptions(options); err != nil {
		return domain.OperationPlan{}, err
	}
	a.mu.RLock()
	proposals := cloneProposals(a.proposals)
	revision := a.revision
	a.mu.RUnlock()
	plan, err := planner.BuildWithOptions(target, proposals, options)
	if err != nil {
		a.logger.Error("plan", "Operationsplan fehlgeschlagen", map[string]string{"target": target, "error": err.Error()})
		return domain.OperationPlan{}, err
	}
	plan.PlanID, err = secureID(16)
	if err != nil {
		return domain.OperationPlan{}, err
	}
	plan.Revision = revision
	plan.ExpiresAt = time.Now().Add(planLifetime)
	plan.Digest, err = planDigest(plan)
	if err != nil {
		return domain.OperationPlan{}, err
	}
	a.mu.Lock()
	if a.revision != revision {
		a.mu.Unlock()
		return domain.OperationPlan{}, fmt.Errorf("Vorschläge wurden während der Planerstellung geändert; bitte Vorschau erneut erstellen")
	}
	// Only the newest preview can ever be executed. This bounds memory and
	// makes a second preview an explicit revocation of the first one.
	a.plans = make(map[string]storedPlan)
	if plan.Executable {
		a.plans[plan.PlanID] = storedPlan{plan: clonePlan(plan), revision: revision}
	}
	a.mu.Unlock()
	a.logger.Info("plan", "Operationsplan erstellt", map[string]string{
		"planId": plan.PlanID, "digest": plan.Digest, "target": plan.TargetRoot,
		"operations": strconv.Itoa(len(plan.Operations)), "executable": strconv.FormatBool(plan.Executable),
		"bookNumberWidth": strconv.Itoa(options.BookNumberWidth), "trackNumberWidth": strconv.Itoa(options.TrackNumberWidth),
		"audioFileNaming": options.AudioFileNaming, "existingFilePolicy": options.ExistingFilePolicy,
	})
	return plan, nil
}

func (a *App) ExecutePlan(planID string) (domain.ExecutionResult, error) {
	ctx, finish, err := a.beginJob("execute")
	if err != nil {
		return domain.ExecutionResult{}, err
	}
	defer finish()
	planID = strings.TrimSpace(planID)
	a.mu.Lock()
	stored, found := a.plans[planID]
	if found {
		delete(a.plans, planID)
	}
	currentRevision := a.revision
	a.mu.Unlock()
	if !found {
		return domain.ExecutionResult{}, fmt.Errorf("der bestätigte Vorschauplan ist nicht mehr gültig; bitte Vorschau erneut erstellen")
	}
	plan := clonePlan(stored.plan)
	if stored.revision != currentRevision || plan.Revision != currentRevision {
		return domain.ExecutionResult{}, fmt.Errorf("Vorschläge wurden seit der Vorschau geändert; bitte Vorschau erneut erstellen")
	}
	if time.Now().After(plan.ExpiresAt) {
		return domain.ExecutionResult{}, fmt.Errorf("die Vorschau ist abgelaufen; bitte Vorschau erneut erstellen")
	}
	digest, digestErr := planDigest(plan)
	if digestErr != nil || digest != plan.Digest {
		return domain.ExecutionResult{}, fmt.Errorf("der Vorschauplan konnte nicht verifiziert werden")
	}
	if !plan.Executable || len(plan.Operations) == 0 {
		return domain.ExecutionResult{}, fmt.Errorf("der bestätigte Vorschauplan ist nicht ausführbar")
	}
	a.logger.Info("move", "Verschieben gestartet", map[string]string{
		"planId": plan.PlanID, "digest": plan.Digest, "target": plan.TargetRoot,
		"operations": strconv.Itoa(len(plan.Operations)),
	})
	const maxLoggedOperations = 200
	for index, operation := range plan.Operations {
		if index >= maxLoggedOperations {
			break
		}
		targetPath := operation.Target
		if operation.Action == "remove" {
			targetPath = "Undo-fähige Quarantäne"
		}
		a.logger.Info("move.file", "Dateioperation vorgesehen", map[string]string{
			"index": strconv.Itoa(index + 1), "action": operation.Action, "category": operation.Category,
			"source": operation.Source, "target": targetPath,
		})
	}
	if omitted := len(plan.Operations) - maxLoggedOperations; omitted > 0 {
		a.logger.Info("move.file", "Weitere Dateioperationen nicht einzeln protokolliert", map[string]string{"omitted": strconv.Itoa(omitted)})
	}
	result, err := a.executor.ExecuteWithProgress(ctx, plan, func(progress domain.ExecutionProgress) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "execution:progress", progress)
		}
	})
	result = attachProposalResults(plan, result, err)
	if result.JournalID != "" {
		a.mu.Lock()
		a.executed[result.JournalID] = append([]string(nil), result.ProposalIDs...)
		applyProposalExecutionResults(a.proposals, result.ProposalResults, result.JournalID)
		a.invalidatePlansLocked()
		if persistErr := a.persistWorkspaceLocked(); persistErr != nil {
			result.Warnings = append(result.Warnings, "Arbeitsstand konnte nicht gespeichert werden: "+persistErr.Error())
		}
		a.mu.Unlock()
		logDetails := map[string]string{
			"journalId": result.JournalID, "status": result.Status,
			"completed": strconv.Itoa(result.Completed), "total": strconv.Itoa(result.Total),
		}
		if err != nil && result.Error == "" {
			result.Error = err.Error()
		}
		if result.Error != "" {
			logDetails["error"] = result.Error
			a.logger.Error("move", "Verschieben nicht vollständig abgeschlossen", logDetails)
		} else if len(result.Warnings) > 0 {
			logDetails["warnings"] = strings.Join(result.Warnings, " | ")
			a.logger.Warn("move", "Übertragung abgeschlossen; Quelldateien teilweise beibehalten", logDetails)
		} else {
			a.logger.Info("move", "Verschieben abgeschlossen", logDetails)
		}
		return result, nil
	}
	if err != nil {
		a.logger.Error("move", "Verschieben fehlgeschlagen", map[string]string{"planId": plan.PlanID, "target": plan.TargetRoot, "error": err.Error()})
	}
	return result, err
}

func (a *App) UndoExecution(journalID string) (domain.ExecutionResult, error) {
	ctx, finish, beginErr := a.beginJob("undo")
	if beginErr != nil {
		return domain.ExecutionResult{}, beginErr
	}
	defer finish()
	a.logger.Info("undo", "Undo gestartet", map[string]string{"journalId": journalID})
	result, err := a.executor.Undo(ctx, journalID)
	if err == nil && result.Status == "undone" {
		a.mu.Lock()
		proposalIDs := result.ProposalIDs
		if len(proposalIDs) == 0 {
			proposalIDs = a.executed[journalID]
		}
		clearJournalExecutionState(a.proposals, proposalIDs, journalID)
		delete(a.executed, journalID)
		a.invalidatePlansLocked()
		if persistErr := a.persistWorkspaceLocked(); persistErr != nil {
			result.Warnings = append(result.Warnings, "Arbeitsstand konnte nach Undo nicht gespeichert werden: "+persistErr.Error())
		}
		a.mu.Unlock()
	}
	if result.JournalID != "" {
		details := map[string]string{"journalId": journalID, "status": result.Status}
		if result.Error != "" {
			details["error"] = result.Error
			a.logger.Error("undo", "Undo nicht vollständig abgeschlossen", details)
		} else {
			a.logger.Info("undo", "Undo abgeschlossen", details)
		}
		return result, err
	}
	if err != nil {
		a.logger.Error("undo", "Undo fehlgeschlagen", map[string]string{"journalId": journalID, "error": err.Error()})
	}
	return result, err
}

func (a *App) GetActiveJob() domain.ActiveJob {
	a.jobStateMu.RLock()
	defer a.jobStateMu.RUnlock()
	return a.activeJob
}

func (a *App) CancelActiveJob() error {
	a.jobStateMu.RLock()
	cancel := a.jobCancel
	job := a.activeJob
	a.jobStateMu.RUnlock()
	if cancel == nil || !job.Active {
		return fmt.Errorf("derzeit läuft kein abbrechbarer Vorgang")
	}
	cancel()
	a.logger.Warn("job", "Abbruch angefordert", map[string]string{"jobId": job.ID, "kind": job.Kind})
	return nil
}

func (a *App) ClearWorkspace() error {
	release, err := a.beginMutation()
	if err != nil {
		return err
	}
	defer release()
	if err := a.workspace.Clear(); err != nil {
		return err
	}
	a.mu.Lock()
	a.proposals = nil
	a.matches = make(map[string]storedMatches)
	a.aiResults = make(map[string]storedAISuggestion)
	a.invalidatePlansLocked()
	a.mu.Unlock()
	a.logger.Info("workspace", "Arbeitsstand verworfen", nil)
	return nil
}

func (a *App) UpdateTrack(proposalID string, update domain.TrackUpdate) (domain.BookProposal, error) {
	release, err := a.beginMutation()
	if err != nil {
		return domain.BookProposal{}, err
	}
	defer release()
	update.Path = filepath.Clean(strings.TrimSpace(update.Path))
	update.TargetTitle = strings.TrimSpace(update.TargetTitle)
	if update.Path == "." || update.Path == "" {
		return domain.BookProposal{}, fmt.Errorf("Trackpfad ist erforderlich")
	}
	if len([]rune(update.TargetTitle)) > 300 || update.Track < 0 || update.Track > 999999 || update.Disc < 0 || update.Disc > 9999 {
		return domain.BookProposal{}, fmt.Errorf("ungültige Trackdaten")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	proposal, err := a.findProposal(proposalID)
	if err != nil {
		return domain.BookProposal{}, err
	}
	previous := cloneProposal(*proposal)
	found := false
	for index := range proposal.Files {
		if filepath.Clean(proposal.Files[index].Path) != update.Path {
			continue
		}
		proposal.Files[index].TargetTitle = update.TargetTitle
		proposal.Files[index].Track = update.Track
		proposal.Files[index].Disc = update.Disc
		proposal.Files[index].Excluded = update.Excluded
		found = true
		break
	}
	if !found {
		return domain.BookProposal{}, fmt.Errorf("Track wurde im Vorschlag nicht gefunden")
	}
	proposal.Status = domain.StatusReviewRequired
	if err := a.persistWorkspaceLocked(); err != nil {
		*proposal = previous
		return domain.BookProposal{}, fmt.Errorf("Trackänderung konnte nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	return cloneProposal(*proposal), nil
}

func (a *App) ReorderTracks(proposalID string, orderedPaths []string) (domain.BookProposal, error) {
	release, err := a.beginMutation()
	if err != nil {
		return domain.BookProposal{}, err
	}
	defer release()
	a.mu.Lock()
	defer a.mu.Unlock()
	proposal, err := a.findProposal(proposalID)
	if err != nil {
		return domain.BookProposal{}, err
	}
	if len(orderedPaths) != len(proposal.Files) {
		return domain.BookProposal{}, fmt.Errorf("die neue Reihenfolge muss jeden Track genau einmal enthalten")
	}
	byPath := make(map[string]domain.AudioFile, len(proposal.Files))
	for _, file := range proposal.Files {
		byPath[filepath.Clean(file.Path)] = file
	}
	reordered := make([]domain.AudioFile, 0, len(orderedPaths))
	seen := make(map[string]bool, len(orderedPaths))
	for _, path := range orderedPaths {
		path = filepath.Clean(path)
		file, found := byPath[path]
		if !found || seen[path] {
			return domain.BookProposal{}, fmt.Errorf("ungültiger oder doppelter Trackpfad")
		}
		seen[path] = true
		reordered = append(reordered, file)
	}
	previous := cloneProposal(*proposal)
	for index := range reordered {
		reordered[index].Track = index + 1
	}
	proposal.Files = reordered
	proposal.Status = domain.StatusReviewRequired
	if err := a.persistWorkspaceLocked(); err != nil {
		*proposal = previous
		return domain.BookProposal{}, fmt.Errorf("Trackreihenfolge konnte nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	return cloneProposal(*proposal), nil
}

func (a *App) MergeProposals(ids []string) (domain.BookProposal, error) {
	release, err := a.beginMutation()
	if err != nil {
		return domain.BookProposal{}, err
	}
	defer release()
	if len(ids) < 2 {
		return domain.BookProposal{}, fmt.Errorf("mindestens zwei Vorschläge zum Zusammenführen auswählen")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = true
		}
	}
	if len(selected) < 2 {
		return domain.BookProposal{}, fmt.Errorf("mindestens zwei unterschiedliche Vorschläge zum Zusammenführen auswählen")
	}
	items := make([]domain.BookProposal, 0, len(selected))
	insertAt := -1
	for index, proposal := range a.proposals {
		if selected[proposal.ID] {
			if insertAt < 0 {
				insertAt = index
			}
			items = append(items, cloneProposal(proposal))
		}
	}
	if len(items) != len(selected) {
		return domain.BookProposal{}, fmt.Errorf("mindestens ein Vorschlag wurde nicht gefunden")
	}
	previousProposals := cloneProposals(a.proposals)
	root := filepath.Clean(items[0].SourceRoot)
	for _, item := range items[1:] {
		if root != filepath.Clean(item.SourceRoot) {
			return domain.BookProposal{}, fmt.Errorf("nur Vorschläge aus demselben Scan können zusammengeführt werden")
		}
	}
	merged := cloneProposal(items[0])
	merged.ID, err = secureID(12)
	if err != nil {
		return domain.BookProposal{}, err
	}
	merged.ID = "manual-" + merged.ID
	merged.Files = nil
	merged.Companions = nil
	merged.Warnings = nil
	companionPaths := make(map[string]bool)
	audioPaths := make(map[string]bool)
	groups := make([]string, 0, len(items))
	for _, item := range items {
		for _, file := range item.Files {
			path := filepath.Clean(file.Path)
			if audioPaths[path] {
				return domain.BookProposal{}, fmt.Errorf("Audiodatei ist mehreren Vorschlägen zugeordnet: %s", file.Name)
			}
			audioPaths[path] = true
			merged.Files = append(merged.Files, file)
		}
		groups = append(groups, item.GroupPath)
		for _, companion := range item.Companions {
			path := filepath.Clean(companion.Path)
			if !companionPaths[path] {
				companionPaths[path] = true
				merged.Companions = append(merged.Companions, companion)
			}
		}
		for _, warning := range item.Warnings {
			merged.Warnings = appendUnique(merged.Warnings, warning)
		}
	}
	merged.GroupPath = commonDirectory(groups, root)
	merged.Status = domain.StatusReviewRequired
	merged.Warnings = appendUnique(merged.Warnings, "Vorschläge wurden manuell zusammengeführt; Trackreihenfolge und Metadaten bitte prüfen.")
	updated := make([]domain.BookProposal, 0, len(a.proposals)-len(items)+1)
	inserted := false
	for index, proposal := range a.proposals {
		if index == insertAt && !inserted {
			updated = append(updated, merged)
			inserted = true
		}
		if !selected[proposal.ID] {
			updated = append(updated, proposal)
		}
	}
	a.proposals = updated
	if err := a.persistWorkspaceLocked(); err != nil {
		a.proposals = previousProposals
		return domain.BookProposal{}, fmt.Errorf("Zusammengeführter Vorschlag konnte nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	return cloneProposal(merged), nil
}

func (a *App) SplitProposal(proposalID string, selectedPaths []string) ([]domain.BookProposal, error) {
	release, err := a.beginMutation()
	if err != nil {
		return nil, err
	}
	defer release()
	a.mu.Lock()
	defer a.mu.Unlock()
	proposal, err := a.findProposal(proposalID)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(selectedPaths))
	for _, path := range selectedPaths {
		selected[filepath.Clean(path)] = true
	}
	left := make([]domain.AudioFile, 0, len(proposal.Files))
	right := make([]domain.AudioFile, 0, len(selected))
	for _, file := range proposal.Files {
		if selected[filepath.Clean(file.Path)] {
			right = append(right, file)
		} else {
			left = append(left, file)
		}
	}
	if len(left) == 0 || len(right) == 0 || len(right) != len(selected) {
		return nil, fmt.Errorf("zum Aufteilen müssen auf beiden Seiten gültige Tracks verbleiben")
	}
	previousProposals := cloneProposals(a.proposals)
	created := cloneProposal(*proposal)
	created.ID, err = secureID(12)
	if err != nil {
		return nil, err
	}
	created.ID = "manual-" + created.ID
	created.Files = right
	created.Companions = nil
	created.GroupPath = commonFileDirectory(right, proposal.SourceRoot)
	created.Status = domain.StatusReviewRequired
	created.Warnings = appendUnique(created.Warnings, "Manuell abgetrennter Vorschlag; Titel, Begleitdateien und Reihenfolge bitte prüfen.")
	proposal.Files = left
	proposal.GroupPath = commonFileDirectory(left, proposal.SourceRoot)
	proposal.Status = domain.StatusReviewRequired
	proposal.Warnings = appendUnique(proposal.Warnings, "Vorschlag wurde manuell aufgeteilt; verbleibende Tracks bitte prüfen.")
	for index := range a.proposals {
		if a.proposals[index].ID == proposalID {
			a.proposals = append(a.proposals[:index+1], append([]domain.BookProposal{created}, a.proposals[index+1:]...)...)
			break
		}
	}
	if err := a.persistWorkspaceLocked(); err != nil {
		a.proposals = previousProposals
		return nil, fmt.Errorf("Aufgeteilte Vorschläge konnten nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	return []domain.BookProposal{cloneProposal(*proposal), cloneProposal(created)}, nil
}

func (a *App) UpdateCompanion(proposalID, path string, kind domain.CompanionKind) (domain.BookProposal, error) {
	release, err := a.beginMutation()
	if err != nil {
		return domain.BookProposal{}, err
	}
	defer release()
	if kind != domain.CompanionEbook && kind != domain.CompanionDiscard && kind != domain.CompanionUnknown {
		return domain.BookProposal{}, fmt.Errorf("unbekannte Begleitdatei-Behandlung %q", kind)
	}
	path = filepath.Clean(strings.TrimSpace(path))
	a.mu.Lock()
	defer a.mu.Unlock()
	proposal, err := a.findProposal(proposalID)
	if err != nil {
		return domain.BookProposal{}, err
	}
	previous := cloneProposal(*proposal)
	found := false
	for index := range proposal.Companions {
		if filepath.Clean(proposal.Companions[index].Path) == path {
			proposal.Companions[index].Kind = kind
			found = true
			break
		}
	}
	if !found {
		return domain.BookProposal{}, fmt.Errorf("Begleitdatei wurde im Vorschlag nicht gefunden")
	}
	proposal.Status = domain.StatusReviewRequired
	if err := a.persistWorkspaceLocked(); err != nil {
		*proposal = previous
		return domain.BookProposal{}, fmt.Errorf("Begleitdatei-Änderung konnte nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	return cloneProposal(*proposal), nil
}

func (a *App) MoveCompanions(sourceProposalID, targetProposalID string, paths []string) ([]domain.BookProposal, error) {
	release, err := a.beginMutation()
	if err != nil {
		return nil, err
	}
	defer release()
	if sourceProposalID == targetProposalID {
		return nil, fmt.Errorf("Quell- und Zielvorschlag müssen verschieden sein")
	}
	selected := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path = filepath.Clean(strings.TrimSpace(path)); path != "." && path != "" {
			selected[path] = true
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("mindestens eine Begleitdatei auswählen")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	sourceIndex, targetIndex := -1, -1
	for index := range a.proposals {
		switch a.proposals[index].ID {
		case sourceProposalID:
			sourceIndex = index
		case targetProposalID:
			targetIndex = index
		}
	}
	if sourceIndex < 0 || targetIndex < 0 {
		return nil, fmt.Errorf("Quell- oder Zielvorschlag wurde nicht gefunden")
	}
	previousProposals := cloneProposals(a.proposals)
	source := &a.proposals[sourceIndex]
	target := &a.proposals[targetIndex]
	if filepath.Clean(source.SourceRoot) != filepath.Clean(target.SourceRoot) {
		return nil, fmt.Errorf("Begleitdateien können nur innerhalb desselben Scans verschoben werden")
	}
	targetPaths := make(map[string]bool, len(target.Companions))
	for _, companion := range target.Companions {
		targetPaths[filepath.Clean(companion.Path)] = true
	}
	remaining := make([]domain.CompanionFile, 0, len(source.Companions))
	moving := make([]domain.CompanionFile, 0, len(selected))
	for _, companion := range source.Companions {
		clean := filepath.Clean(companion.Path)
		if selected[clean] {
			if targetPaths[clean] {
				return nil, fmt.Errorf("Begleitdatei ist dem Ziel bereits zugeordnet: %s", companion.Name)
			}
			moving = append(moving, companion)
			delete(selected, clean)
		} else {
			remaining = append(remaining, companion)
		}
	}
	if len(selected) != 0 {
		return nil, fmt.Errorf("mindestens eine Begleitdatei wurde nicht gefunden")
	}
	source.Companions = remaining
	target.Companions = append(target.Companions, moving...)
	source.Status = domain.StatusReviewRequired
	target.Status = domain.StatusReviewRequired
	if err := a.persistWorkspaceLocked(); err != nil {
		a.proposals = previousProposals
		return nil, fmt.Errorf("Begleitdatei-Zuordnung konnte nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	return []domain.BookProposal{cloneProposal(*source), cloneProposal(*target)}, nil
}

func (a *App) GetImportHistory() ([]domain.ImportRun, error) {
	history, err := a.executor.History()
	if err != nil {
		a.logger.Error("history", "Importverlauf konnte nicht geladen werden", map[string]string{"error": err.Error()})
		return nil, err
	}
	return history, nil
}

func (a *App) SearchOnline(id, provider, region string) ([]domain.MetadataCandidate, error) {
	ctx, finish, beginErr := a.beginJob("online-search")
	if beginErr != nil {
		return nil, beginErr
	}
	defer finish()
	a.mu.RLock()
	proposal, err := a.findProposal(id)
	if err != nil {
		a.mu.RUnlock()
		return nil, err
	}
	query := domain.MetadataSearchQuery{
		Title: proposal.Metadata.Title, Author: proposal.Metadata.Author, Narrator: proposal.Metadata.Narrator,
		ASIN: proposal.Metadata.ASIN, ISBN: proposal.Metadata.ISBN, Language: proposal.Metadata.Language, Region: region,
	}
	if proposal.Metadata.Evidence["title"].Source == "fallback" {
		query.Title = ""
	}
	if proposal.Metadata.Evidence["author"].Source == "fallback" {
		query.Author = ""
	}
	revision := a.revision
	a.mu.RUnlock()
	return a.searchOnline(ctx, id, provider, normaliseSearchQuery(query), revision)
}

func (a *App) SearchOnlineWithQuery(id, provider string, query domain.MetadataSearchQuery) ([]domain.MetadataCandidate, error) {
	ctx, finish, beginErr := a.beginJob("online-search")
	if beginErr != nil {
		return nil, beginErr
	}
	defer finish()
	query = normaliseSearchQuery(query)
	if err := validateSearchQuery(query); err != nil {
		return nil, err
	}
	a.mu.RLock()
	_, err := a.findProposal(id)
	if err != nil {
		a.mu.RUnlock()
		return nil, err
	}
	revision := a.revision
	a.mu.RUnlock()
	return a.searchOnline(ctx, id, provider, query, revision)
}


func (a *App) searchOnline(ctx context.Context, id, provider string, query domain.MetadataSearchQuery, revision uint64) ([]domain.MetadataCandidate, error) {
	if err := validateSearchQuery(query); err != nil {
		return nil, err
	}
	a.logger.Info("online", "Onlineabgleich gestartet", map[string]string{"proposalId": id, "provider": provider, "region": query.Region})
	results, err := a.providers.Search(ctx, provider, query)
	if err != nil {
		a.logger.Error("online", "Onlineabgleich fehlgeschlagen", map[string]string{"proposalId": id, "provider": provider, "error": err.Error()})
		return nil, err
	}
	a.mu.Lock()
	if a.revision != revision {
		a.mu.Unlock()
		return nil, fmt.Errorf("Vorschlag wurde während der Online-Suche geändert; bitte Suche erneut starten")
	}
	byID := make(map[string]domain.MetadataCandidate, len(results))
	for _, candidate := range results {
		byID[candidate.ID] = candidate
	}
	a.matches[id] = storedMatches{revision: revision, candidates: byID}
	a.mu.Unlock()
	a.logger.Info("online", "Onlineabgleich abgeschlossen", map[string]string{"proposalId": id, "provider": provider, "results": strconv.Itoa(len(results))})
	return results, nil
}

func (a *App) ApplyOnlineCandidate(proposalID, candidateID string) (domain.BookProposal, error) {
	release, beginErr := a.beginMutation()
	if beginErr != nil {
		return domain.BookProposal{}, beginErr
	}
	defer release()
	a.mu.Lock()
	defer a.mu.Unlock()
	proposal, err := a.findProposal(proposalID)
	if err != nil {
		return domain.BookProposal{}, err
	}
	matches, found := a.matches[proposalID]
	if !found || matches.revision != a.revision {
		return domain.BookProposal{}, fmt.Errorf("Online-Suche ist nicht mehr aktuell; bitte erneut suchen")
	}
	candidate, found := matches.candidates[candidateID]
	if !found {
		return domain.BookProposal{}, fmt.Errorf("Online-Treffer %q wurde nicht gefunden", candidateID)
	}
	updatedMetadata := cloneProposal(*proposal).Metadata
	applyCandidate(&updatedMetadata, candidate)
	if err := validateMetadata(updatedMetadata); err != nil {
		return domain.BookProposal{}, fmt.Errorf("Online-Treffer enthält ungültige Metadaten: %w", err)
	}
	previous := cloneProposal(*proposal)
	proposal.Metadata = updatedMetadata
	proposal.Status = domain.StatusReviewRequired
	proposal.Confidence = candidate.Confidence
	if err := a.persistWorkspaceLocked(); err != nil {
		*proposal = previous
		return domain.BookProposal{}, fmt.Errorf("Online-Vorschlag konnte nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	delete(a.matches, proposalID)
	a.logger.Info("online", "Online-Treffer übernommen", map[string]string{
		"proposalId": proposalID, "candidateId": candidateID, "provider": candidate.Provider, "title": candidate.Title,
		"series": candidate.Series, "seriesSequence": candidate.SeriesSequence,
	})
	return cloneProposal(*proposal), nil
}

func (a *App) GetSessionLog() domain.LogSnapshot {
	return a.logger.Snapshot()
}

func (a *App) GetLogSessions() ([]domain.LogSessionInfo, error) {
	return a.logger.Sessions()
}

func (a *App) GetLogSession(sessionID string) (domain.LogSnapshot, error) {
	return a.logger.LoadSession(sessionID)
}

func (a *App) GetAIProfiles() ([]domain.AIProfile, error) {
	return a.ai.Profiles()
}

func (a *App) SaveAIProfile(input domain.AIProfileInput) (domain.AIProfile, error) {
	profile, err := a.ai.SaveProfile(input)
	if err != nil {
		a.logger.Error("ai.profile", "AI-Profil konnte nicht gespeichert werden", map[string]string{"provider": input.Provider, "error": err.Error()})
		return domain.AIProfile{}, err
	}
	a.logger.Info("ai.profile", "AI-Profil gespeichert", map[string]string{
		"profileId": profile.ID, "provider": profile.Provider, "model": profile.Model,
	})
	return profile, nil
}

func (a *App) DeleteAIProfile(id string) error {
	if err := a.ai.DeleteProfile(id); err != nil {
		a.logger.Error("ai.profile", "AI-Profil konnte nicht gelöscht werden", map[string]string{"profileId": id, "error": err.Error()})
		return err
	}
	a.logger.Info("ai.profile", "AI-Profil gelöscht", map[string]string{"profileId": id})
	return nil
}

func (a *App) TestAIProfile(id string) error {
	ctx, finish, beginErr := a.beginJob("ai-test")
	if beginErr != nil {
		return beginErr
	}
	defer finish()
	a.logger.Info("ai.profile", "AI-Profiltest gestartet", map[string]string{"profileId": id})
	if err := a.ai.TestProfile(ctx, id); err != nil {
		a.logger.Error("ai.profile", "AI-Profiltest fehlgeschlagen", map[string]string{"profileId": id, "error": err.Error()})
		return err
	}
	a.logger.Info("ai.profile", "AI-Profiltest erfolgreich", map[string]string{"profileId": id})
	return nil
}

func (a *App) AnalyzeWithAI(proposalID, profileID string) (domain.AISuggestion, error) {
	ctx, finish, beginErr := a.beginJob("ai-analysis")
	if beginErr != nil {
		return domain.AISuggestion{}, beginErr
	}
	defer finish()
	a.mu.RLock()
	proposal, err := a.findProposal(proposalID)
	if err != nil {
		a.mu.RUnlock()
		return domain.AISuggestion{}, err
	}
	input := cloneProposal(*proposal)
	revision := a.revision
	a.mu.RUnlock()
	a.logger.Info("ai", "AI-Analyse gestartet", map[string]string{
		"proposalId": proposalID, "profileId": profileID, "files": strconv.Itoa(len(input.Files)),
	})
	suggestion, err := a.ai.Analyze(ctx, profileID, input)
	if err != nil {
		a.logger.Error("ai", "AI-Analyse fehlgeschlagen", map[string]string{"proposalId": proposalID, "profileId": profileID, "error": err.Error()})
		return domain.AISuggestion{}, err
	}
	a.mu.Lock()
	if a.revision != revision {
		a.mu.Unlock()
		return domain.AISuggestion{}, fmt.Errorf("Vorschlag wurde während der AI-Analyse geändert; bitte Analyse erneut starten")
	}
	a.aiResults[proposalID] = storedAISuggestion{revision: revision, suggestion: suggestion}
	a.mu.Unlock()
	a.logger.Info("ai", "AI-Vorschlag empfangen", map[string]string{
		"proposalId": proposalID, "profileId": profileID, "confidence": strconv.FormatFloat(suggestion.Confidence, 'f', 2, 64),
	})
	return suggestion, nil
}

func (a *App) ApplyAISuggestion(proposalID string) (domain.BookProposal, error) {
	release, beginErr := a.beginMutation()
	if beginErr != nil {
		return domain.BookProposal{}, beginErr
	}
	defer release()
	a.mu.Lock()
	defer a.mu.Unlock()
	proposal, err := a.findProposal(proposalID)
	if err != nil {
		return domain.BookProposal{}, err
	}
	stored, found := a.aiResults[proposalID]
	if !found || stored.revision != a.revision {
		return domain.BookProposal{}, fmt.Errorf("Für dieses Hörbuch liegt kein AI-Vorschlag vor")
	}
	suggestion := stored.suggestion
	updatedMetadata := cloneProposal(*proposal).Metadata
	applyAISuggestion(&updatedMetadata, suggestion)
	if err := validateMetadata(updatedMetadata); err != nil {
		return domain.BookProposal{}, fmt.Errorf("AI-Vorschlag enthält ungültige Metadaten: %w", err)
	}
	previous := cloneProposal(*proposal)
	proposal.Metadata = updatedMetadata
	proposal.Status = domain.StatusReviewRequired
	proposal.Confidence = suggestion.Confidence
	if err := a.persistWorkspaceLocked(); err != nil {
		*proposal = previous
		return domain.BookProposal{}, fmt.Errorf("AI-Vorschlag konnte nicht dauerhaft gespeichert werden: %w", err)
	}
	a.invalidatePlansLocked()
	delete(a.aiResults, proposalID)
	a.logger.Info("ai", "AI-Vorschlag übernommen", map[string]string{
		"proposalId": proposalID, "profileId": suggestion.ProfileID, "title": suggestion.Title,
	})
	return cloneProposal(*proposal), nil
}

func (a *App) beginJob(kind string) (context.Context, func(), error) {
	if !a.jobMu.TryLock() {
		return nil, nil, fmt.Errorf("ein anderer Vorgang läuft bereits")
	}
	jobID, err := secureID(8)
	if err != nil {
		a.jobMu.Unlock()
		return nil, nil, err
	}
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	a.jobStateMu.Lock()
	a.activeJob = domain.ActiveJob{ID: jobID, Kind: kind, StartedAt: time.Now(), Active: true}
	a.jobCancel = cancel
	a.jobStateMu.Unlock()
	finish := func() {
		cancel()
		a.jobStateMu.Lock()
		a.activeJob = domain.ActiveJob{}
		a.jobCancel = nil
		a.jobStateMu.Unlock()
		a.jobMu.Unlock()
	}
	return ctx, finish, nil
}

func (a *App) beginMutation() (func(), error) {
	if !a.jobMu.TryLock() {
		return nil, fmt.Errorf("ein anderer Vorgang läuft bereits")
	}
	return a.jobMu.Unlock, nil
}

func (a *App) invalidatePlansLocked() {
	a.revision++
	a.plans = make(map[string]storedPlan)
}

func (a *App) prunePlansLocked(now time.Time) {
	for id, stored := range a.plans {
		if now.After(stored.plan.ExpiresAt) || stored.revision != a.revision {
			delete(a.plans, id)
		}
	}
}

func (a *App) persistWorkspaceLocked() error {
	return a.workspace.Save(cloneProposals(a.proposals))
}

func secureID(bytes int) (string, error) {
	data := make([]byte, bytes)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("sichere ID erzeugen: %w", err)
	}
	return hex.EncodeToString(data), nil
}

func planDigest(plan domain.OperationPlan) (string, error) {
	copy := clonePlan(plan)
	copy.PlanID = ""
	copy.Digest = ""
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func clonePlan(input domain.OperationPlan) domain.OperationPlan {
	input.Operations = append([]domain.PlannedOperation(nil), input.Operations...)
	input.Warnings = append([]string(nil), input.Warnings...)
	return input
}

func validatePlanOptions(options domain.PlanOptions) error {
	for label, width := range map[string]int{"Bandnummern": options.BookNumberWidth, "Tracknummern": options.TrackNumberWidth} {
		if width != 0 && width != -1 && (width < 1 || width > 6) {
			return fmt.Errorf("%s: Stellenzahl muss automatisch oder zwischen 1 und 6 liegen", label)
		}
	}
	if options.AudioFileNaming != "" && options.AudioFileNaming != "source_title" && options.AudioFileNaming != "book_title" {
		return fmt.Errorf("unbekannte Audiodatei-Benennung %q", options.AudioFileNaming)
	}
	if options.ExistingFilePolicy != "" && options.ExistingFilePolicy != "error" && options.ExistingFilePolicy != "skip_identical" {
		return fmt.Errorf("unbekannte Behandlung vorhandener Zieldateien %q", options.ExistingFilePolicy)
	}
	return nil
}

func validateMetadata(metadata domain.BookMetadata) error {
	fields := []struct {
		label   string
		value   string
		maximum int
	}{
		{"Titel", metadata.Title, 500}, {"Autor", metadata.Author, 300}, {"Serie", metadata.Series, 300},
		{"Band", metadata.SeriesSequence, 32}, {"Info", metadata.EditionInfo, 300}, {"Sprecher", metadata.Narrator, 300},
		{"Sprache", metadata.Language, 64}, {"ASIN", metadata.ASIN, 32}, {"ISBN", metadata.ISBN, 32},
	}
	for _, field := range fields {
		if len([]rune(field.value)) > field.maximum {
			return fmt.Errorf("%s ist zu lang (maximal %d Zeichen)", field.label, field.maximum)
		}
		if strings.ContainsRune(field.value, '\x00') {
			return fmt.Errorf("%s enthält ungültige Steuerzeichen", field.label)
		}
	}
	if len(metadata.Evidence) > 32 {
		return fmt.Errorf("zu viele Metadaten-Nachweise")
	}
	for key, evidence := range metadata.Evidence {
		if len([]rune(key)) > 64 || len([]rune(evidence.Value)) > 1000 || len([]rune(evidence.Source)) > 128 ||
			strings.ContainsRune(key, '\x00') || strings.ContainsRune(evidence.Value, '\x00') || strings.ContainsRune(evidence.Source, '\x00') ||
			math.IsNaN(evidence.Confidence) || math.IsInf(evidence.Confidence, 0) || evidence.Confidence < 0 || evidence.Confidence > 1 {
			return fmt.Errorf("ungültiger Metadaten-Nachweis %q", key)
		}
	}
	return nil
}

func validateSearchQuery(query domain.MetadataSearchQuery) error {
	fields := []struct {
		label   string
		value   string
		maximum int
	}{
		{"Titel", query.Title, 500}, {"Autor", query.Author, 300}, {"Sprecher", query.Narrator, 300},
		{"ASIN", query.ASIN, 32}, {"ISBN", query.ISBN, 32}, {"Sprache", query.Language, 64}, {"Region", query.Region, 16},
	}
	for _, field := range fields {
		if len([]rune(field.value)) > field.maximum || strings.ContainsRune(field.value, '\x00') {
			return fmt.Errorf("ungültiges Suchfeld %s", field.label)
		}
	}
	if query.Title == "" && query.ASIN == "" && query.ISBN == "" {
		return fmt.Errorf("für den Onlineabgleich mindestens Titel, ASIN oder ISBN angeben")
	}
	return nil
}

func normaliseSearchQuery(query domain.MetadataSearchQuery) domain.MetadataSearchQuery {
	query.Title = strings.TrimSpace(query.Title)
	query.Author = strings.TrimSpace(query.Author)
	query.Narrator = strings.TrimSpace(query.Narrator)
	query.ASIN = strings.TrimSpace(query.ASIN)
	query.ISBN = strings.TrimSpace(query.ISBN)
	query.Language = strings.TrimSpace(query.Language)
	query.Region = strings.TrimSpace(query.Region)
	return query
}

func attachProposalResults(plan domain.OperationPlan, result domain.ExecutionResult, executionErr error) domain.ExecutionResult {
	ids := uniqueProposalIDs(plan.Operations)
	result.ProposalIDs = append([]string(nil), ids...)
	totals := make(map[string]int, len(ids))
	completed := make(map[string]int, len(ids))
	for _, operation := range plan.Operations {
		totals[operation.ProposalID]++
	}
	completedLimit := result.Completed
	if completedLimit > len(plan.Operations) {
		completedLimit = len(plan.Operations)
	}
	for index := 0; index < completedLimit; index++ {
		completed[plan.Operations[index].ProposalID]++
	}
	failedProposal := ""
	if completedLimit < len(plan.Operations) && (executionErr != nil || result.Status == "failed") {
		failedProposal = plan.Operations[completedLimit].ProposalID
	}
	result.ProposalResults = make([]domain.ProposalExecutionResult, 0, len(ids))
	for _, id := range ids {
		item := domain.ProposalExecutionResult{ProposalID: id, Completed: completed[id], Total: totals[id], Status: "pending"}
		switch {
		case item.Total > 0 && item.Completed == item.Total:
			item.Status = "completed"
		case item.Completed > 0 || id == failedProposal:
			item.Status = "failed"
			item.Error = result.Error
		}
		result.ProposalResults = append(result.ProposalResults, item)
	}
	return result
}

func applyProposalExecutionResults(proposals []domain.BookProposal, results []domain.ProposalExecutionResult, journalID string) {
	byID := make(map[string]domain.ProposalExecutionResult, len(results))
	for _, result := range results {
		byID[result.ProposalID] = result
	}
	for index := range proposals {
		result, found := byID[proposals[index].ID]
		if !found {
			continue
		}
		applyProposalExecutionResult(&proposals[index], result, journalID)
	}
}

func applyProposalExecutionResult(proposal *domain.BookProposal, result domain.ProposalExecutionResult, journalID string) {
	proposal.ExecutionJournalID = journalID
	switch result.Status {
		case "completed":
		proposal.Status = domain.StatusImported
		clearExecutionWarning(proposal)
		case "failed":
		proposal.Status = domain.StatusError
		proposal.Warnings = appendUnique(proposal.Warnings, "Import wurde unterbrochen. Nutze das Journal "+journalID+" für Undo.")
		default:
		proposal.Status = domain.StatusConfirmed
	}
}

func clearExecutionWarning(proposal *domain.BookProposal) {
	warnings := proposal.Warnings[:0]
	for _, warning := range proposal.Warnings {
		if !strings.HasPrefix(warning, "Import wurde unterbrochen.") && !strings.HasPrefix(warning, "Undo wurde unterbrochen.") {
			warnings = append(warnings, warning)
		}
	}
	proposal.Warnings = warnings
}

func clearJournalExecutionState(proposals []domain.BookProposal, proposalIDs []string, journalID string) {
	selected := make(map[string]bool, len(proposalIDs))
	for _, id := range proposalIDs {
		selected[id] = true
	}
	for index := range proposals {
		if !selected[proposals[index].ID] {
			continue
		}
		if proposals[index].ExecutionJournalID != "" && proposals[index].ExecutionJournalID != journalID {
			continue
		}
		proposals[index].Status = domain.StatusConfirmed
		proposals[index].ExecutionJournalID = ""
		clearExecutionWarning(&proposals[index])
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func commonDirectory(paths []string, fallback string) string {
	fallback = filepath.Clean(fallback)
	if len(paths) == 0 {
		return fallback
	}
	common := filepath.Clean(paths[0])
	for _, path := range paths[1:] {
		path = filepath.Clean(path)
		for !pathWithin(common, path) {
			parent := filepath.Dir(common)
			if parent == common {
				return fallback
			}
			common = parent
		}
	}
	if !pathWithin(fallback, common) {
		return fallback
	}
	return common
}

func commonFileDirectory(files []domain.AudioFile, fallback string) string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, filepath.Dir(file.Path))
	}
	return commonDirectory(paths, fallback)
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func countIncludedTracks(files []domain.AudioFile) int {
	count := 0
	for _, file := range files {
		if !file.Excluded {
			count++
		}
	}
	return count
}

func (a *App) findProposal(id string) (*domain.BookProposal, error) {
	for index := range a.proposals {
		if a.proposals[index].ID == id {
			return &a.proposals[index], nil
		}
	}
	return nil, fmt.Errorf("proposal %q not found", id)
}

func markManualChanges(before domain.BookMetadata, after *domain.BookMetadata) {
	fields := []struct {
		key      string
		previous string
		current  string
	}{
		{"title", before.Title, after.Title},
		{"author", before.Author, after.Author},
		{"series", before.Series, after.Series},
		{"seriesSequence", before.SeriesSequence, after.SeriesSequence},
		{"editionInfo", before.EditionInfo, after.EditionInfo},
		{"narrator", before.Narrator, after.Narrator},
		{"language", before.Language, after.Language},
		{"asin", before.ASIN, after.ASIN},
		{"isbn", before.ISBN, after.ISBN},
	}
	for _, field := range fields {
		if field.previous != field.current {
			after.Evidence[field.key] = domain.Evidence{Value: field.current, Source: "manual", Confidence: 1}
		}
	}
}

func applyCandidate(meta *domain.BookMetadata, candidate domain.MetadataCandidate) {
	if meta.Evidence == nil {
		meta.Evidence = make(map[string]domain.Evidence)
	}
	source := "online:" + candidate.Provider
	fields := []struct {
		key   string
		value string
		set   func(string)
	}{
		{"title", candidate.Title, func(value string) { meta.Title = value }},
		{"author", candidate.Author, func(value string) { meta.Author = value }},
		{"series", candidate.Series, func(value string) { meta.Series = value }},
		{"seriesSequence", candidate.SeriesSequence, func(value string) { meta.SeriesSequence = value }},
		{"narrator", candidate.Narrator, func(value string) { meta.Narrator = value }},
		{"language", candidate.Language, func(value string) { meta.Language = value }},
		{"asin", candidate.ASIN, func(value string) { meta.ASIN = value }},
		{"isbn", candidate.ISBN, func(value string) { meta.ISBN = value }},
	}
	for _, field := range fields {
		value := strings.TrimSpace(field.value)
		if value == "" {
			continue
		}
		field.set(value)
		meta.Evidence[field.key] = domain.Evidence{Value: value, Source: source, Confidence: candidate.Confidence}
	}
}

func applyAISuggestion(meta *domain.BookMetadata, suggestion domain.AISuggestion) {
	if meta.Evidence == nil {
		meta.Evidence = make(map[string]domain.Evidence)
	}
	source := "ai:" + suggestion.ProfileID
	fields := []struct {
		key   string
		value string
		set   func(string)
	}{
		{"title", suggestion.Title, func(value string) { meta.Title = value }},
		{"author", suggestion.Author, func(value string) { meta.Author = value }},
		{"series", suggestion.Series, func(value string) { meta.Series = value }},
		{"seriesSequence", suggestion.SeriesSequence, func(value string) { meta.SeriesSequence = value }},
		{"editionInfo", suggestion.EditionInfo, func(value string) { meta.EditionInfo = value }},
		{"narrator", suggestion.Narrator, func(value string) { meta.Narrator = value }},
		{"language", suggestion.Language, func(value string) { meta.Language = value }},
	}
	for _, field := range fields {
		value := strings.TrimSpace(field.value)
		if value == "" {
			continue
		}
		field.set(value)
		meta.Evidence[field.key] = domain.Evidence{Value: value, Source: source, Confidence: suggestion.Confidence}
	}
}

func cloneProposals(input []domain.BookProposal) []domain.BookProposal {
	result := make([]domain.BookProposal, len(input))
	for index, proposal := range input {
		result[index] = cloneProposal(proposal)
	}
	return result
}

func cloneProposal(input domain.BookProposal) domain.BookProposal {
	input.Files = append([]domain.AudioFile(nil), input.Files...)
	input.Companions = append([]domain.CompanionFile(nil), input.Companions...)
	input.Warnings = append([]string(nil), input.Warnings...)
	evidence := make(map[string]domain.Evidence, len(input.Metadata.Evidence))
	for key, value := range input.Metadata.Evidence {
		evidence[key] = value
	}
	input.Metadata.Evidence = evidence
	return input
}

func uniqueProposalIDs(operations []domain.PlannedOperation) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, operation := range operations {
		if !seen[operation.ProposalID] {
			seen[operation.ProposalID] = true
			result = append(result, operation.ProposalID)
		}
	}
	return result
}
