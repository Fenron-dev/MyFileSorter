package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

const (
	snapshotVersion = 1
	keepSnapshots   = 3
	maxSnapshotSize = 128 << 20
	maxProposals    = 100_000
	maxFiles        = 1_000_000
)

type Snapshot struct {
	Version    int                   `json:"version"`
	Generation uint64                `json:"generation,omitempty"`
	SavedAt    time.Time             `json:"savedAt"`
	Proposals  []domain.BookProposal `json:"proposals"`
}

type snapshotCandidate struct {
	name     string
	snapshot Snapshot
}

// Store writes immutable, generation-named snapshots. This avoids replacing a
// live file in place and leaves older valid generations available after a crash.
type Store struct {
	mu        sync.Mutex
	directory string
	now       func() time.Time
}

func New(directory string) *Store {
	return &Store{directory: directory, now: time.Now}
}

func (s *Store) Save(proposals []domain.BookProposal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	directory, err := s.resolveDirectory()
	if err != nil {
		return err
	}
	if err := ensurePrivateDirectory(directory); err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("Arbeitsstände für neue Generation lesen: %w", err)
	}
	generation, err := nextGeneration(entries)
	if err != nil {
		return err
	}
	snapshot := Snapshot{Version: snapshotVersion, Generation: generation, SavedAt: s.now().UTC(), Proposals: proposals}
	if !validSnapshot(snapshot) {
		return fmt.Errorf("Arbeitsstand enthält ungültige oder zu viele Daten")
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("Arbeitsstand serialisieren: %w", err)
	}
	if len(data) > maxSnapshotSize {
		return fmt.Errorf("Arbeitsstand ist größer als %d MiB", maxSnapshotSize>>20)
	}
	temporary, err := os.CreateTemp(directory, ".workspace-*.tmp")
	if err != nil {
		return fmt.Errorf("Arbeitsstand vorbereiten: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("Arbeitsstand schreiben: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("Arbeitsstand synchronisieren: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	name := fmt.Sprintf("workspace-g%020d-%s.json", snapshot.Generation, suffix)
	if err := os.Rename(temporaryName, filepath.Join(directory, name)); err != nil {
		return fmt.Errorf("Arbeitsstand finalisieren: %w", err)
	}
	syncDirectory(directory)
	// The new generation is committed at this point. Retention cleanup is best
	// effort so callers never mistake a successful save for a failed mutation.
	_ = prune(directory, keepSnapshots)
	return nil
}

func (s *Store) Load() ([]domain.BookProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	directory, err := s.resolveDirectory()
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(directory); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("Arbeitsstände lesen: %w", err)
	}
	names := snapshotNames(entries)
	candidates := make([]snapshotCandidate, 0, len(names))
	for _, name := range names {
		path := filepath.Join(directory, name)
		data, readErr := readSnapshot(path)
		if readErr != nil {
			continue
		}
		var snapshot Snapshot
		if json.Unmarshal(data, &snapshot) != nil || !validSnapshot(snapshot) {
			continue
		}
		if namedGeneration, found := generationFromName(name); found && namedGeneration != snapshot.Generation {
			continue
		}
		candidates = append(candidates, snapshotCandidate{name: name, snapshot: snapshot})
	}
	if len(candidates) > 0 {
		sort.Slice(candidates, func(left, right int) bool { return candidateOlder(candidates[left], candidates[right]) })
		return candidates[len(candidates)-1].snapshot.Proposals, nil
	}
	if len(names) > 0 {
		return nil, fmt.Errorf("kein gültiger gespeicherter Arbeitsstand gefunden")
	}
	return nil, nil
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	directory, err := s.resolveDirectory()
	if err != nil {
		return err
	}
	if err := ensurePrivateDirectory(directory); err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, name := range snapshotNames(entries) {
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	syncDirectory(directory)
	return nil
}

func (s *Store) resolveDirectory() (string, error) {
	if strings.TrimSpace(s.directory) != "" {
		return s.directory, nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("Arbeitsordner bestimmen: %w", err)
	}
	return filepath.Join(config, "MyFileSorter", "workspace"), nil
}

func ensurePrivateDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("Arbeitsordner anlegen: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("Arbeitsordner ist kein sicheres Verzeichnis")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("Arbeitsordner absichern: %w", err)
	}
	return nil
}

func snapshotNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "workspace-") || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}

