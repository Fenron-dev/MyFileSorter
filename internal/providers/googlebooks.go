package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/dennis/myfilesorter/internal/domain"
)

type GoogleBooks struct {
	client  *http.Client
	baseURL string
}

func NewGoogleBooks(client *http.Client) *GoogleBooks {
	return &GoogleBooks{client: client, baseURL: "https://www.googleapis.com/books/v1/volumes"}
}

func (p *GoogleBooks) Name() string { return "google_books" }

func (p *GoogleBooks) Search(ctx context.Context, query domain.MetadataSearchQuery) ([]domain.MetadataCandidate, error) {
	search := strings.TrimSpace(query.Title)
	if query.ISBN != "" {
		search = "isbn:" + compactID(query.ISBN)
	} else if query.Author != "" {
		search += " inauthor:" + strings.TrimSpace(query.Author)
	}
	if strings.TrimSpace(search) == "" {
		return nil, fmt.Errorf("Google Books benötigt einen Titel oder eine ISBN")
	}
	params := url.Values{
		"q":          {search},
		"printType":  {"books"},
		"projection": {"lite"},
		"maxResults": {"10"},
	}
	if len(query.Language) == 2 {
		params.Set("langRestrict", strings.ToLower(query.Language))
	}
	var response googleVolumesResponse
	if err := getJSON(ctx, p.client, p.baseURL+"?"+params.Encode(), &response); err != nil {
		return nil, err
	}
	items := response.Items
	if len(items) > 10 {
		items = items[:10]
	}
	results := make([]domain.MetadataCandidate, 0, len(items))
	for _, item := range items {
		candidate := googleCandidate(item)
		if candidate.Title == "" {
			continue
		}
		candidate.Confidence = score(query, candidate)
		results = append(results, candidate)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Confidence > results[j].Confidence })
	return results, nil
}

type googleVolumesResponse struct {
	Items []googleVolume `json:"items"`
}

type googleVolume struct {
	ID         string           `json:"id"`
	VolumeInfo googleVolumeInfo `json:"volumeInfo"`
}

type googleVolumeInfo struct {
	Title               string             `json:"title"`
	Subtitle            string             `json:"subtitle"`
	Authors             []string           `json:"authors"`
	Publisher           string             `json:"publisher"`
	PublishedDate       string             `json:"publishedDate"`
	Description         string             `json:"description"`
	Language            string             `json:"language"`
	IndustryIdentifiers []googleIdentifier `json:"industryIdentifiers"`
	ImageLinks          struct {
		Thumbnail string `json:"thumbnail"`
	} `json:"imageLinks"`
}

type googleIdentifier struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}

func googleCandidate(item googleVolume) domain.MetadataCandidate {
	info := item.VolumeInfo
	candidate := domain.MetadataCandidate{
		ID:            "google_books:" + item.ID,
		Provider:      "google_books",
		Title:         info.Title,
		Subtitle:      info.Subtitle,
		Author:        strings.Join(info.Authors, " & "),
		Language:      info.Language,
		Publisher:     info.Publisher,
		PublishedYear: year(info.PublishedDate),
		Description:   info.Description,
		CoverURL:      secureURL(info.ImageLinks.Thumbnail),
	}
	for _, identifier := range info.IndustryIdentifiers {
		if strings.HasPrefix(identifier.Type, "ISBN_") {
			candidate.ISBN = identifier.Identifier
			if identifier.Type == "ISBN_13" {
				break
			}
		}
	}
	return candidate
}

func secureURL(value string) string {
	if strings.HasPrefix(value, "http://") {
		return "https://" + strings.TrimPrefix(value, "http://")
	}
	return value
}

func year(value string) string {
	if len(value) >= 4 {
		return value[:4]
	}
	return value
}
