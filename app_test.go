package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dennis/myfilesorter/internal/applog"
	"github.com/dennis/myfilesorter/internal/domain"
	"github.com/dennis/myfilesorter/internal/executor"
	"github.com/dennis/myfilesorter/internal/workspace"
)

func TestBuildPlanStoresOnlyNewestImmutablePreview(t *testing.T) {
	root := t.TempDir()
	app := newTestApp(t, root)
	proposal := testProposal(t, root)
	app.proposals = []domain.BookProposal{proposal}
	app.revision = 1
	target := filepath.Join(root, "target")

	first, err := app.BuildPlan(target, domain.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Executable || first.PlanID == "" || len(app.plans) != 1 {
		t.Fatalf("unexpected first plan: %#v", first)
	}
	originalTarget := app.plans[first.PlanID].plan.Operations[0].Target
	first.Operations[0].Target = filepath.Join(root, "tampered")
	if app.plans[first.PlanID].plan.Operations[0].Target != originalTarget {
		t.Fatal("caller mutation changed the stored executable plan")
	}

	second, err := app.BuildPlan(target, domain.PlanOptions{TrackNumberWidth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(app.plans) != 1 {
		t.Fatalf("expected one bounded plan, got %d", len(app.plans))
	}
	if _, found := app.plans[first.PlanID]; found {
		t.Fatal("older preview was not revoked")
	}
	if _, found := app.plans[second.PlanID]; !found {
		t.Fatal("newest preview was not retained")
	}
}

func TestBulkStatusUpdateIsAtomic(t *testing.T) {
	root := t.TempDir()
	app := newTestApp(t, root)
	proposal := testProposal(t, root)
	proposal.Status = domain.StatusReviewRequired
	app.proposals = []domain.BookProposal{proposal}
	if _, err := app.BulkSetProposalStatus([]string{proposal.ID, "missing"}, domain.StatusConfirmed); err == nil {
		t.Fatal("expected missing proposal error")
	}
	if app.proposals[0].Status != domain.StatusReviewRequired {
		t.Fatal("partial bulk update changed a proposal")
	}
}

func TestActiveJobExcludesMutationsAndCanBeCancelled(t *testing.T) {
	app := newTestApp(t, t.TempDir())
	ctx, finish, err := app.beginJob("scan")
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if release, mutationErr := app.beginMutation(); mutationErr == nil {
		release()
		t.Fatal("mutation started while another job was active")
	}
	if err := app.CancelActiveJob(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("active job context was not cancelled")
	}
}

func TestStartupReconcilesCompletedImportAndUndo(t *testing.T) {
	root := t.TempDir()
	workspaceDirectory := filepath.Join(root, "workspace")
	journalDirectory := filepath.Join(root, "journals")
	store := workspace.New(workspaceDirectory)
	proposal := testProposal(t, root)
	if err := store.Save([]domain.BookProposal{proposal}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(proposal.Files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	targetRoot := filepath.Join(root, "library")
	if err := os.MkdirAll(targetRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	service := executor.New(journalDirectory)
	result, err := service.Execute(context.Background(), domain.OperationPlan{
		TargetRoot: targetRoot,
		Executable: true,
		Operations: []domain.PlannedOperation{{
			ProposalID: proposal.ID, SourceRoot: proposal.SourceRoot, Source: proposal.Files[0].Path,
			SourceModifiedNanos: info.ModTime().UnixNano(), Target: filepath.Join(targetRoot, "book.m4b"), Size: info.Size(),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	firstStart := newTestAppWithStores(root, store, service)
	firstStart.startup(context.Background())
	if len(firstStart.proposals) != 1 || firstStart.proposals[0].Status != domain.StatusImported || firstStart.proposals[0].ExecutionJournalID != result.JournalID {
		t.Fatalf("completed journal was not reconciled: %#v", firstStart.proposals)
	}
	if _, err := service.Undo(context.Background(), result.JournalID); err != nil {
		t.Fatal(err)
	}

	secondStart := newTestAppWithStores(root, store, service)
	secondStart.startup(context.Background())
	if len(secondStart.proposals) != 1 || secondStart.proposals[0].Status != domain.StatusConfirmed || secondStart.proposals[0].ExecutionJournalID != "" {
		t.Fatalf("undone journal was not reconciled: %#v", secondStart.proposals)
	}
}

func newTestApp(t *testing.T, root string) *App {
	t.Helper()
	return newTestAppWithStores(root, workspace.New(filepath.Join(root, "workspace")), executor.New(filepath.Join(root, "journals")))
}

func newTestAppWithStores(root string, store *workspace.Store, service *executor.Service) *App {
	return &App{
		ctx: context.Background(), executor: service, workspace: store,
		logger:  applog.New(filepath.Join(root, "logs")),
		matches: make(map[string]storedMatches), executed: make(map[string][]string),
		aiResults: make(map[string]storedAISuggestion), plans: make(map[string]storedPlan),
	}
}

func testProposal(t *testing.T, root string) domain.BookProposal {
	t.Helper()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "book.m4b")
	if err := os.WriteFile(path, []byte("audiobook"), 0o600); err != nil {
		t.Fatal(err)
	}
	return domain.BookProposal{
		ID: "book", SourceRoot: source, GroupPath: source, Status: domain.StatusConfirmed, Confidence: 1,
		Metadata: domain.BookMetadata{Title: "Titel", Author: "Autor", Evidence: map[string]domain.Evidence{}},
		Files:    []domain.AudioFile{{Path: path, Name: "book.m4b", Extension: ".m4b", Size: int64(len("audiobook")), Track: 1}},
	}
}
