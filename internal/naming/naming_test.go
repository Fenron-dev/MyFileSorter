package naming

import (
	"path/filepath"
	"testing"

	"github.com/dennis/myfilesorter/internal/domain"
)

func TestBookDirectory(t *testing.T) {
	got, err := BookDirectory(domain.BookMetadata{
		Author: "Patrick Rothfuss", Title: "Der Name des Windes", Series: "Königsmörder-Chronik", SeriesSequence: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("Patrick Rothfuss", "Königsmörder-Chronik", "01 - Der Name des Windes")
	if got != want {
		t.Fatalf("BookDirectory() = %q, want %q", got, want)
	}
}

func TestBookDirectoryStandalone(t *testing.T) {
	got, err := BookDirectory(domain.BookMetadata{Author: "Autor", Title: "Buch"})
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("Autor", "Buch") {
		t.Fatalf("BookDirectory() = %q", got)
	}
}

func TestSegmentIsPortable(t *testing.T) {
	if got := Segment(`CON`); got != `_CON` {
		t.Fatalf("Segment(CON) = %q", got)
	}
	if got := Segment(`Titel: Teil/1?`); got != `Titel- Teil-1-` {
		t.Fatalf("Segment(invalid) = %q", got)
	}
}

func TestSequence(t *testing.T) {
	for input, want := range map[string]string{"1": "01", "2,5": "02.5", "12": "12"} {
		if got := Sequence(input); got != want {
			t.Errorf("Sequence(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSequenceWithSelectableWidth(t *testing.T) {
	for width, want := range map[int]string{1: "1", 2: "01", 3: "001", 4: "0001"} {
		if got := SequenceWithWidth("1", width); got != want {
			t.Errorf("SequenceWithWidth(1, %d) = %q, want %q", width, got, want)
		}
	}
}

func TestSourceTrackNameKeepsTitleAndNormalisesSeparators(t *testing.T) {
	file := domain.AudioFile{Name: "1.Opening_Credits.mp3", Extension: ".mp3", Track: 1}
	if got := SourceTrackName(file, 1, 12, -1); got != "01 - Opening Credits.mp3" {
		t.Fatalf("SourceTrackName() = %q", got)
	}
}
