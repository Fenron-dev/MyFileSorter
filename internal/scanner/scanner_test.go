package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dennis/myfilesorter/internal/domain"
)

type stubReader struct {
	values map[string]domain.EmbeddedMetadata
}

func (stubReader) Available() bool { return true }

func (s stubReader) Read(_ context.Context, path string) (domain.EmbeddedMetadata, error) {
	return s.values[filepath.Base(path)], nil
}

func TestScanGroupsNestedTracksAndKeepsRootFilesSeparate(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "book")
	if err := os.Mkdir(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(bookDir, "track 2.m4b"),
		filepath.Join(bookDir, "track 1.m4b"),
		filepath.Join(root, "standalone.m4b"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reader := stubReader{values: map[string]domain.EmbeddedMetadata{
		"track 1.m4b":    {Album: "Ein Buch", AlbumArtist: "Eine Autorin", Track: 1},
		"track 2.m4b":    {Album: "Ein Buch", AlbumArtist: "Eine Autorin", Track: 2},
		"standalone.m4b": {Title: "Einzelbuch", Artist: "Ein Autor"},
	}}

	result, err := New(reader).Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Books != 2 || result.Summary.Files != 3 {
		t.Fatalf("unexpected summary: %#v", result.Summary)
	}
	var grouped domain.BookProposal
	for _, proposal := range result.Proposals {
		if len(proposal.Files) == 2 {
			grouped = proposal
		}
	}
	if grouped.Metadata.Title != "Ein Buch" || grouped.Metadata.Author != "Eine Autorin" {
		t.Fatalf("unexpected proposal: %#v", grouped)
	}
	if grouped.Files[0].Track != 1 || grouped.Files[1].Track != 2 {
		t.Fatalf("tracks not sorted: %#v", grouped.Files)
	}
}
