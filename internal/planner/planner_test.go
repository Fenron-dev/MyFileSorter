package planner

import (
	"fmt"
	"os"
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
	writeProposalFiles(t, confirmed)

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
	writeProposalFiles(t, proposal)
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

func TestBuildUsesSelectedNumberWidthsAndSourceTrackTitles(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	proposal := domain.BookProposal{
		ID: "book", SourceRoot: source, Status: domain.StatusConfirmed,
		Metadata: domain.BookMetadata{Author: "Autor", Series: "Serie", SeriesSequence: "1", Title: "Titel"},
		Files: []domain.AudioFile{
			{Path: filepath.Join(source, "1.Opening_Credits.mp3"), Name: "1.Opening_Credits.mp3", Extension: ".mp3", Track: 1},
			{Path: filepath.Join(source, "2.Kapitel_1.mp3"), Name: "2.Kapitel_1.mp3", Extension: ".mp3", Track: 2},
		},
	}
	writeProposalFiles(t, proposal)
	plan, err := BuildWithOptions(target, []domain.BookProposal{proposal}, domain.PlanOptions{
		BookNumberWidth: 3, TrackNumberWidth: 3, AudioFileNaming: "source_title",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Operations[0].Target; !strings.Contains(got, filepath.Join("Serie", "001 - Titel", "001 - Opening Credits.mp3")) {
		t.Fatalf("unexpected target: %q", got)
	}
}

func TestBuildAutomaticWidthsFollowDetectedMaximum(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	proposals := []domain.BookProposal{
		{ID: "one", SourceRoot: source, Status: domain.StatusConfirmed, Metadata: domain.BookMetadata{Author: "Autor", Series: "Serie", SeriesSequence: "1", Title: "Eins"}, Files: make([]domain.AudioFile, 10)},
		{ID: "hundred", SourceRoot: source, Status: domain.StatusReviewRequired, Metadata: domain.BookMetadata{Author: "Autor", Series: "Serie", SeriesSequence: "100", Title: "Hundert"}},
	}
	for index := range proposals[0].Files {
		proposals[0].Files[index] = domain.AudioFile{Path: filepath.Join(source, fmt.Sprintf("%d.mp3", index+1)), Name: fmt.Sprintf("%d.mp3", index+1), Extension: ".mp3"}
	}
	writeProposalFiles(t, proposals[0])
	plan, err := BuildWithOptions(target, proposals, domain.PlanOptions{BookNumberWidth: -1, TrackNumberWidth: -1})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Operations[0].Target; !strings.Contains(got, filepath.Join("Serie", "001 - Eins", "01 - Eins.mp3")) {
		t.Fatalf("unexpected automatic target: %q", got)
	}
}

func TestBuildRenumbersDuplicateDiscTrackNumbersGlobally(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	proposal := domain.BookProposal{
		ID: "multidisc", SourceRoot: source, Status: domain.StatusConfirmed,
		Metadata: domain.BookMetadata{Author: "Autor", Title: "Titel"},
		Files: []domain.AudioFile{
			{Path: filepath.Join(source, "cd1-01.mp3"), Extension: ".mp3", Disc: 1, Track: 1},
			{Path: filepath.Join(source, "cd1-02.mp3"), Extension: ".mp3", Disc: 1, Track: 2},
			{Path: filepath.Join(source, "cd2-01.mp3"), Extension: ".mp3", Disc: 2, Track: 1},
		},
	}
	writeProposalFiles(t, proposal)
	plan, err := Build(target, []domain.BookProposal{proposal})
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"01 - Titel.mp3", "02 - Titel.mp3", "03 - Titel.mp3"} {
		if got := filepath.Base(plan.Operations[index].Target); got != want {
			t.Fatalf("operation %d target = %q, want %q", index, got, want)
		}
		if plan.Operations[index].SourceRoot == "" {
			t.Fatalf("operation %d has no bound source root", index)
		}
	}
}

func TestBuildCanPlanVerifiedDeduplicationPolicy(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	proposal := domain.BookProposal{
		ID: "book", SourceRoot: source, Status: domain.StatusConfirmed,
		Metadata: domain.BookMetadata{Author: "Autor", Title: "Titel"},
		Files: []domain.AudioFile{{
			Path: filepath.Join(source, "book.m4b"), Extension: ".m4b", Size: int64(len("audio")),
		}},
	}
	writeProposalFiles(t, proposal)
	existing := filepath.Join(target, "Autor", "Titel", "01 - Titel.m4b")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildWithOptions(target, []domain.BookProposal{proposal}, domain.PlanOptions{ExistingFilePolicy: "skip_identical"})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Executable || len(plan.Operations) != 1 || plan.Operations[0].Action != "deduplicate" {
		t.Fatalf("unexpected deduplication plan: %#v", plan)
	}
}

func writeProposalFiles(t *testing.T, proposal domain.BookProposal) {
	t.Helper()
	for _, file := range proposal.Files {
		if err := os.MkdirAll(filepath.Dir(file.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.Path, []byte("audio"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range proposal.Companions {
		if err := os.MkdirAll(filepath.Dir(file.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.Path, []byte("companion"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
