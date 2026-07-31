package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

type stubReader struct {
	values map[string]domain.EmbeddedMetadata
}

func (stubReader) Available() bool { return true }

func (s stubReader) Read(_ context.Context, path string) (domain.EmbeddedMetadata, error) {
	return s.values[filepath.Base(path)], nil
}

type failingReader struct{}

func (failingReader) Available() bool { return true }

func (failingReader) Read(_ context.Context, path string) (domain.EmbeddedMetadata, error) {
	if strings.HasPrefix(filepath.Base(path), "02") {
		return domain.EmbeddedMetadata{}, errors.New("beschädigter Metadatenblock")
	}
	return domain.EmbeddedMetadata{Album: "Fehlertolerantes Buch", AlbumArtist: "Test Autor"}, nil
}

type concurrentReader struct {
	active  int32
	maximum int32
	calls   int32
}

func (*concurrentReader) Available() bool { return true }

func (r *concurrentReader) Read(_ context.Context, _ string) (domain.EmbeddedMetadata, error) {
	atomic.AddInt32(&r.calls, 1)
	active := atomic.AddInt32(&r.active, 1)
	for {
		maximum := atomic.LoadInt32(&r.maximum)
		if active <= maximum || atomic.CompareAndSwapInt32(&r.maximum, maximum, active) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	atomic.AddInt32(&r.active, -1)
	return domain.EmbeddedMetadata{Album: "Parallel gelesen", AlbumArtist: "Test Autor"}, nil
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
	if proposal.Metadata.Title != "Die Nullform" || proposal.Metadata.Author != "Dem Mikhailov" {
		t.Fatalf("unexpected folder inference: %#v", proposal.Metadata)
	}
	if proposal.Metadata.Series != "Die Nullform" || proposal.Metadata.SeriesSequence != "1" || proposal.Metadata.EditionInfo != "Ungekürzt" {
		t.Fatalf("unexpected sequence or edition info: %#v", proposal.Metadata)
	}
}

func TestScanNormalizesTrailingVolumeNumber(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "Dem.Mikhailov.-.Die.Nullform.05")
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
	metadata := result.Proposals[0].Metadata
	if metadata.Title != "Die Nullform" || metadata.Series != "Die Nullform" || metadata.SeriesSequence != "05" {
		t.Fatalf("unexpected numbered folder inference: %#v", metadata)
	}
}

