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
	"sync"
	"sync/atomic"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
	"github.com/dennis/myfilesorter/internal/metadata"
)

var (
	errNotDirectory = errors.New("source is not a directory")
	trackPrefix     = regexp.MustCompile(`^\s*(?:cd|disc|disk|track|part|teil)?\s*\d{1,4}(?:[._ -]+)`)
	trackNumber     = regexp.MustCompile(`(?i)^\s*(?:cd|disc|disk|track|part|teil)?\s*(\d{1,4})(?:[._ -]+)`)
	seriesPrefix    = regexp.MustCompile(`^\s*(\d+(?:[.,]\d+)?)\s*[-._]\s*(.+)$`)
	discDirectory   = regexp.MustCompile(`(?i)^\s*(?:cd|disc|disk)\s*[-_. ]*(\d{1,3})(?:\s*(?:of|von)\s*\d{1,3})?\s*$`)
)

const (
	maxMetadataWorkers   = 4
	maxScanIssueDetails  = 20
	maxAudioFiles        = 200_000
	maxBookGroups        = 50_000
	maxDiscoveredEntries = 1_000_000
	maxCompanionsPerBook = 50_000
	maxResultFiles       = 1_000_000
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
	scanMu   sync.Mutex
	cacheMu  sync.RWMutex
	cache    map[string]metadataCacheEntry
}

type metadataCacheEntry struct {
	Size           int64
	ModifiedNanos  int64
	Metadata       domain.EmbeddedMetadata
	MetadataNotice string
}

type audioScanResult struct {
	File domain.AudioFile
	Err  error
}

type audioScanTask struct {
	GroupIndex int
	FileIndex  int
	Path       string
}

type issueCollector struct {
	limit    int
	total    int
	messages []string
}

func New(reader metadata.Reader) *Scanner {
	if reader == nil {
		reader = metadata.NoopReader{}
	}
	return &Scanner{metadata: reader, cache: make(map[string]metadataCacheEntry)}
}

func (s *Scanner) Scan(ctx context.Context, source string) (domain.ScanResult, error) {
	return s.ScanWithProgress(ctx, source, nil)
}

