package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/dennis/myfilesorter/internal/domain"
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
	mu        sync.RWMutex
	proposals []domain.BookProposal
	matches   map[string]map[string]domain.MetadataCandidate
}

func NewApp() *App {
	return &App{
		scanner:   scanner.New(metadata.NewFFProbeReader()),
		providers: providers.NewRegistry(nil),
		matches:   make(map[string]map[string]domain.MetadataCandidate),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) SelectDirectory(title string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
}

func (a *App) Scan(source string) (domain.ScanResult, error) {
	result, err := a.scanner.Scan(a.ctx, source)
	if err != nil {
		return domain.ScanResult{}, err
	}
	a.mu.Lock()
	a.proposals = cloneProposals(result.Proposals)
	a.matches = make(map[string]map[string]domain.MetadataCandidate)
	a.mu.Unlock()
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
	return cloneProposal(*proposal), nil
}

func (a *App) BuildPlan(target string) (domain.OperationPlan, error) {
	a.mu.RLock()
	proposals := cloneProposals(a.proposals)
	a.mu.RUnlock()
	return planner.Build(target, proposals)
}

func (a *App) SearchOnline(id, provider, region string) ([]domain.MetadataCandidate, error) {
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
		return nil, err
	}
	a.mu.Lock()
	byID := make(map[string]domain.MetadataCandidate, len(results))
	for _, candidate := range results {
		byID[candidate.ID] = candidate
	}
	a.matches[id] = byID
	a.mu.Unlock()
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

func cloneProposals(input []domain.BookProposal) []domain.BookProposal {
	result := make([]domain.BookProposal, len(input))
	for index, proposal := range input {
		result[index] = cloneProposal(proposal)
	}
	return result
}

func cloneProposal(input domain.BookProposal) domain.BookProposal {
	input.Files = append([]domain.AudioFile(nil), input.Files...)
	input.Warnings = append([]string(nil), input.Warnings...)
	evidence := make(map[string]domain.Evidence, len(input.Metadata.Evidence))
	for key, value := range input.Metadata.Evidence {
		evidence[key] = value
	}
	input.Metadata.Evidence = evidence
	return input
}