func TestScanRecognizesDecomposedUnicodeEditionMarker(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "Dem.Mikhailov.-.Die.Nullform.1.(Ungeku\u0308rzt),.ABOOK,.mp3")
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
	metadata := result.Proposals[0].Metadata
	if metadata.Title != "Die Nullform" || metadata.Series != "Die Nullform" || metadata.SeriesSequence != "1" || metadata.EditionInfo != "Ungekürzt" {
		t.Fatalf("unexpected decomposed unicode inference: %#v", metadata)
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
	if len(companions) != 5 {
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
	if kinds["notes.docx"] != domain.CompanionUnknown {
		t.Fatalf("unknown companion should stay visible without being actionable: %#v", kinds)
	}
	if warnings := strings.Join(result.Proposals[0].Warnings, " "); !strings.Contains(warnings, "nicht klassifizierte Begleitdatei") {
		t.Fatalf("missing unknown companion warning: %q", warnings)
	}
}

func TestScanGroupsDiscDirectoriesAndOrdersTracksByDisc(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "Autor - Serie 02 - Titel")
	for _, disc := range []string{"CD 1", "Disc_2"} {
		if err := os.MkdirAll(filepath.Join(bookDir, disc), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"01 - Kapitel.mp3", "02 - Kapitel.mp3"} {
			if err := os.WriteFile(filepath.Join(bookDir, disc, name), []byte("audio"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	result, err := New(nil).Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Proposals) != 1 || len(result.Proposals[0].Files) != 4 {
		t.Fatalf("disc directories were not grouped: %#v", result.Proposals)
	}
	proposal := result.Proposals[0]
	if proposal.Metadata.Title != "Titel" || proposal.Metadata.Series != "Serie" || proposal.Metadata.SeriesSequence != "02" {
		t.Fatalf("unexpected folder metadata: %#v", proposal.Metadata)
	}
	if proposal.Files[0].Disc != 1 || proposal.Files[1].Disc != 1 || proposal.Files[2].Disc != 2 || proposal.Files[3].Disc != 2 {
		t.Fatalf("unexpected disc ordering: %#v", proposal.Files)
	}
}

func TestDiscDirectoryNumberAcceptsCommonLayouts(t *testing.T) {
	for name, expected := range map[string]int{"CD1": 1, "Disc_02": 2, "Disk 3 von 12": 3, "CD 4 of 9": 4} {
		if actual, found := discNumberFromDirectory(name); !found || actual != expected {
			t.Fatalf("discNumberFromDirectory(%q) = %d, %v", name, actual, found)
		}
	}
	if _, found := discNumberFromDirectory("Teil 5"); found {
		t.Fatal("series-volume folders must not be treated as physical discs")
	}
}

func TestScanUsesSelectedBookFolderForDiscOnlyLayout(t *testing.T) {
	bookRoot := filepath.Join(t.TempDir(), "Autor - Buch")
	for _, disc := range []string{"CD1", "CD2"} {
		if err := os.MkdirAll(filepath.Join(bookRoot, disc), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bookRoot, disc, "01 - Kapitel.mp3"), []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := New(nil).Scan(context.Background(), bookRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Proposals) != 1 || result.Proposals[0].Metadata.Title != "Buch" || result.Proposals[0].Metadata.Author != "Autor" {
		t.Fatalf("selected disc-only folder was not used as evidence: %#v", result.Proposals)
	}
}

func TestScanKeepsProposalWhenEmbeddedMetadataFails(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "Autor - Buch")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01 - Kapitel.mp3", "02 - Kapitel.mp3"} {
		if err := os.WriteFile(filepath.Join(bookDir, name), []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := New(failingReader{}).Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Proposals) != 1 || len(result.Proposals[0].Files) != 2 {
		t.Fatalf("metadata failure removed scan results: %#v", result)
	}
	if result.Proposals[0].Files[1].MetadataNotice == "" {
		t.Fatal("expected per-file metadata notice")
	}
	if warnings := strings.Join(result.Proposals[0].Warnings, " "); !strings.Contains(warnings, "Metadaten konnten") {
		t.Fatalf("missing metadata warning: %q", warnings)
	}
}

func TestScanUsesBoundedMetadataWorkersAndCachesUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "Autor - Buch")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	firstFile := ""
	for index := 1; index <= 8; index++ {
		name := filepath.Join(bookDir, fmt.Sprintf("%02d - Kapitel.mp3", index))
		if firstFile == "" {
			firstFile = name
		}
		if err := os.WriteFile(name, []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	reader := &concurrentReader{}
	scanner := New(reader)
	if _, err := scanner.Scan(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	maximum := atomic.LoadInt32(&reader.maximum)
	if maximum < 2 || maximum > maxMetadataWorkers {
		t.Fatalf("metadata concurrency = %d, want 2..%d", maximum, maxMetadataWorkers)
	}
	if _, err := scanner.Scan(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if calls := atomic.LoadInt32(&reader.calls); calls != 8 {
		t.Fatalf("metadata reader calls = %d, want unchanged files to come from cache", calls)
	}
	if err := os.WriteFile(firstFile, []byte("changed audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Scan(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if calls := atomic.LoadInt32(&reader.calls); calls != 9 {
		t.Fatalf("metadata reader calls after one changed file = %d, want 9", calls)
	}
}

func TestScanAudioFilesReportsMissingFileWithoutAbortingOtherResults(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "01.mp3")
	missing := filepath.Join(root, "02.mp3")
	if err := os.WriteFile(existing, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanner := New(nil)
	results, err := scanner.scanAudioFiles(context.Background(), []string{"book"}, map[string][]string{"book": []string{existing, missing}})
	if err != nil {
		t.Fatal(err)
	}
	if results[0][0].Err != nil || results[0][1].Err == nil {
		t.Fatalf("unexpected partial file results: %#v", results[0])
	}
}
