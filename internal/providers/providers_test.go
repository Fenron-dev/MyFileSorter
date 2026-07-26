package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dennis/myfilesorter/internal/domain"
)

func TestScorePrefersExactIdentifiers(t *testing.T) {
	query := domain.MetadataSearchQuery{Title: "Ähnlicher Titel", Author: "Autor", ASIN: "B012345678"}
	candidate := domain.MetadataCandidate{Title: "Anderer Titel", Author: "Andere Person", ASIN: "b012345678"}
	if got := score(query, candidate); got != 1 {
		t.Fatalf("score() = %f, want 1", got)
	}
}

func TestGoogleBooksSearchMapsAndScoresResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("q") == "" {
			t.Error("missing search query")
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "items": [{
    "id": "volume-1",
    "volumeInfo": {
      "title": "Der Testtitel",
      "authors": ["Erika Beispiel"],
      "language": "de",
      "industryIdentifiers": [{"type":"ISBN_13","identifier":"9781234567890"}],
      "imageLinks": {"thumbnail":"http://example.test/cover.jpg"}
    }
  }]
}`))
	}))
	defer server.Close()
	provider := NewGoogleBooks(server.Client())
	provider.baseURL = server.URL

	results, err := provider.Search(context.Background(), domain.MetadataSearchQuery{Title: "Der Testtitel", Author: "Erika Beispiel"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %#v", results)
	}
	if results[0].ISBN != "9781234567890" || results[0].CoverURL != "https://example.test/cover.jpg" {
		t.Fatalf("unexpected result: %#v", results[0])
	}
	if results[0].Confidence < .9 {
		t.Fatalf("confidence = %f", results[0].Confidence)
	}
}

func TestAudibleCandidate(t *testing.T) {
	item := audibleBook{
		Title: "Ein Hörbuch", ASIN: "B012345678", Language: "german", RuntimeLengthMin: 600,
		Authors: []struct {
			Name string `json:"name"`
		}{{Name: "Eine Autorin"}},
		Narrators: []struct {
			Name string `json:"name"`
		}{{Name: "Ein Sprecher"}},
		SeriesPrimary: &struct {
			Name     string `json:"name"`
			Position string `json:"position"`
		}{Name: "Eine Reihe", Position: "Book 2, Dramatized"},
	}
	got := audibleCandidate(item)
	if got.Author != "Eine Autorin" || got.Narrator != "Ein Sprecher" {
		t.Fatalf("unexpected people: %#v", got)
	}
	if got.Series != "Eine Reihe" || got.SeriesSequence != "2" {
		t.Fatalf("unexpected series: %#v", got)
	}
}

func TestValidASIN(t *testing.T) {
	if !validASIN("B012345678") || validASIN("not-an-asin") {
		t.Fatal("ASIN validation failed")
	}
}

func TestCleanSequence(t *testing.T) {
	for input, want := range map[string]string{"Book 2, Dramatized": "2", "Volume 1.5": "1.5", ".5": ".5"} {
		if got := cleanSequence(input); got != want {
			t.Errorf("cleanSequence(%q) = %q, want %q", input, got, want)
		}
	}
}
