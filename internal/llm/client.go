package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

const maxAIResponseBytes = 2 << 20

type Service struct {
	store  *Store
	client *http.Client
}

func New(directory string) *Service {
	return NewWithClient(directory, nil)
}

func NewWithClient(directory string, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{
			Timeout: 90 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("zu viele AI-Weiterleitungen")
				}
				if len(via) > 0 && (request.URL.Scheme != via[0].URL.Scheme || request.URL.Host != via[0].URL.Host) {
					return fmt.Errorf("AI-Weiterleitung zu einem anderen Host blockiert")
				}
				return nil
			},
		}
	}
	return &Service{store: NewStore(directory), client: client}
}

func (s *Service) Profiles() ([]domain.AIProfile, error) {
	return s.store.List()
}

func (s *Service) SaveProfile(input domain.AIProfileInput) (domain.AIProfile, error) {
	return s.store.Save(input)
}

func (s *Service) DeleteProfile(id string) error {
	return s.store.Delete(id)
}

func (s *Service) TestProfile(ctx context.Context, id string) error {
	profile, secret, err := s.store.profileWithSecret(id)
	if err != nil {
		return err
	}
	endpoint := profile.BaseURL + "/models"
	if profile.Provider == "ollama" {
		endpoint = profile.BaseURL + "/api/tags"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	setHeaders(request, secret)
	_, err = s.do(request)
	return err
}

func (s *Service) Analyze(ctx context.Context, profileID string, proposal domain.BookProposal) (domain.AISuggestion, error) {
	profile, secret, err := s.store.profileWithSecret(profileID)
	if err != nil {
		return domain.AISuggestion{}, err
	}
	prompt, err := analysisPrompt(proposal)
	if err != nil {
		return domain.AISuggestion{}, err
	}
	content, err := s.chat(ctx, profile, secret, prompt)
	if err != nil {
		return domain.AISuggestion{}, err
	}
	suggestion, err := parseSuggestion(content)
	if err != nil {
		return domain.AISuggestion{}, err
	}
	suggestion.ProposalID = proposal.ID
	suggestion.ProfileID = profile.ID
	return suggestion, nil
}

func (s *Service) chat(ctx context.Context, profile domain.AIProfile, secret, prompt string) (string, error) {
	endpoint := profile.BaseURL + "/chat/completions"
	payload := map[string]interface{}{
		"model": profile.Model, "temperature": 0.1,
		"messages": []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": prompt}},
	}
	if profile.Provider == "ollama" {
		endpoint = profile.BaseURL + "/api/chat"
		delete(payload, "temperature")
		payload["stream"] = false
		payload["format"] = "json"
		payload["options"] = map[string]interface{}{"temperature": 0.1}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	setHeaders(request, secret)
	response, err := s.do(request)
	if err != nil {
		return "", err
	}
	if profile.Provider == "ollama" {
		var envelope struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(response, &envelope); err != nil || strings.TrimSpace(envelope.Message.Content) == "" {
			return "", fmt.Errorf("Ollama lieferte keine auswertbare Antwort")
		}
		return envelope.Message.Content, nil
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || len(envelope.Choices) == 0 || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("AI-Anbieter lieferte keine auswertbare Antwort")
	}
	return envelope.Choices[0].Message.Content, nil
}

func (s *Service) do(request *http.Request) ([]byte, error) {
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, maxAIResponseBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(data) > maxAIResponseBytes {
		return nil, fmt.Errorf("AI-Antwort ist zu groß")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("AI-Anbieter antwortete mit HTTP %d", response.StatusCode)
	}
	return data, nil
}

func setHeaders(request *http.Request, secret string) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "MyFileSorter/0.1")
	if request.Method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
}

func validateBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("AI-Endpoint muss eine vollständige HTTP- oder HTTPS-Adresse sein")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("AI-Endpoint darf keine Zugangsdaten, Query oder Fragmente enthalten")
	}
	return nil
}

const systemPrompt = `Du erkennst Hörbuch-Metadaten ausschließlich aus den gelieferten lokalen Textinformationen. Erfinde keine Angaben. Leere oder unsichere Felder bleiben leer. Antworte ausschließlich als einzelnes JSON-Objekt mit title, author, series, seriesSequence, editionInfo, narrator, language, suggestedSearchTitle, suggestedSearchAuthor, confidence (0 bis 1) und reasoning. Editionshinweise wie Ungekürzt gehören nur in editionInfo, nicht in title. Eine Nummer nach einem Serientitel ist normalerweise die Bandnummer.`

func analysisPrompt(proposal domain.BookProposal) (string, error) {
	fileNames := make([]string, 0, len(proposal.Files))
	for index, file := range proposal.Files {
		if index >= 200 {
			fileNames = append(fileNames, fmt.Sprintf("… %d weitere Dateien", len(proposal.Files)-index))
			break
		}
		fileNames = append(fileNames, truncate(file.Name, 300))
	}
	input := struct {
		FolderName    string              `json:"folderName"`
		Current       domain.BookMetadata `json:"currentSuggestion"`
		FileNames     []string            `json:"fileNames"`
		EmbeddedFirst domain.EmbeddedMetadata `json:"embeddedMetadataFirstFile,omitempty"`
	}{FolderName: filepath.Base(proposal.GroupPath), Current: proposal.Metadata, FileNames: fileNames}
	input.Current.Evidence = nil
	if len(proposal.Files) > 0 {
		input.EmbeddedFirst = proposal.Files[0].Metadata
	}
	data, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	return "Analysiere diesen Hörbuchvorschlag:\n" + string(data), nil
}

func parseSuggestion(content string) (domain.AISuggestion, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return domain.AISuggestion{}, fmt.Errorf("AI-Antwort enthält kein JSON-Objekt")
	}
	var suggestion domain.AISuggestion
	if err := json.Unmarshal([]byte(content[start:end+1]), &suggestion); err != nil {
		return domain.AISuggestion{}, fmt.Errorf("AI-Antwort enthält ungültiges JSON: %w", err)
	}
	suggestion.Title = strings.TrimSpace(suggestion.Title)
	suggestion.Author = strings.TrimSpace(suggestion.Author)
	if suggestion.Title == "" || suggestion.Author == "" {
		return domain.AISuggestion{}, fmt.Errorf("AI-Vorschlag enthält keinen vollständigen Titel und Autor")
	}
	if suggestion.Confidence < 0 {
		suggestion.Confidence = 0
	} else if suggestion.Confidence > 1 {
		suggestion.Confidence = 1
	}
	return suggestion, nil
}

func truncate(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}
