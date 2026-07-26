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

func TestScanUsesStructuredFolderInsteadOfGenericTrackName(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "Bradford_Bates,_Michael_Anderle_-_Aufstieg_des_Großmeisters_03_-_Der_Zorn_des_Kardinals")
	if err := os.Mkdir(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01 - Opening Credits.mp3", "02 - Kapitel 1.mp3"} {
		if err := os.WriteFile(filepath.Join(bookDir, name), []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := New(nil).Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	proposal := result.Proposals[0]
	if proposal.Metadata.Title != "Der Zorn des Kardinals" {
		t.Fatalf("unexpected title: %q", proposal.Metadata.Title)
	}
	if proposal.Metadata.Author != "Bradford Bates, Michael Anderle" {
		t.Fatalf("unexpected author: %q", proposal.Metadata.Author)
	}
	if proposal.Metadata.Series != "Aufstieg des Großmeisters" || proposal.Metadata.SeriesSequence != "03" {
		t.Fatalf("unexpected series: %#v", proposal.Metadata)
	}
}

func TestScanTreatsDirectoryEndingInMP3AsFolderEvidence(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "Dem.Mikhailov.-.Die.Nullform.1.(Ungekürzt).ABOOK,.mp3")
	if err := os.Mkdir(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bookDir, "01 - Opening Credits.mp3"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := New(nil).Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	proposal := result.Proposals[0]
	if proposal.Metadata.Title != "Die Nullform 1" || proposal.Metadata.Author != "Dem Mikhailov" {
		t.Fatalf("unexpected folder inference: %#v", proposal.Metadata)
	}
}

func TestScanClassifiesEbooksAndDiscardableSidecars(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "Autor - Buch")
	if err := os.Mkdir(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01 - Kapitel.mp3", "Buch.epub", "Booklet.pdf", "folder.jpg", "playlist.m3u", "notes.docx"} {
		if err := os.WriteFile(filepath.Join(bookDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := New(nil).Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	companions := result.Proposals[0].Companions
	if len(companions) != 4 {
		t.Fatalf("unexpected companions: %#v", companions)
	}
	kinds := map[string]domain.CompanionKind{}
	for _, companion := range companions {
		kinds[companion.Name] = companion.Kind
	}
	if kinds["Buch.epub"] != domain.CompanionEbook || kinds["Booklet.pdf"] != domain.CompanionEbook {
		t.Fatalf("ebooks not classified: %#v", kinds)
	}
	if kinds["folder.jpg"] != domain.CompanionDiscard || kinds["playlist.m3u"] != domain.CompanionDiscard {
		t.Fatalf("sidecars not classified: %#v", kinds)
	}
}
