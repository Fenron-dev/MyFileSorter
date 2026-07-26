package metadata

import "testing"

func TestParseFFProbe(t *testing.T) {
	input := []byte(`{
  "format": {
    "duration": "123.5",
    "tags": {
      "album": "Der Testtitel",
      "album_artist": "Erika Beispiel",
      "series": "Testreihe",
      "series-part": "2",
      "AUDIBLE_ASIN": "B012345678"
    }
  },
  "streams": [{"codec_type":"audio","tags":{"track":"3/8","composer":"Ein Sprecher"}}]
}`)

	got, err := parseFFProbe(input)
	if err != nil {
		t.Fatalf("parseFFProbe() error = %v", err)
	}
	if got.Album != "Der Testtitel" || got.AlbumArtist != "Erika Beispiel" {
		t.Fatalf("unexpected identity: %#v", got)
	}
	if got.Series != "Testreihe" || got.SeriesSequence != "2" || got.Track != 3 {
		t.Fatalf("unexpected ordering: %#v", got)
	}
	if got.ASIN != "B012345678" || got.Narrator != "Ein Sprecher" {
		t.Fatalf("unexpected identifiers: %#v", got)
	}
	if got.DurationMillis != 123500 {
		t.Fatalf("duration = %d", got.DurationMillis)
	}
}
