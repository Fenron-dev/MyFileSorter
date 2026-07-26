package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

const maxResponseBytes = 4 << 20

type Provider interface {
	Name() string
	Search(context.Context, domain.MetadataSearchQuery) ([]domain.MetadataCandidate, error)
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry(client *http.Client) *Registry {
	if client == nil {
		client = &http.Client{
			Timeout: 12 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("zu viele Weiterleitungen")
				}
				if request.URL.Scheme != "https" || !allowedProviderHost(request.URL.Hostname()) {
					return fmt.Errorf("unsichere Anbieterweiterleitung blockiert")
				}
				return nil
			},
		}
	}
	items := []Provider{
		NewGoogleBooks(client),
		NewAudible(client),
	}
	registry := &Registry{providers: make(map[string]Provider, len(items))}
	for _, item := range items {
		registry.providers[item.Name()] = item
	}
	return registry
}

func allowedProviderHost(host string) bool {
	host = strings.ToLower(host)
	if host == "www.googleapis.com" || host == "api.audnex.us" {
		return true
	}
	for _, allowed := range []string{
		"api.audible.com", "api.audible.ca", "api.audible.co.uk", "api.audible.com.au", "api.audible.fr",
		"api.audible.de", "api.audible.co.jp", "api.audible.it", "api.audible.in", "api.audible.es",
	} {
		if host == allowed {
			return true
		}
	}
	return false
}

func (r *Registry) Search(ctx context.Context, provider string, query domain.MetadataSearchQuery) ([]domain.MetadataCandidate, error) {
	item, ok := r.providers[strings.ToLower(strings.TrimSpace(provider))]
	if !ok {
		return nil, fmt.Errorf("unbekannter Metadatenanbieter %q", provider)
	}
	if strings.TrimSpace(query.Title) == "" && strings.TrimSpace(query.ASIN) == "" && strings.TrimSpace(query.ISBN) == "" {
		return nil, fmt.Errorf("Titel, ASIN oder ISBN ist für die Suche erforderlich")
	}
	return item.Search(ctx, query)
}

func getJSON(ctx context.Context, client *http.Client, url string, target interface{}) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "MyFileSorter/0.1")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Metadatenanbieter antwortete mit HTTP %d", response.StatusCode)
	}
	reader := io.LimitReader(response.Body, maxResponseBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("Antwort des Metadatenanbieters ist zu groß")
	}
	if err := jsonUnmarshal(data, target); err != nil {
		return fmt.Errorf("ungültige Anbieterantwort: %w", err)
	}
	return nil
}
