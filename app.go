package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/dennis/myfilesorter/internal/applog"
	"github.com/dennis/myfilesorter/internal/domain"
	"github.com/dennis/myfilesorter/internal/executor"
	"github.com/dennis/myfilesorter/internal/llm"
	"github.com/dennis/myfilesorter/internal/metadata"
	"github.com/dennis/myfilesorter/internal/planner"
	"github.com/dennis/myfilesorter/internal/providers"
	"github.com/dennis/myfilesorter/internal/scanner"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx       context.Context
	scanner   *scanner.Scanner
	providers *providers.Registry
	executor  *executor.Service
	ai        *llm.Service
	logger    *applog.Logger
	mu        sync.RWMutex
	proposals []domain.BookProposal
	matches   map[string]map[string]domain.MetadataCandidate
	executed  map[string][]string
	aiResults map[string]domain.AISuggestion
}

func NewApp() *App {
	return &App{
		scanner:   scanner.New(metadata.NewFFProbeReader()),
		providers: providers.NewRegistry(nil),
		executor:  executor.New(""),
		ai:        llm.New(""),
		logger:    applog.New(""),
		matches:   make(map[string]map[string]domain.MetadataCandidate),
		executed:  make(map[string][]string),
		aiResults: make(map[string]domain.AISuggestion),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.logger.Info("app", "App-Sitzung gestartet", nil)
}

func (a *App) SelectDirectory(title string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
}

func (a *App) Scan(source string) (domain.ScanResult, error) {
	a.logger.Info("scan", "Lokaler Scan gestartet", map[string]string{"source": source})
	result, err := a.scanner.Scan(a.ctx, source)
	if err != nil {
		a.logger.Error("scan", "Lokaler Scan fehlgeschlagen", map[string]string{"source": source, "error": err.Error()})
		return domain.ScanResult{}, err
	}
	a.mu.Lock()
	a.proposals = cloneProposals(result.Proposals)
	a.matches = make(map[string]map[string]domain.MetadataCandidate)
	a.aiResults = make(map[string]domain.AISuggestion)
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
	if update.Title == "" || update.Author == "" {
		return domain.BookProposal{}, fmt.Errorf("Titel und Autor sind erforderlich")
	}
	if update.Evidence == nil {
		update.Evidence = make(map[string]domain.Evidence)
	}
	markManualChanges(proposal.Metadata, &update)
	proposal.Metadata = update
	proposal.Status = domain.StatusReviewRequired
	proposal.Confidence = (update.Evidence["title"].Confidence + update.Evidence["author"].Confidence) / 2
	a.logger.Info("review", "Metadatenvorschlag bearbeitet", map[string]string{"proposalId": id, "title": update.Title})
	return cloneProposal(*proposal), nil
}

func (a *App) SetProposalStatus(id string, status domain.ProposalStatus) (domain.BookProposal, error) {
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
	}
	proposal.Status = status
	a.logger.Info("review", "Auswahlstatus geändert", map[string]string{"proposalId": id, "status": string(status)})
	return cloneProposal(*proposal), nil
}

func (a *App) BuildPlan(target string, options domain.PlanOptions) (domain.OperationPlan, error) {
	a.mu.RLock()
	proposals := cloneProposals(a.proposals)
	a.mu.RUnlock()
	plan, err := planner.BuildWithOptions(target, proposals, options)
	if err != nil {
		a.logger.Error("plan", "Operationsplan fehlgeschlagen", map[string]string{"target": target, "error": err.Error()})
		return domain.OperationPlan{}, err
	}
	a.logger.Info("plan", "Operationsplan erstellt", map[string]string{
		"target": plan.TargetRoot, "operations": strconv.Itoa(len(plan.Operations)), "executable": strconv.FormatBool(plan.Executable),
		"bookNumberWidth": strconv.Itoa(options.BookNumberWidth), "trackNumberWidth": strconv.Itoa(options.TrackNumberWidth),
		"audioFileNaming": options.AudioFileNaming,
	})
	return plan, nil
}

