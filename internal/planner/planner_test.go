package planner

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dennis/myfilesorter/internal/domain"
)

func TestBuildIncludesOnlyConfirmed(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	confirmed := domain.BookProposal{
		ID: "confirmed", SourceRoot: source, Status: domain.StatusConfirmed,
		Metadata: domain.BookMetadata{Author: "Autor", Title: "Titel"},
		Files:    []domain.AudioFile{{Path: filepath.Join(source, "book.m4b"), Extension: ".m4b", Size: 42}},
	}
	review := confirmed
	review.ID = "review"
	review.Status = domain.StatusReviewRequired

	plan, err := Build(target, []domain.BookProposal{confirmed, review})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].ProposalID != "confirmed" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if !plan.Executable {
		t.Fatalf("expected executable plan: %#v", plan)
	}
}

func TestBuildRejectsOverlappingRoots(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(source, "sorted")
	proposal := domain.BookProposal{
		ID: "book", SourceRoot: source, Status: domain.StatusConfirmed,
		Metadata: domain.BookMetadata{Author: "Autor", Title: "Titel"},
		Files:    []domain.AudioFile{{Path: filepath.Join(source, "book.m4b"), Extension: ".m4b"}},
	}
	if _, err := Build(target, []domain.BookProposal{proposal}); err == nil {
		t.Fatal("expected overlapping roots error")
	}
}

func TestBuildPlansEbooksAndOptionalSidecarCleanup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	proposal := domain.BookProposal{
		ID: "book", SourceRoot: source, Status: domain.StatusConfirmed,
		Metadata: domain.BookMetadata{Author: "Autor", Series: "Serie", SeriesSequence: "2", Title: "Titel"},
		Files:    []domain.AudioFile{{Path: filepath.Join(source, "audio.mp3"), Extension: ".mp3", Size: 10}},
		Companions: []domain.CompanionFile{
			{Path: filepath.Join(source, "book.epub"), Extension: ".epub", Size: 4, Kind: domain.CompanionEbook},
			{Path: filepath.Join(source, "folder.jpg"), Extension: ".jpg", Size: 2, Kind: domain.CompanionDiscard},
		},
	}
	plan, err := BuildWithOptions(target, []domain.BookProposal{proposal}, domain.PlanOptions{MoveEbooks: true, CleanupSidecars: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 3 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if plan.Operations[1].Category != "ebook" || !strings.Contains(plan.Operations[1].Target, filepath.Join("# Ebooks", "Autor", "Serie", "02 - Titel")) {
		t.Fatalf("unexpected ebook target: %#v", plan.Operations[1])
	}
	if plan.Operations[2].Action != "remove" || plan.Operations[2].Target != "" {
		t.Fatalf("unexpected cleanup operation: %#v", plan.Operations[2])
	}
}
