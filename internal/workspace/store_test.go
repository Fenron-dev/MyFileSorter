package workspace

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

func TestStoreKeepsLatestValidSnapshot(t *testing.T) {
	store := New(t.TempDir())
	source := t.TempDir()
	first := []domain.BookProposal{{ID: "first", SourceRoot: source, GroupPath: source, Status: domain.StatusReviewRequired}}
	second := []domain.BookProposal{{ID: "second", SourceRoot: source, GroupPath: source, Status: domain.StatusConfirmed}}
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].ID != "second" || loaded[0].Status != domain.StatusConfirmed {
		t.Fatalf("unexpected snapshot: %#v", loaded)
	}
}

func TestStoreClearRemovesSnapshots(t *testing.T) {
	store := New(t.TempDir())
	source := t.TempDir()
	if err := store.Save([]domain.BookProposal{{ID: "book", SourceRoot: source, GroupPath: source, Status: domain.StatusReviewRequired}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected empty workspace, got %#v", loaded)
	}
}

func TestStoreRejectsPathsOutsideSourceRoot(t *testing.T) {
	store := New(t.TempDir())
	source := t.TempDir()
	proposal := domain.BookProposal{
		ID: "book", SourceRoot: source, GroupPath: source, Status: domain.StatusReviewRequired,
		Files: []domain.AudioFile{{Path: filepath.Join(filepath.Dir(source), "outside.mp3"), Name: "outside.mp3"}},
	}
	if err := store.Save([]domain.BookProposal{proposal}); err == nil {
		t.Fatal("expected invalid snapshot to be rejected")
	}
}

func TestValidSnapshotRequiresVersionAndTimestamp(t *testing.T) {
	source := t.TempDir()
	snapshot := Snapshot{
		Version: snapshotVersion, SavedAt: time.Now(),
		Proposals: []domain.BookProposal{{ID: "book", SourceRoot: source, GroupPath: source, Status: domain.StatusReviewRequired}},
	}
	if !validSnapshot(snapshot) {
		t.Fatal("expected valid snapshot")
	}
	snapshot.SavedAt = time.Time{}
	if validSnapshot(snapshot) {
		t.Fatal("expected zero timestamp to be rejected")
	}
}

func TestStoreUsesMonotoneGenerationWhenClockMovesBackwards(t *testing.T) {
	directory := t.TempDir()
	source := t.TempDir()
	store := New(directory)
	store.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	first := []domain.BookProposal{{ID: "first", SourceRoot: source, GroupPath: source, Status: domain.StatusReviewRequired}}
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) }
	second := []domain.BookProposal{{ID: "second", SourceRoot: source, GroupPath: source, Status: domain.StatusConfirmed}}
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].ID != "second" {
		t.Fatalf("clock rollback restored stale workspace: %#v", loaded)
	}
}

func TestGenerationFilenameParsingRejectsOverflow(t *testing.T) {
	if _, found := generationFromName("workspace-g99999999999999999999-deadbeef.json"); found {
		t.Fatal("overflowing generation must be rejected")
	}
	if generation, found := generationFromName("workspace-g00000000000000000042-deadbeef.json"); !found || generation != 42 {
		t.Fatalf("unexpected parsed generation: %d, %v", generation, found)
	}
}
