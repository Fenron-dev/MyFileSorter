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
	"strconv"
	"strings"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
	"github.com/dennis/myfilesorter/internal/metadata"
)

var (
	errNotDirectory = errors.New("source is not a directory")
	trackPrefix     = regexp.MustCompile(`^\s*(?:cd|disc|disk|track|part|teil)?\s*\d{1,4}(?:[._ -]+)`)
	trackNumber     = regexp.MustCompile(`(?i)^\s*(?:cd|disc|disk|track|part|teil)?\s*(\d{1,4})(?:[._ -]+)`)
	seriesPrefix    = regexp.MustCompile(`^\s*(\d+(?:[.,]\d+)?)\s*[-._]\s*(.+)$`)
)

var audioExtensions = map[string]struct{}{
	".m4b": {}, ".m4a": {}, ".mp3": {}, ".aac": {}, ".flac": {}, ".ogg": {}, ".opus": {}, ".wav": {},
}

var ebookExtensions = map[string]struct{}{
	".epub": {}, ".pdf": {}, ".mobi": {}, ".azw": {}, ".azw3": {}, ".cbz": {}, ".cbr": {},
}

var discardExtensions = map[string]struct{}{
	".ico": {}, ".jpg": {}, ".jpeg": {}, ".png": {}, ".nfo": {}, ".m3u": {}, ".m3u8": {}, ".cue": {},
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
		for _, file := range proposal.Companions {
			if file.Kind == domain.CompanionEbook {
				result.Summary.Ebooks++
			} else if file.Kind == domain.CompanionDiscard {
				result.Summary.Sidecars++
			}
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
		if file.Track <= 0 {
			file.Track = trackNumberFromName(file.Name)
		}
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
	companions, companionErr := scanCompanions(groupPath, files)
	if companionErr != nil {
		return domain.BookProposal{}, companionErr
	}
	proposal.Companions = companions
	folder := inferFolderMetadata(root, groupPath, files)
	proposal.Metadata.Title, proposal.Metadata.Evidence["title"] = inferTitle(root, groupPath, files, folder)
	proposal.Metadata.Author, proposal.Metadata.Evidence["author"] = inferAuthor(files, folder)
	proposal.Metadata.Series, proposal.Metadata.Evidence["series"] = inferField(files, "series")
	proposal.Metadata.SeriesSequence, proposal.Metadata.Evidence["seriesSequence"] = inferField(files, "seriesSequence")
	proposal.Metadata.Narrator, proposal.Metadata.Evidence["narrator"] = inferField(files, "narrator")
	proposal.Metadata.Language, proposal.Metadata.Evidence["language"] = inferField(files, "language")
	proposal.Metadata.ASIN, proposal.Metadata.Evidence["asin"] = inferField(files, "asin")
	proposal.Metadata.ISBN, proposal.Metadata.Evidence["isbn"] = inferField(files, "isbn")
	if proposal.Metadata.Series == "" && folder.Series != "" {
		proposal.Metadata.Series = folder.Series
		proposal.Metadata.Evidence["series"] = domain.Evidence{Value: folder.Series, Source: "folder", Confidence: .62}
	}
	if proposal.Metadata.SeriesSequence == "" && folder.SeriesSequence != "" {
		proposal.Metadata.SeriesSequence = folder.SeriesSequence
		proposal.Metadata.Evidence["seriesSequence"] = domain.Evidence{Value: folder.SeriesSequence, Source: "folder", Confidence: .62}
	}

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

func trackNumberFromName(name string) int {
	match := trackNumber.FindStringSubmatch(name)
	if len(match) != 2 {
		return 0
	}
	value, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return value
}

func scanCompanions(groupPath string, audioFiles []domain.AudioFile) ([]domain.CompanionFile, error) {
	if len(audioFiles) == 1 && samePath(groupPath, audioFiles[0].Path) {
		return nil, nil
	}
	audioPaths := make(map[string]bool, len(audioFiles))
	for _, file := range audioFiles {
		audioPaths[filepath.Clean(file.Path)] = true
	}
	entries, err := os.ReadDir(groupPath)
	if err != nil {
		return nil, fmt.Errorf("scan companion files: %w", err)
	}
	companions := make([]domain.CompanionFile, 0)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(groupPath, entry.Name())
		if audioPaths[filepath.Clean(path)] {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		kind := domain.CompanionKind("")
		if _, found := ebookExtensions[extension]; found {
			kind = domain.CompanionEbook
		} else if _, found := discardExtensions[extension]; found {
			kind = domain.CompanionDiscard
		}
		if kind == "" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat companion file: %w", err)
		}
		companions = append(companions, domain.CompanionFile{
			Path: path, Name: entry.Name(), Extension: extension, Size: info.Size(), Kind: kind,
		})
	}
	sort.Slice(companions, func(left, right int) bool { return naturalLess(companions[left].Name, companions[right].Name) })
	return companions, nil
}
