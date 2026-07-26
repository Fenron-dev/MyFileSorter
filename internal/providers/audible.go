package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/dennis/myfilesorter/internal/domain"
)

var sequenceNumber = regexp.MustCompile(`\.\d+|\d+(?:\.\d+)?`)
var localizedEditionSuffix = regexp.MustCompile(`(?i)\s*[\[(](?:german|english|french|italian|spanish|japanese) edition[\])]\s*$`)

type Audible struct {
	client       *http.Client
	catalogHosts map[string]string
	detailURL    string
}

func NewAudible(client *http.Client) *Audible {
	return &Audible{
		client: client,
		catalogHosts: map[string]string{
			"us": "https://api.audible.com",
			"ca": "https://api.audible.ca",
			"uk": "https://api.audible.co.uk",
			"au": "https://api.audible.com.au",
			"fr": "https://api.audible.fr",
			"de": "https://api.audible.de",
			"jp": "https://api.audible.co.jp",
			"it": "https://api.audible.it",
			"in": "https://api.audible.in",
			"es": "https://api.audible.es",
		},
		detailURL: "https://api.audnex.us/books/",
	}
}

func (p *Audible) Name() string { return "audible" }

func (p *Audible) Search(ctx context.Context, query domain.MetadataSearchQuery) ([]domain.MetadataCandidate, error) {
	region := strings.ToLower(strings.TrimSpace(query.Region))
	if region == "" {
		region = "de"
	}
	host, ok := p.catalogHosts[region]
	if !ok {
		return nil, fmt.Errorf("nicht unterstützte Audible-Region %q", region)
	}
	if asin := compactID(query.ASIN); validASIN(asin) {
		candidate, err := p.fetchDetail(ctx, asin, region)
		if err != nil {
			return nil, err
		}
		if product, catalogErr := p.fetchCatalogProduct(ctx, host, asin); catalogErr == nil {
			applyCatalogSeries(&candidate, product)
		}
		candidate.Confidence = score(query, candidate)
		return []domain.MetadataCandidate{candidate}, nil
	}
	if strings.TrimSpace(query.Title) == "" {
		return nil, fmt.Errorf("Audible benötigt einen Titel oder eine gültige ASIN")
	}

	params := url.Values{
		"num_results":      {"8"},
		"products_sort_by": {"Relevance"},
		"response_groups":  {"series"},
		"title":            {query.Title},
	}
	if query.Author != "" {
		params.Set("author", query.Author)
	}
	var catalog audibleCatalogResponse
	endpoint := host + "/1.0/catalog/products?" + params.Encode()
	if err := getJSON(ctx, p.client, endpoint, &catalog); err != nil {
		return nil, err
	}

	products := catalog.Products
	if len(products) > 8 {
		products = products[:8]
	}
	results := make([]domain.MetadataCandidate, len(products))
	errorsByIndex := make([]error, len(products))
	var wait sync.WaitGroup
	for index, product := range products {
		asin := strings.ToUpper(strings.TrimSpace(product.ASIN))
		if !validASIN(asin) {
			continue
		}
		wait.Add(1)
		go func(index int, asin string, product audibleCatalogProduct) {
			defer wait.Done()
			results[index], errorsByIndex[index] = p.fetchDetail(ctx, asin, region)
			if errorsByIndex[index] == nil {
				applyCatalogSeries(&results[index], product)
			}
		}(index, asin, product)
	}
	wait.Wait()

	filtered := results[:0]
	for index, candidate := range results {
		if errorsByIndex[index] != nil || candidate.Title == "" {
			continue
		}
		candidate.Confidence = score(query, candidate)
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 && len(products) > 0 {
		return nil, fmt.Errorf("Audible-Treffer konnten nicht mit Detaildaten angereichert werden")
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Confidence > filtered[j].Confidence })
	return filtered, nil
}

func (p *Audible) fetchDetail(ctx context.Context, asin, region string) (domain.MetadataCandidate, error) {
	var item audibleBook
	endpoint := p.detailURL + url.PathEscape(strings.ToUpper(asin)) + "?region=" + url.QueryEscape(region)
	if err := getJSON(ctx, p.client, endpoint, &item); err != nil {
		return domain.MetadataCandidate{}, err
	}
	return audibleCandidate(item), nil
}

