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
