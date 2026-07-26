package domain

import "time"

type ProposalStatus string

const (
	StatusReviewRequired ProposalStatus = "review_required"
	StatusConfirmed      ProposalStatus = "confirmed"
	StatusExcluded       ProposalStatus = "excluded"
	StatusConflict       ProposalStatus = "conflict"
	StatusError          ProposalStatus = "error"
)

type Evidence struct {
	Value      string  `json:"value"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
}

type BookMetadata struct {
	Title          string              `json:"title"`
	Author         string              `json:"author"`
	Series         string              `json:"series,omitempty"`
	SeriesSequence string              `json:"seriesSequence,omitempty"`
	Narrator       string              `json:"narrator,omitempty"`
	Language       string              `json:"language,omitempty"`
	ASIN           string              `json:"asin,omitempty"`
	ISBN           string              `json:"isbn,omitempty"`
	Evidence       map[string]Evidence `json:"evidence"`
}

type EmbeddedMetadata struct {
	Title          string `json:"title,omitempty"`
	Album          string `json:"album,omitempty"`
	Artist         string `json:"artist,omitempty"`
	AlbumArtist    string `json:"albumArtist,omitempty"`
	Series         string `json:"series,omitempty"`
	SeriesSequence string `json:"seriesSequence,omitempty"`
	Narrator       string `json:"narrator,omitempty"`
	Language       string `json:"language,omitempty"`
	ASIN           string `json:"asin,omitempty"`
	ISBN           string `json:"isbn,omitempty"`
	Track          int    `json:"track,omitempty"`
	Disc           int    `json:"disc,omitempty"`
	DurationMillis int64  `json:"durationMillis,omitempty"`
}

type AudioFile struct {
	Path           string           `json:"path"`
	Name           string           `json:"name"`
	Extension      string           `json:"extension"`
	Size           int64            `json:"size"`
	Track          int              `json:"track"`
	Disc           int              `json:"disc"`
	Metadata       EmbeddedMetadata `json:"metadata"`
	MetadataNotice string           `json:"metadataNotice,omitempty"`
}

type BookProposal struct {
	ID         string         `json:"id"`
	SourceRoot string         `json:"sourceRoot"`
	GroupPath  string         `json:"groupPath"`
	Metadata   BookMetadata   `json:"metadata"`
	Files      []AudioFile    `json:"files"`
	Status     ProposalStatus `json:"status"`
	Confidence float64        `json:"confidence"`
	Warnings   []string       `json:"warnings"`
}

type ScanSummary struct {
	Books             int   `json:"books"`
	Files             int   `json:"files"`
	Bytes             int64 `json:"bytes"`
	MetadataAvailable bool  `json:"metadataAvailable"`
}

type ScanResult struct {
	Source      string         `json:"source"`
	ScannedAt   time.Time      `json:"scannedAt"`
	Proposals   []BookProposal `json:"proposals"`
	Summary     ScanSummary    `json:"summary"`
	GlobalNotes []string       `json:"globalNotes"`
}

type PlannedOperation struct {
	ProposalID string `json:"proposalId"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Size       int64  `json:"size"`
}

type OperationPlan struct {
	TargetRoot string             `json:"targetRoot"`
	CreatedAt  time.Time          `json:"createdAt"`
	Operations []PlannedOperation `json:"operations"`
	TotalBytes int64              `json:"totalBytes"`
	Warnings   []string           `json:"warnings"`
	Executable bool               `json:"executable"`
}

type MetadataSearchQuery struct {
	Title    string `json:"title"`
	Author   string `json:"author,omitempty"`
	Narrator string `json:"narrator,omitempty"`
	ASIN     string `json:"asin,omitempty"`
	ISBN     string `json:"isbn,omitempty"`
	Language string `json:"language,omitempty"`
	Region   string `json:"region,omitempty"`
}

type MetadataCandidate struct {
	ID              string  `json:"id"`
	Provider        string  `json:"provider"`
	Title           string  `json:"title"`
	Subtitle        string  `json:"subtitle,omitempty"`
	Author          string  `json:"author,omitempty"`
	Series          string  `json:"series,omitempty"`
	SeriesSequence  string  `json:"seriesSequence,omitempty"`
	Narrator        string  `json:"narrator,omitempty"`
	Language        string  `json:"language,omitempty"`
	ASIN            string  `json:"asin,omitempty"`
	ISBN            string  `json:"isbn,omitempty"`
	Publisher       string  `json:"publisher,omitempty"`
	PublishedYear   string  `json:"publishedYear,omitempty"`
	Description     string  `json:"description,omitempty"`
	CoverURL        string  `json:"coverUrl,omitempty"`
	DurationMinutes int     `json:"durationMinutes,omitempty"`
	Confidence      float64 `json:"confidence"`
}