type audibleCatalogResponse struct {
	Products []audibleCatalogProduct `json:"products"`
}

type audibleCatalogProductResponse struct {
	Product audibleCatalogProduct `json:"product"`
}

type audibleCatalogProduct struct {
	ASIN   string `json:"asin"`
	Series []struct {
		Title    string `json:"title"`
		Sequence string `json:"sequence"`
	} `json:"series"`
}

type audibleBook struct {
	Title            string `json:"title"`
	Subtitle         string `json:"subtitle"`
	ASIN             string `json:"asin"`
	PublisherName    string `json:"publisherName"`
	Summary          string `json:"summary"`
	ReleaseDate      string `json:"releaseDate"`
	Image            string `json:"image"`
	Language         string `json:"language"`
	RuntimeLengthMin int    `json:"runtimeLengthMin"`
	ISBN             string `json:"isbn"`
	Authors          []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Narrators []struct {
		Name string `json:"name"`
	} `json:"narrators"`
	SeriesPrimary *struct {
		Name     string `json:"name"`
		Position string `json:"position"`
	} `json:"seriesPrimary"`
}

func audibleCandidate(item audibleBook) domain.MetadataCandidate {
	authors := make([]string, 0, len(item.Authors))
	for _, author := range item.Authors {
		authors = append(authors, author.Name)
	}
	narrators := make([]string, 0, len(item.Narrators))
	for _, narrator := range item.Narrators {
		narrators = append(narrators, narrator.Name)
	}
	candidate := domain.MetadataCandidate{
		ID:              "audible:" + item.ASIN,
		Provider:        "audible",
		Title:           item.Title,
		Subtitle:        item.Subtitle,
		Author:          strings.Join(authors, " & "),
		Narrator:        strings.Join(narrators, " & "),
		Language:        item.Language,
		ASIN:            item.ASIN,
		ISBN:            item.ISBN,
		Publisher:       item.PublisherName,
		PublishedYear:   year(item.ReleaseDate),
		Description:     item.Summary,
		CoverURL:        secureURL(item.Image),
		DurationMinutes: item.RuntimeLengthMin,
	}
	if item.SeriesPrimary != nil {
		candidate.Series = cleanSeriesName(item.SeriesPrimary.Name)
		candidate.SeriesSequence = cleanSequence(item.SeriesPrimary.Position)
	}
	return candidate
}

func (p *Audible) fetchCatalogProduct(ctx context.Context, host, asin string) (audibleCatalogProduct, error) {
	var response audibleCatalogProductResponse
	endpoint := host + "/1.0/catalog/products/" + url.PathEscape(strings.ToUpper(asin)) + "?response_groups=series"
	if err := getJSON(ctx, p.client, endpoint, &response); err != nil {
		return audibleCatalogProduct{}, err
	}
	return response.Product, nil
}

func applyCatalogSeries(candidate *domain.MetadataCandidate, product audibleCatalogProduct) {
	if len(product.Series) == 0 {
		return
	}
	primary := product.Series[0]
	if strings.TrimSpace(candidate.Series) == "" {
		candidate.Series = cleanSeriesName(primary.Title)
	}
	if strings.TrimSpace(candidate.SeriesSequence) == "" {
		candidate.SeriesSequence = cleanSequence(primary.Sequence)
	}
}

func cleanSeriesName(value string) string {
	return strings.TrimSpace(localizedEditionSuffix.ReplaceAllString(value, ""))
}

func validASIN(value string) bool {
	if len(value) != 10 {
		return false
	}
	for _, character := range value {
		if character < '0' || (character > '9' && character < 'A') || character > 'Z' {
			return false
		}
	}
	return true
}

func cleanSequence(value string) string {
	if match := sequenceNumber.FindString(strings.TrimSpace(value)); match != "" {
		return match
	}
	return strings.TrimSpace(value)
}