func prune(directory string, keep int) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	names := snapshotNames(entries)
	sort.Slice(names, func(left, right int) bool { return snapshotNameOlder(names[left], names[right]) })
	for len(names) > keep {
		if err := os.Remove(filepath.Join(directory, names[0])); err != nil && !os.IsNotExist(err) {
			return err
		}
		names = names[1:]
	}
	return nil
}

func nextGeneration(entries []os.DirEntry) (uint64, error) {
	var maximum uint64
	for _, name := range snapshotNames(entries) {
		if generation, found := generationFromName(name); found && generation > maximum {
			maximum = generation
		}
	}
	if maximum == math.MaxUint64 {
		return 0, fmt.Errorf("Arbeitsstand-Generation ist ausgeschöpft")
	}
	return maximum + 1, nil
}

func generationFromName(name string) (uint64, bool) {
	const prefix = "workspace-g"
	if !strings.HasPrefix(name, prefix) || len(name) < len(prefix)+21 {
		return 0, false
	}
	digits := name[len(prefix) : len(prefix)+20]
	if name[len(prefix)+20] != '-' {
		return 0, false
	}
	generation, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, false
	}
	return generation, generation > 0
}

func snapshotNameOlder(left, right string) bool {
	leftGeneration, leftFound := generationFromName(left)
	rightGeneration, rightFound := generationFromName(right)
	if leftFound != rightFound {
		return !leftFound
	}
	if leftFound && leftGeneration != rightGeneration {
		return leftGeneration < rightGeneration
	}
	return left < right
}

func candidateOlder(left, right snapshotCandidate) bool {
	if left.snapshot.Generation != right.snapshot.Generation {
		return left.snapshot.Generation < right.snapshot.Generation
	}
	if !left.snapshot.SavedAt.Equal(right.snapshot.SavedAt) {
		return left.snapshot.SavedAt.Before(right.snapshot.SavedAt)
	}
	return left.name < right.name
}

func randomSuffix() (string, error) {
	data := make([]byte, 6)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("Arbeitsstand-ID erzeugen: %w", err)
	}
	return hex.EncodeToString(data), nil
}

func validSnapshot(snapshot Snapshot) bool {
	if snapshot.Version != snapshotVersion || snapshot.SavedAt.IsZero() || len(snapshot.Proposals) > maxProposals {
		return false
	}
	files := 0
	ids := make(map[string]bool, len(snapshot.Proposals))
	for _, proposal := range snapshot.Proposals {
		if !validProposal(proposal) || ids[proposal.ID] {
			return false
		}
		ids[proposal.ID] = true
		files += len(proposal.Files) + len(proposal.Companions)
		if files > maxFiles {
			return false
		}
	}
	return true
}

func readSnapshot(path string) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil || !pathInfo.Mode().IsRegular() || pathInfo.Size() > maxSnapshotSize {
		return nil, fmt.Errorf("Arbeitsstanddatei ist ungültig")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return nil, fmt.Errorf("Arbeitsstanddatei wurde beim Öffnen ausgetauscht")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotSize+1))
	if err != nil || len(data) > maxSnapshotSize {
		return nil, fmt.Errorf("Arbeitsstanddatei ist zu groß oder nicht lesbar")
	}
	postInfo, err := file.Stat()
	if err != nil || !os.SameFile(openedInfo, postInfo) || int64(len(data)) != openedInfo.Size() {
		return nil, fmt.Errorf("Arbeitsstanddatei wurde während des Lesens verändert")
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
		return nil, fmt.Errorf("Arbeitsstanddatei wurde nach dem Lesen ausgetauscht")
	}
	return data, nil
}

