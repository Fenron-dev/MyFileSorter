package planner

import (
	"path/filepath"
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