func (s *Scanner) ScanWithProgress(ctx context.Context, source string, progress func(domain.ScanProgress)) (domain.ScanResult, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	if strings.TrimSpace(source) == "" {
		return domain.ScanResult{}, fmt.Errorf("source is required")
	}
	root, err := filepath.Abs(filepath.Clean(source))
	if err != nil {
		return domain.ScanResult{}, fmt.Errorf("resolve source: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return domain.ScanResult{}, fmt.Errorf("resolve physical source: %w", err)
	}
	if isFilesystemRoot(root) {
		return domain.ScanResult{}, fmt.Errorf("filesystem root cannot be used as scan source")
	}
	info, err := os.Stat(root)
	if err != nil {
		return domain.ScanResult{}, fmt.Errorf("open source: %w", err)
	}
	if !info.IsDir() {
		return domain.ScanResult{}, errNotDirectory
	}

	groups := make(map[string][]string)
	audioFileCount := 0
	discoveredEntries := 0
	issues := issueCollector{limit: maxScanIssueDetails}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			issues.Add(fmt.Sprintf("Pfad beim Scan übersprungen: %s: %v", path, walkErr))
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		discoveredEntries++
		if discoveredEntries > maxDiscoveredEntries {
			return fmt.Errorf("scan contains more than %d filesystem entries", maxDiscoveredEntries)
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
		key := logicalGroupPath(root, path)
		audioFileCount++
		if audioFileCount > maxAudioFiles {
			return fmt.Errorf("scan contains more than %d audio files", maxAudioFiles)
		}
		if _, found := groups[key]; !found && len(groups) >= maxBookGroups {
			return fmt.Errorf("scan contains more than %d audiobook groups", maxBookGroups)
		}
		groups[key] = append(groups[key], path)
		if progress != nil && (audioFileCount == 1 || audioFileCount%25 == 0) {
			progress(domain.ScanProgress{Phase: "discovering", Discovered: audioFileCount, CurrentPath: path})
		}
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return domain.ScanResult{}, ctx.Err()
		}
		return domain.ScanResult{}, fmt.Errorf("scan source: %w", err)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		sort.Strings(groups[key])
		keys = append(keys, key)
	}
	sort.Strings(keys)
	scannedFiles, err := s.scanAudioFilesWithProgress(ctx, keys, groups, progress)
	if err != nil {
		return domain.ScanResult{}, err
	}
	s.pruneMetadataCache(groups)

	result := domain.ScanResult{Source: root, ScannedAt: time.Now(), Summary: domain.ScanSummary{MetadataAvailable: s.metadata.Available()}}
	if !s.metadata.Available() {
		result.GlobalNotes = append(result.GlobalNotes, "ffprobe wurde nicht gefunden; Vorschläge basieren nur auf Datei- und Ordnernamen.")
	}
	resultFileCount := 0
	for groupIndex, key := range keys {
		files := make([]domain.AudioFile, 0, len(scannedFiles[groupIndex]))
		groupIssues := issueCollector{limit: 4}
		for _, scanned := range scannedFiles[groupIndex] {
			if scanned.Err != nil {
				message := fmt.Sprintf("Audiodatei übersprungen: %s: %v", scanned.File.Path, scanned.Err)
				issues.Add(message)
				groupIssues.Add(message)
				continue
			}
			files = append(files, scanned.File)
		}
		if len(files) == 0 {
			issues.Add("Hörbuchgruppe ohne lesbare Audiodateien übersprungen: " + key)
			continue
		}
		proposal, buildErr := s.buildProposal(ctx, root, key, files, groupIssues.Notes())
		if buildErr != nil {
			if ctx.Err() != nil {
				return domain.ScanResult{}, ctx.Err()
			}
			issues.Add(fmt.Sprintf("Hörbuchgruppe übersprungen: %s: %v", key, buildErr))
			continue
		}
		resultFileCount += len(proposal.Files) + len(proposal.Companions)
		if resultFileCount > maxResultFiles {
			return domain.ScanResult{}, fmt.Errorf("scan result contains more than %d files", maxResultFiles)
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
	result.GlobalNotes = append(result.GlobalNotes, issues.Notes()...)
	if progress != nil {
		progress(domain.ScanProgress{Phase: "completed", Discovered: audioFileCount, Inspected: audioFileCount})
	}
	return result, nil
}

func (s *Scanner) scanAudioFiles(ctx context.Context, keys []string, groups map[string][]string) ([][]audioScanResult, error) {
	return s.scanAudioFilesWithProgress(ctx, keys, groups, nil)
}

func (s *Scanner) scanAudioFilesWithProgress(ctx context.Context, keys []string, groups map[string][]string, progress func(domain.ScanProgress)) ([][]audioScanResult, error) {
	results := make([][]audioScanResult, len(keys))
	totalFiles := 0
	for groupIndex, key := range keys {
		results[groupIndex] = make([]audioScanResult, len(groups[key]))
		totalFiles += len(groups[key])
	}
	if totalFiles == 0 {
		return results, nil
	}

	workerCount := min(maxMetadataWorkers, totalFiles)
	tasks := make(chan audioScanTask)
	var inspected atomic.Int64
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for task := range tasks {
				file, err := s.inspectAudioFile(ctx, task.Path)
				if err != nil {
					s.forgetMetadata(task.Path)
				}
				results[task.GroupIndex][task.FileIndex] = audioScanResult{File: file, Err: err}
				completed := int(inspected.Add(1))
				if progress != nil && (completed == 1 || completed%10 == 0 || completed == totalFiles) {
					progress(domain.ScanProgress{Phase: "inspecting", Discovered: totalFiles, Inspected: completed, CurrentPath: task.Path})
				}
			}
		}()
	}

	for groupIndex, key := range keys {
		for fileIndex, path := range groups[key] {
			select {
			case <-ctx.Done():
				close(tasks)
				workers.Wait()
				return nil, ctx.Err()
			case tasks <- audioScanTask{GroupIndex: groupIndex, FileIndex: fileIndex, Path: path}:
			}
		}
	}
	close(tasks)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Scanner) inspectAudioFile(ctx context.Context, path string) (domain.AudioFile, error) {
	file := domain.AudioFile{
		Path:      path,
		Name:      filepath.Base(path),
		Extension: strings.ToLower(filepath.Ext(path)),
	}
	info, err := os.Lstat(path)
	if err != nil {
		return file, fmt.Errorf("Audiodatei prüfen: %w", err)
	}
	if !info.Mode().IsRegular() {
		return file, fmt.Errorf("Audiodatei ist keine reguläre Datei")
	}
	file.Size = info.Size()
	handle, err := os.Open(path)
	if err != nil {
		return file, fmt.Errorf("Audiodatei öffnen: %w", err)
	}
	openedInfo, statErr := handle.Stat()
	if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		handle.Close()
		return file, fmt.Errorf("Audiodatei wurde während des Scans verändert")
	}
	if err := handle.Close(); err != nil {
		return file, fmt.Errorf("Audiodatei schließen: %w", err)
	}
	if s.metadata.Available() {
		file.Metadata, file.MetadataNotice, err = s.readMetadata(ctx, path, info)
		if err != nil {
			return file, err
		}
	}
	file.Track = file.Metadata.Track
	file.Disc = file.Metadata.Disc
	if file.Track <= 0 {
		file.Track = trackNumberFromName(file.Name)
	}
	return file, nil
}

