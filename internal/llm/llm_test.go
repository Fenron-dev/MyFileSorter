package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dennis/myfilesorter/internal/domain"
)

func TestStoreEncryptsAPIKeyAndReturnsOnlyKeyPresence(t *testing.T) {
	directory := t.TempDir()
	store := NewStore(directory)
	profile, err := store.Save(domain.AIProfileInput{
		Name: "Lokal", Provider: "ollama", Model: "qwen3:8b", APIKey: "super-secret", IsDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !profile.HasAPIKey {
		t.Fatal("expected API key presence")
	}
	data, err := os.ReadFile(filepath.Join(directory, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "super-secret") {
		t.Fatal("profile file contains plaintext API key")
	}
	profiles, err := store.List()
	if err != nil || len(profiles) != 1 || profiles[0].ID == "" || !profiles[0].IsDefault {
		t.Fatalf("unexpected profiles: %#v, %v", profiles, err)
	}
	_, secret, err := store.profileWithSecret(profile.ID)
	if err != nil || secret != "super-secret" {
		t.Fatalf("secret roundtrip failed: %q, %v", secret, err)
	}
}

func TestOpenAICompatibleAnalysisSendsOnlyTextEvidence(t *testing.T) {
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(payload)
		requestBody = string(encoded)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"Die Nullform\",\"author\":\"Dem Mikhailov\",\"series\":\"Die Nullform\",\"seriesSequence\":\"1\",\"editionInfo\":\"Ungekürzt\",\"confidence\":0.88,\"reasoning\":\"Ordnername\"}"}}]}`))
	}))
	defer server.Close()

	service := NewWithClient(t.TempDir(), server.Client())
	profile, err := service.SaveProfile(domain.AIProfileInput{
		Name: "Test", Provider: "openai_compatible", BaseURL: server.URL + "/v1", Model: "local-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal := domain.BookProposal{
		ID: "book", SourceRoot: "/secret/library", GroupPath: "/secret/library/Die.Nullform.1",
		Metadata: domain.BookMetadata{Title: "Die Nullform 1", Author: "Dem Mikhailov", Evidence: map[string]domain.Evidence{"title": {Value: "Die Nullform 1"}}},
		Files:    []domain.AudioFile{{Path: "/secret/library/Die.Nullform.1/01_Kapitel.mp3", Name: "01_Kapitel.mp3"}},
	}
	suggestion, err := service.Analyze(context.Background(), profile.ID, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if suggestion.Title != "Die Nullform" || suggestion.SeriesSequence != "1" || suggestion.EditionInfo != "Ungekürzt" {
		t.Fatalf("unexpected suggestion: %#v", suggestion)
	}
	if strings.Contains(requestBody, "/secret/library") || !strings.Contains(requestBody, "01_Kapitel.mp3") {
		t.Fatalf("unexpected AI payload: %s", requestBody)
	}
}

func TestParseSuggestionRejectsIncompleteMetadata(t *testing.T) {
	if _, err := parseSuggestion(`{"title":"Nur Titel"}`); err == nil {
		t.Fatal("expected incomplete suggestion error")
	}
}