func validProposal(proposal domain.BookProposal) bool {
	if !validIdentifier(proposal.ID) || !validStatus(proposal.Status) || !filepath.IsAbs(proposal.SourceRoot) || !filepath.IsAbs(proposal.GroupPath) {
		return false
	}
	if proposal.ExecutionJournalID != "" && !validIdentifier(proposal.ExecutionJournalID) {
		return false
	}
	if !within(proposal.SourceRoot, proposal.GroupPath) || !validMetadata(proposal.Metadata) || math.IsNaN(proposal.Confidence) || math.IsInf(proposal.Confidence, 0) || proposal.Confidence < 0 || proposal.Confidence > 1 {
		return false
	}
	if len(proposal.Warnings) > 100 {
		return false
	}
	for _, warning := range proposal.Warnings {
		if !validText(warning, 4000) {
			return false
		}
	}
	paths := make(map[string]bool, len(proposal.Files)+len(proposal.Companions))
	for _, file := range proposal.Files {
		clean := filepath.Clean(file.Path)
		if !filepath.IsAbs(file.Path) || !within(proposal.SourceRoot, clean) || paths[clean] || !validText(file.Name, 1024) || !validText(file.Extension, 32) || !validText(file.TargetTitle, 300) || !validText(file.MetadataNotice, 4000) {
			return false
		}
		if file.Size < 0 || file.Track < 0 || file.Track > 999999 || file.Disc < 0 || file.Disc > 9999 || !validEmbeddedMetadata(file.Metadata) {
			return false
		}
		paths[clean] = true
	}
	for _, file := range proposal.Companions {
		clean := filepath.Clean(file.Path)
		if !filepath.IsAbs(file.Path) || !within(proposal.SourceRoot, clean) || paths[clean] || file.Size < 0 || !validText(file.Name, 1024) || !validText(file.Extension, 32) {
			return false
		}
		if file.Kind != domain.CompanionEbook && file.Kind != domain.CompanionDiscard && file.Kind != domain.CompanionUnknown {
			return false
		}
		paths[clean] = true
	}
	return true
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

func validStatus(status domain.ProposalStatus) bool {
	switch status {
	case domain.StatusReviewRequired, domain.StatusConfirmed, domain.StatusExcluded, domain.StatusImported, domain.StatusConflict, domain.StatusError:
		return true
	default:
		return false
	}
}

func validMetadata(metadata domain.BookMetadata) bool {
	fields := []struct {
		value   string
		maximum int
	}{
		{metadata.Title, 500}, {metadata.Author, 300}, {metadata.Series, 300}, {metadata.SeriesSequence, 32},
		{metadata.EditionInfo, 300}, {metadata.Narrator, 300}, {metadata.Language, 64}, {metadata.ASIN, 32}, {metadata.ISBN, 32},
	}
	for _, field := range fields {
		if !validText(field.value, field.maximum) {
			return false
		}
	}
	if len(metadata.Evidence) > 32 {
		return false
	}
	for key, evidence := range metadata.Evidence {
		if key == "" || !validText(key, 64) || !validText(evidence.Value, 1000) || !validText(evidence.Source, 128) || math.IsNaN(evidence.Confidence) || math.IsInf(evidence.Confidence, 0) || evidence.Confidence < 0 || evidence.Confidence > 1 {
			return false
		}
	}
	return true
}

func validEmbeddedMetadata(metadata domain.EmbeddedMetadata) bool {
	values := []string{metadata.Title, metadata.Album, metadata.Artist, metadata.AlbumArtist, metadata.Series, metadata.SeriesSequence, metadata.Narrator, metadata.Language, metadata.ASIN, metadata.ISBN}
	for _, value := range values {
		if !validText(value, 1000) {
			return false
		}
	}
	return metadata.Track >= 0 && metadata.Track <= 999999 && metadata.Disc >= 0 && metadata.Disc <= 9999 && metadata.DurationMillis >= 0
}

func validText(value string, maximum int) bool {
	return len([]rune(value)) <= maximum && !strings.ContainsRune(value, '\x00')
}

func within(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func syncDirectory(directory string) {
	handle, err := os.Open(directory)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}
