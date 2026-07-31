package domain

import "time"

type ProposalStatus string

const (
	StatusReviewRequired ProposalStatus = "review_required"
	StatusConfirmed      ProposalStatus = "confirmed"
	StatusExcluded       ProposalStatus = "excluded"
	StatusImported       ProposalStatus = "imported"
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
	EditionInfo    string              `json:"editionInfo,omitempty"`
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
	TargetTitle    string           `json:"targetTitle,omitempty"`
	Excluded       bool             `json:"excluded,omitempty"`
	Metadata       EmbeddedMetadata `json:"metadata"`
	MetadataNotice string           `json:"metadataNotice,omitempty"`
}

type CompanionKind string

const (
	CompanionEbook   CompanionKind = "ebook"
	CompanionDiscard CompanionKind = "discard"
	CompanionUnknown CompanionKind = "unknown"
)

type TrackUpdate struct {
	Path        string `json:"path"`
	TargetTitle string `json:"targetTitle,omitempty"`
	Track       int    `json:"track"`
	Disc        int    `json:"disc"`
	Excluded    bool   `json:"excluded"`
}

type CompanionFile struct {
	Path      string        `json:"path"`
	Name      string        `json:"name"`
	Extension string        `json:"extension"`
	Size      int64         `json:"size"`
	Kind      CompanionKind `json:"kind"`
}

type BookProposal struct {
	ID                 string          `json:"id"`
	SourceRoot         string          `json:"sourceRoot"`
	GroupPath          string          `json:"groupPath"`
	Metadata           BookMetadata    `json:"metadata"`
	Files              []AudioFile     `json:"files"`
	Companions         []CompanionFile `json:"companions,omitempty"`
	Status             ProposalStatus  `json:"status"`
	ExecutionJournalID string          `json:"executionJournalId,omitempty"`
	Confidence         float64         `json:"confidence"`
	Warnings           []string        `json:"warnings"`
}

type ScanSummary struct {
	Books             int   `json:"books"`
	Files             int   `json:"files"`
	Ebooks            int   `json:"ebooks"`
	Sidecars          int   `json:"sidecars"`
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

type ScanProgress struct {
	Phase       string `json:"phase"`
	Discovered  int    `json:"discovered"`
	Inspected   int    `json:"inspected"`
	CurrentPath string `json:"currentPath,omitempty"`
}

type PlannedOperation struct {
	ProposalID string `json:"proposalId"`
	Action     string `json:"action"`
	Category   string `json:"category"`
	SourceRoot string `json:"sourceRoot,omitempty"`
	Source     string `json:"source"`
	SourceModifiedNanos int64 `json:"sourceModifiedNanos,omitempty"`
	Target     string `json:"target"`
	Size       int64  `json:"size"`
}

type PlanOptions struct {
	MoveEbooks      bool `json:"moveEbooks"`
	CleanupSidecars bool `json:"cleanupSidecars"`
	// Number widths use -1 for automatic detection, 1 for no padding and
	// values >= 2 for a fixed minimum width. Zero keeps the legacy default.
	BookNumberWidth  int    `json:"bookNumberWidth"`
	TrackNumberWidth int    `json:"trackNumberWidth"`
	AudioFileNaming  string `json:"audioFileNaming"`
	// ExistingFilePolicy is empty/"error" for fail-closed planning or
	// "skip_identical" to quarantine a source only after a verified SHA-256
	// match with the existing target.
	ExistingFilePolicy string `json:"existingFilePolicy,omitempty"`
}

type OperationPlan struct {
	PlanID     string             `json:"planId"`
	Revision   uint64             `json:"revision"`
	Digest     string             `json:"digest"`
	TargetRoot string             `json:"targetRoot"`
	CreatedAt  time.Time          `json:"createdAt"`
	ExpiresAt  time.Time          `json:"expiresAt"`
	Operations []PlannedOperation `json:"operations"`
	TotalBytes int64              `json:"totalBytes"`
	Warnings   []string           `json:"warnings"`
	Executable bool               `json:"executable"`
}

type ProposalExecutionResult struct {
	ProposalID string `json:"proposalId"`
	Status     string `json:"status"`
	Completed  int    `json:"completed"`
	Total      int    `json:"total"`
	Error      string `json:"error,omitempty"`
}

type ExecutionResult struct {
	JournalID       string                    `json:"journalId"`
	Status          string                    `json:"status"`
	Completed       int                       `json:"completed"`
	Total           int                       `json:"total"`
	TotalBytes      int64                     `json:"totalBytes"`
	Error           string                    `json:"error,omitempty"`
	Warnings        []string                  `json:"warnings,omitempty"`
	ProposalIDs     []string                  `json:"proposalIds,omitempty"`
	ProposalResults []ProposalExecutionResult `json:"proposalResults,omitempty"`
}

type ActiveJob struct {
	ID        string    `json:"id,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	StartedAt time.Time `json:"startedAt,omitempty"`
	Active    bool      `json:"active"`
}

type ExecutionProgress struct {
	Status         string `json:"status"`
	Completed      int    `json:"completed"`
	Total          int    `json:"total"`
	CompletedBytes int64  `json:"completedBytes"`
	TotalBytes     int64  `json:"totalBytes"`
	CurrentSource  string `json:"currentSource,omitempty"`
	CurrentTarget  string `json:"currentTarget,omitempty"`
}

type ImportRun struct {
	JournalID  string    `json:"journalId"`
	Status     string    `json:"status"`
	TargetRoot string    `json:"targetRoot"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Completed  int       `json:"completed"`
	Total      int       `json:"total"`
	TotalBytes int64     `json:"totalBytes"`
	Error      string    `json:"error,omitempty"`
	CanUndo    bool      `json:"canUndo"`
}

type LogEntry struct {
	Timestamp time.Time         `json:"timestamp"`
	Level     string            `json:"level"`
	Component string            `json:"component"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
}

type LogSnapshot struct {
	SessionID string     `json:"sessionId"`
	FilePath  string     `json:"filePath,omitempty"`
	Warning   string     `json:"warning,omitempty"`
	Entries   []LogEntry `json:"entries"`
}

type LogSessionInfo struct {
	SessionID  string    `json:"sessionId"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Size       int64     `json:"size"`
	Current    bool      `json:"current"`
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

type AIProfile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	BaseURL   string `json:"baseUrl"`
	Model     string `json:"model"`
	HasAPIKey bool   `json:"hasApiKey"`
	IsDefault bool   `json:"isDefault"`
}

type AIProfileInput struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	BaseURL     string `json:"baseUrl"`
	Model       string `json:"model"`
	APIKey      string `json:"apiKey,omitempty"`
	ClearAPIKey bool   `json:"clearApiKey,omitempty"`
	IsDefault   bool   `json:"isDefault"`
}

type AISuggestion struct {
	ProposalID            string  `json:"proposalId"`
	ProfileID             string  `json:"profileId"`
	Title                 string  `json:"title"`
	Author                string  `json:"author"`
	Series                string  `json:"series,omitempty"`
	SeriesSequence        string  `json:"seriesSequence,omitempty"`
	EditionInfo           string  `json:"editionInfo,omitempty"`
	Narrator              string  `json:"narrator,omitempty"`
	Language              string  `json:"language,omitempty"`
	SuggestedSearchTitle  string  `json:"suggestedSearchTitle,omitempty"`
	SuggestedSearchAuthor string  `json:"suggestedSearchAuthor,omitempty"`
	Confidence            float64 `json:"confidence"`
	Reasoning             string  `json:"reasoning,omitempty"`
}