func (a *App) ExecutePlan(target string, options domain.PlanOptions) (domain.ExecutionResult, error) {
	a.logger.Info("move", "Verschieben gestartet", map[string]string{
		"target": target, "bookNumberWidth": strconv.Itoa(options.BookNumberWidth),
		"trackNumberWidth": strconv.Itoa(options.TrackNumberWidth), "audioFileNaming": options.AudioFileNaming,
	})
	a.mu.RLock()
	proposals := cloneProposals(a.proposals)
	a.mu.RUnlock()
	plan, err := planner.BuildWithOptions(target, proposals, options)
	if err != nil {
		a.logger.Error("move", "Verschiebeplan konnte nicht erstellt werden", map[string]string{"target": target, "error": err.Error()})
		return domain.ExecutionResult{}, err
	}
	if !plan.Executable {
		a.logger.Warn("move", "Verschieben durch Konflikte blockiert", map[string]string{"target": target})
		return domain.ExecutionResult{}, fmt.Errorf("der aktuelle Plan enthält Konflikte und kann nicht ausgeführt werden")
	}
	for index, operation := range plan.Operations {
		targetPath := operation.Target
		if operation.Action == "remove" {
			targetPath = "Undo-fähige Quarantäne"
		}
		a.logger.Info("move.file", "Dateioperation vorgesehen", map[string]string{
			"index": strconv.Itoa(index + 1), "action": operation.Action, "category": operation.Category,
			"source": operation.Source, "target": targetPath,
		})
	}
	result, err := a.executor.Execute(a.ctx, plan)
	if result.JournalID != "" {
		proposalIDs := uniqueProposalIDs(plan.Operations)
		a.mu.Lock()
		a.executed[result.JournalID] = proposalIDs
		if result.Status == "completed" {
			setStatuses(a.proposals, proposalIDs, domain.StatusImported)
		} else if result.Completed > 0 {
			setExecutionFailure(a.proposals, proposalIDs, result.JournalID)
		}
		a.mu.Unlock()
		logDetails := map[string]string{
			"journalId": result.JournalID, "status": result.Status,
			"completed": strconv.Itoa(result.Completed), "total": strconv.Itoa(result.Total),
		}
		if result.Error != "" {
			logDetails["error"] = result.Error
			a.logger.Error("move", "Verschieben nicht vollständig abgeschlossen", logDetails)
		} else {
			a.logger.Info("move", "Verschieben abgeschlossen", logDetails)
		}
		return result, nil
	}
	if err != nil {
		a.logger.Error("move", "Verschieben fehlgeschlagen", map[string]string{"target": target, "error": err.Error()})
	}
	return result, err
}

func (a *App) UndoExecution(journalID string) (domain.ExecutionResult, error) {
	a.logger.Info("undo", "Undo gestartet", map[string]string{"journalId": journalID})
	result, err := a.executor.Undo(a.ctx, journalID)
	if result.Status == "undone" {
		a.mu.Lock()
		setStatuses(a.proposals, a.executed[journalID], domain.StatusConfirmed)
		clearExecutionWarnings(a.proposals, a.executed[journalID])
		delete(a.executed, journalID)
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
		return result, nil
	}
	if err != nil {
		a.logger.Error("undo", "Undo fehlgeschlagen", map[string]string{"journalId": journalID, "error": err.Error()})
	}
	return result, err
}

func (a *App) SearchOnline(id, provider, region string) ([]domain.MetadataCandidate, error) {
	a.logger.Info("online", "Onlineabgleich gestartet", map[string]string{"proposalId": id, "provider": provider, "region": region})
	a.mu.RLock()
	proposal, err := a.findProposal(id)
	if err != nil {
		a.mu.RUnlock()
		return nil, err
	}
	query := domain.MetadataSearchQuery{
		Title:    proposal.Metadata.Title,
		Author:   proposal.Metadata.Author,
		Narrator: proposal.Metadata.Narrator,
		ASIN:     proposal.Metadata.ASIN,
		ISBN:     proposal.Metadata.ISBN,
		Language: proposal.Metadata.Language,
		Region:   region,
	}
	if proposal.Metadata.Evidence["title"].Source == "fallback" {
		query.Title = ""
	}
	if proposal.Metadata.Evidence["author"].Source == "fallback" {
		query.Author = ""
	}
	a.mu.RUnlock()

	results, err := a.providers.Search(a.ctx, provider, query)
	if err != nil {
		a.logger.Error("online", "Onlineabgleich fehlgeschlagen", map[string]string{"proposalId": id, "provider": provider, "error": err.Error()})
		return nil, err
	}
	a.mu.Lock()
	byID := make(map[string]domain.MetadataCandidate, len(results))
	for _, candidate := range results {
		byID[candidate.ID] = candidate
	}
	a.matches[id] = byID
	a.mu.Unlock()
	a.logger.Info("online", "Onlineabgleich abgeschlossen", map[string]string{"proposalId": id, "provider": provider, "results": strconv.Itoa(len(results))})
	return results, nil
}