func (s *Scanner) readMetadata(ctx context.Context, path string, info os.FileInfo) (domain.EmbeddedMetadata, string, error) {
	key := filepath.Clean(path)
	modified := info.ModTime().UnixNano()
	s.cacheMu.RLock()
	cached, found := s.cache[key]
	s.cacheMu.RUnlock()
	if found && cached.Size == info.Size() && cached.ModifiedNanos == modified {
		return cached.Metadata, cached.MetadataNotice, nil
	}

	value, err := s.metadata.Read(ctx, path)
	if ctx.Err() != nil {
		return domain.EmbeddedMetadata{}, "", ctx.Err()
	}
	notice := ""
	if err != nil {
		notice = err.Error()
		value = domain.EmbeddedMetadata{}
	}
	s.cacheMu.Lock()
	s.cache[key] = metadataCacheEntry{
		Size: info.Size(), ModifiedNanos: modified, Metadata: value, MetadataNotice: notice,
	}
	s.cacheMu.Unlock()
	return value, notice, nil
}

func (s *Scanner) pruneMetadataCache(groups map[string][]string) {
	seen := make(map[string]struct{})
	for _, paths := range groups {
		for _, path := range paths {
			seen[filepath.Clean(path)] = struct{}{}
		}
	}
	s.cacheMu.Lock()
	for path := range s.cache {
		if _, found := seen[path]; !found {
			delete(s.cache, path)
		}
	}
	s.cacheMu.Unlock()
}

func (s *Scanner) forgetMetadata(path string) {
	s.cacheMu.Lock()
	delete(s.cache, filepath.Clean(path))
	s.cacheMu.Unlock()
}

func (c *issueCollector) Add(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	c.total++
	if len(c.messages) < c.limit {
		c.messages = append(c.messages, message)
	}
}

func (c issueCollector) Notes() []string {
	result := append([]string(nil), c.messages...)
	if hidden := c.total - len(c.messages); hidden > 0 {
		result = append(result, fmt.Sprintf("Weitere %d Scanproblem(e) wurden zusammengefasst.", hidden))
	}
	return result
}

func (s *Scanner) buildProposal(ctx context.Context, root, groupPath string, files []domain.AudioFile, initialWarnings []string) (domain.BookProposal, error) {
	paths := make([]string, len(files))
	metadataFailures := 0
	metadataFailureExample := ""
	for index := range files {
		paths[index] = files[index].Path
		if files[index].Disc <= 0 {
			files[index].Disc = discNumberForPath(groupPath, files[index].Path)
		}
		if files[index].MetadataNotice != "" {
			metadataFailures++
			if metadataFailureExample == "" {
				metadataFailureExample = fmt.Sprintf("%s: %s", files[index].Name, files[index].MetadataNotice)
			}
		}
	}
	sort.Strings(paths)
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
		Warnings:   append([]string(nil), initialWarnings...),
		Metadata: domain.BookMetadata{
			Evidence: make(map[string]domain.Evidence),
		},
	}
	if metadataFailures > 0 {
		proposal.Warnings = append(proposal.Warnings, fmt.Sprintf(
			"Eingebettete Metadaten konnten für %d Datei(en) nicht gelesen werden; Datei- und Ordnernamen werden weiterhin verwendet. Beispiel: %s",
			metadataFailures, metadataFailureExample,
		))
	}
	companions, companionWarnings, companionErr := scanCompanions(ctx, groupPath, files)
	if companionErr != nil {
		return domain.BookProposal{}, companionErr
	}
	proposal.Companions = companions
	proposal.Warnings = append(proposal.Warnings, companionWarnings...)
	folder := inferFolderMetadata(root, groupPath, files)
	proposal.Metadata.Title, proposal.Metadata.Evidence["title"] = inferTitle(root, groupPath, files, folder)
	proposal.Metadata.Author, proposal.Metadata.Evidence["author"] = inferAuthor(files, folder)
	proposal.Metadata.Series, proposal.Metadata.Evidence["series"] = inferField(files, "series")
	proposal.Metadata.SeriesSequence, proposal.Metadata.Evidence["seriesSequence"] = inferField(files, "seriesSequence")
	if folder.EditionInfo != "" {
		proposal.Metadata.EditionInfo = folder.EditionInfo
		proposal.Metadata.Evidence["editionInfo"] = domain.Evidence{Value: folder.EditionInfo, Source: "folder", Confidence: .75}
	}
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

