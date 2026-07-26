package scanner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
	"github.com/dennis/myfilesorter/internal/metadata"
)

var (
	errNotDirectory = errors.New("source is not a directory")
	trackPrefix     = regexp.MustCompile(`^\s*(?:cd|disc|disk|track|part|teil)?\s*\d{1,4}(?:[._ -]+)`)
	seriesPrefix    = regexp.MustCompile(`^\s*(\d+(?:[.,]\d+)?)\s*[-._]\s*(.+)$`)
)

var audioExtensions = map[string]struct{}{
	".m4b": {}, ".m4a": {}, ".mp3": {}, ".aac": {}, ".flac": {}, ".ogg": {}, ".opus": {}, ".wav": {},
}

type Scanner struct {
	metadata metadata.Reader
}

func New(reader metadata.Reader) *Scanner {
	if reader == nil {
		reader = metadata.NoopReader{}
	}
	return &Scanner{metadata: reader}
}

func (s *Scanner) Scan(ctx context.Context, source string) (domain.ScanResult, error) {
	root, err := filepath.Abs(filepath.Clean(source))
	if err != nil {
		return domain.ScanResult{}, fmt.Errorf("resolve source: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return domain.ScanResult{}, fmt.Errorf("open source: %w", err)
	}
	if !info.IsDir() {
		return domain.ScanResult{}, errNotDirectory
	}

	groups := make(map[string][]string)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if _, ok := audioExtensions[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			return nil
		}
		parent := filepath.Dir(path)
		key := parent
		if samePath(parent, root) {
			key = path
		}
		groups[key] = append(groups[key], path)
		return nil
	})
	if err != nil {
		return domain.ScanResult{}, fmt.Errorf("scan source: %w", err)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := domain.ScanResult{Source: root, ScannedAt: time.Now(), Summary: domain.ScanSummary{MetadataAvailable: s.metadata.Available()}}
	if !s.metadata.Available() {
		result.GlobalNotes = append(result.GlobalNotes, "ffprobe wurde nicht gefunden; Vorschläge basieren nur auf Datei- und Ordnernamen.")
	}
	for _, key := range keys {
		proposal, buildErr := s.buildProposal(ctx, root, key, groups[key])
		if buildErr != nil {
			return domain.ScanResult{}, buildErr
		}
		result.Proposals = append(result.Proposals, proposal)
		result.Summary.Books++
		result.Summary.Files += len(proposal.Files)
		for _, file := range proposal.Files {
			result.Summary.Bytes += file.Size
		}
	}
	return result, nil
}

func (s *Scanner) buildProposal(ctx context.Context, root, groupPath string, paths []string) (domain.BookProposal, error) {
	sort.Strings(paths)
	files := make([]domain.AudioFile, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return domain.BookProposal{}, fmt.Errorf("stat audio file: %w", err)
		}
		file := domain.AudioFile{
			Path:      path,
			Name:      filepath.Base(path),
			Extension: strings.ToLower(filepath.Ext(path)),
			Size:      info.Size(),
		}
		if s.metadata.Available() {
			file.Metadata, err = s.metadata.Read(ctx, path)
			if err != nil {
				file.MetadataNotice = err.Error()
			}
		}
		file.Track = file.Metadata.Track
		file.Disc = file.Metadata.Disc
		files = append(files, file)
	}

	sort.SliceStable(files, func(i, j int) bool {
		left, right := files[i], files[j]
		if left.Disc != right.Disc && (left.Disc > 0 || right.Disc > 0) {
			return zeroLast(left.Disc, right.Disc)
		}
		if left.Track != right.Track && (left.Track > 0 || right.Track > 0) {
			return zeroLast(left.Track, right.Track)
		}
		return naturalLess(left.Name, right.Name)
	})

	proposal := domain.BookProposal{
		ID:         proposalID(paths),
		SourceRoot: root,
		GroupPath:  groupPath,
		Files:      files,
		Status:     domain.StatusReviewRequired,
		Metadata: domain.BookMetadata{
			Evidence: make(map[string]domain.Evidence),
		},
	}
	proposal.Metadata.Title, proposal.Metadata.Evidence["title"] = inferTitle(root, groupPath, files)
	proposal.Metadata.Author, proposal.Metadata.Evidence["author"] = inferAuthor(files)
	proposal.Metadata.Series, proposal.Metadata.Evidence["series"] = inferField(files, "series")
	proposal.Metadata.SeriesSequence, proposal.Metadata.Evidence["seriesSequence"] = inferField(files, "seriesSequence")
	proposal.Metadata.Narrator, proposal.Metadata.Evidence["narrator"] = inferField(files, "narrator")
	proposal.Metadata.Language, proposal.Metadata.Evidence["language"] = inferField(files, "language")
	proposal.Metadata.ASIN, proposal.Metadata.Evidence["asin"] = inferField(files, "asin")
	proposal.Metadata.ISBN, proposal.Metadata.Evidence["isbn"] = inferField(files, "isbn")

	if proposal.Metadata.SeriesSequence == "" {
		if match := seriesPrefix.FindStringSubmatch(filepath.Base(groupPath)); len(match) == 3 && proposal.Metadata.Series != "" {
			proposal.Metadata.SeriesSequence = strings.ReplaceAll(match[1], ",", ".")
			proposal.Metadata.Evidence["seriesSequence"] = domain.Evidence{Value: proposal.Metadata.SeriesSequence, Source: "folder", Confidence: .55}
		}
	}
	if proposal.Metadata.Author == "" {
		proposal.Metadata.Author = "Unbekannter Autor"
		proposal.Metadata.Evidence["author"] = domain.Evidence{Value: proposal.Metadata.Author, Source: "fallback", Confidence: .05}
		proposal.Warnings = append(proposal.Warnings, "Autor konnte lokal nicht erkannt werden.")
	}
	if proposal.Metadata.Title == "" {
		proposal.Metadata.Title = "Unbekanntes Hörbuch"
		proposal.Metadata.Evidence["title"] = domain.Evidence{Value: proposal.Metadata.Title, Source: "fallback", Confidence: .05}
		proposal.Warnings = append(proposal.Warnings, "Titel konnte lokal nicht erkannt werden.")
	}
	if len(files) > 1 && !hasTrackEvidence(files) {
		proposal.Warnings = append(proposal.Warnings, "Mehrere Dateien ohne vollständige Tracknummern; Reihenfolge bitte prüfen.")
	}
	proposal.Confidence = averageRequiredConfidence(proposal.Metadata)
	return proposal, nil
}