func (a *App) ApplyOnlineCandidate(proposalID, candidateID string) (domain.BookProposal, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	proposal, err := a.findProposal(proposalID)
	if err != nil {
		return domain.BookProposal{}, err
	}
	candidate, found := a.matches[proposalID][candidateID]
	if !found {
		return domain.BookProposal{}, fmt.Errorf("Online-Treffer %q wurde nicht gefunden", candidateID)
	}
	applyCandidate(&proposal.Metadata, candidate)
	proposal.Status = domain.StatusReviewRequired
	proposal.Confidence = candidate.Confidence
	a.logger.Info("online", "Online-Treffer übernommen", map[string]string{
		"proposalId": proposalID, "candidateId": candidateID, "provider": candidate.Provider, "title": candidate.Title,
	})
	return cloneProposal(*proposal), nil
}

func (a *App) GetSessionLog() domain.LogSnapshot {
	return a.logger.Snapshot()
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
	a.logger.Info("ai.profile", "AI-Profiltest gestartet", map[string]string{"profileId": id})
	if err := a.ai.TestProfile(a.ctx, id); err != nil {
		a.logger.Error("ai.profile", "AI-Profiltest fehlgeschlagen", map[string]string{"profileId": id, "error": err.Error()})
		return err
	}
	a.logger.Info("ai.profile", "AI-Profiltest erfolgreich", map[string]string{"profileId": id})
	return nil
}

func (a *App) AnalyzeWithAI(proposalID, profileID string) (domain.AISuggestion, error) {
	a.mu.RLock()
	proposal, err := a.findProposal(proposalID)
	if err != nil {
		a.mu.RUnlock()
		return domain.AISuggestion{}, err
	}
	input := cloneProposal(*proposal)
	a.mu.RUnlock()
	a.logger.Info("ai", "AI-Analyse gestartet", map[string]string{
		"proposalId": proposalID, "profileId": profileID, "files": strconv.Itoa(len(input.Files)),
	})
	suggestion, err := a.ai.Analyze(a.ctx, profileID, input)
	if err != nil {
		a.logger.Error("ai", "AI-Analyse fehlgeschlagen", map[string]string{"proposalId": proposalID, "profileId": profileID, "error": err.Error()})
		return domain.AISuggestion{}, err
	}
	a.mu.Lock()
	a.aiResults[proposalID] = suggestion
	a.mu.Unlock()
	a.logger.Info("ai", "AI-Vorschlag empfangen", map[string]string{
		"proposalId": proposalID, "profileId": profileID, "confidence": strconv.FormatFloat(suggestion.Confidence, 'f', 2, 64),
	})
	return suggestion, nil
}

func (a *App) ApplyAISuggestion(proposalID string) (domain.BookProposal, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	proposal, err := a.findProposal(proposalID)
	if err != nil {
		return domain.BookProposal{}, err
	}
	suggestion, found := a.aiResults[proposalID]
	if !found {
		return domain.BookProposal{}, fmt.Errorf("Für dieses Hörbuch liegt kein AI-Vorschlag vor")
	}
	applyAISuggestion(&proposal.Metadata, suggestion)
	proposal.Status = domain.StatusReviewRequired
	proposal.Confidence = suggestion.Confidence
	a.logger.Info("ai", "AI-Vorschlag übernommen", map[string]string{
		"proposalId": proposalID, "profileId": suggestion.ProfileID, "title": suggestion.Title,
	})
	return cloneProposal(*proposal), nil
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

func setStatuses(proposals []domain.BookProposal, proposalIDs []string, status domain.ProposalStatus) {
	selected := make(map[string]bool, len(proposalIDs))
	for _, id := range proposalIDs {
		selected[id] = true
	}
	for index := range proposals {
		if selected[proposals[index].ID] {
			proposals[index].Status = status
		}
	}
}

func setExecutionFailure(proposals []domain.BookProposal, proposalIDs []string, journalID string) {
	selected := make(map[string]bool, len(proposalIDs))
	for _, id := range proposalIDs {
		selected[id] = true
	}
	for index := range proposals {
		if selected[proposals[index].ID] {
			proposals[index].Status = domain.StatusError
			proposals[index].Warnings = append(proposals[index].Warnings, "Import wurde unterbrochen. Nutze das Journal "+journalID+" für Undo.")
		}
	}
}

func clearExecutionWarnings(proposals []domain.BookProposal, proposalIDs []string) {
	selected := make(map[string]bool, len(proposalIDs))
	for _, id := range proposalIDs {
		selected[id] = true
	}
	for index := range proposals {
		if !selected[proposals[index].ID] {
			continue
		}
		warnings := proposals[index].Warnings[:0]
		for _, warning := range proposals[index].Warnings {
			if !strings.HasPrefix(warning, "Import wurde unterbrochen.") {
				warnings = append(warnings, warning)
			}
		}
		proposals[index].Warnings = warnings
	}
}