func logicalGroupPath(root, audioPath string) string {
	parent := filepath.Dir(audioPath)
	for directory := parent; !samePath(directory, root); directory = filepath.Dir(directory) {
		if _, found := discNumberFromDirectory(filepath.Base(directory)); found {
			return filepath.Dir(directory)
		}
		next := filepath.Dir(directory)
		if next == directory {
			break
		}
	}
	if samePath(parent, root) {
		return audioPath
	}
	return parent
}

func discNumberForPath(groupPath, audioPath string) int {
	for directory := filepath.Dir(audioPath); !samePath(directory, groupPath); directory = filepath.Dir(directory) {
		if number, found := discNumberFromDirectory(filepath.Base(directory)); found {
			return number
		}
		next := filepath.Dir(directory)
		if next == directory {
			break
		}
	}
	return 0
}

func discNumberFromDirectory(name string) (int, bool) {
	match := discDirectory.FindStringSubmatch(name)
	if len(match) != 2 {
		return 0, false
	}
	number, err := strconv.Atoi(match[1])
	return number, err == nil && number > 0
}

func isFilesystemRoot(path string) bool {
	clean := filepath.Clean(path)
	return filepath.Dir(clean) == clean
}

func scanCompanions(ctx context.Context, groupPath string, audioFiles []domain.AudioFile) ([]domain.CompanionFile, []string, error) {
	if len(audioFiles) == 1 && samePath(groupPath, audioFiles[0].Path) {
		return nil, nil, nil
	}
	audioPaths := make(map[string]bool, len(audioFiles))
	directories := map[string]struct{}{filepath.Clean(groupPath): {}}
	for _, file := range audioFiles {
		audioPaths[filepath.Clean(file.Path)] = true
		directories[filepath.Clean(filepath.Dir(file.Path))] = struct{}{}
	}
	directoryNames := make([]string, 0, len(directories))
	for directory := range directories {
		directoryNames = append(directoryNames, directory)
	}
	sort.Strings(directoryNames)

	companions := make([]domain.CompanionFile, 0)
	warnings := issueCollector{limit: 4}
	seen := make(map[string]bool)
	unknown := 0
	for _, directory := range directoryNames {
		if err := ctx.Err(); err != nil {
			return nil, warnings.Notes(), err
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			warnings.Add(fmt.Sprintf("Begleitdateien konnten nicht gelesen werden: %s: %v", directory, err))
			continue
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, warnings.Notes(), err
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			cleanedPath := filepath.Clean(path)
			if audioPaths[cleanedPath] || seen[cleanedPath] {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				warnings.Add(fmt.Sprintf("Begleitdatei übersprungen: %s: %v", path, err))
				continue
			}
			if !info.Mode().IsRegular() {
				continue
			}
			extension := strings.ToLower(filepath.Ext(entry.Name()))
			kind := domain.CompanionUnknown
			if _, found := ebookExtensions[extension]; found {
				kind = domain.CompanionEbook
			} else if _, found := discardExtensions[extension]; found {
				kind = domain.CompanionDiscard
			} else {
				unknown++
			}
			seen[cleanedPath] = true
			companions = append(companions, domain.CompanionFile{
				Path: path, Name: entry.Name(), Extension: extension, Size: info.Size(), Kind: kind,
			})
			if len(companions) > maxCompanionsPerBook {
				return nil, warnings.Notes(), fmt.Errorf("Hörbuchgruppe enthält mehr als %d Begleitdateien", maxCompanionsPerBook)
			}
		}
	}
	if unknown > 0 {
		warnings.Add(fmt.Sprintf(
			"%d nicht klassifizierte Begleitdatei(en) werden angezeigt, aber weder verschoben noch entfernt.", unknown,
		))
	}
	sort.Slice(companions, func(left, right int) bool {
		if companions[left].Name == companions[right].Name {
			return companions[left].Path < companions[right].Path
		}
		return naturalLess(companions[left].Name, companions[right].Name)
	})
	return companions, warnings.Notes(), nil
}
