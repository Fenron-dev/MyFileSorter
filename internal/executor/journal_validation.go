package executor

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxJournalBytes            = 64 * 1024 * 1024
	maxJournalOperations       = 1_000_000
	maxHistoryJournals         = 200
	maxHistoryBytes            = 64 * 1024 * 1024
	maxHistoryEntriesInspected = 2_000
)

var validJournalStatuses = map[string]struct{}{
	"running": {}, "failed": {}, "completed": {}, "undoing": {}, "undo_failed": {}, "undone": {},
}

var validOperationStatuses = map[string]struct{}{
	"pending": {}, "transferring": {}, "failed": {}, "completed": {}, "undone": {},
}

func (s *Service) validateJournal(journal *Journal, requestedID string) error {
	if journal == nil || journal.Version != journalVersion || journal.ID != requestedID || !validJournalID(journal.ID) {
		return fmt.Errorf("Journal ist ungültig oder inkompatibel")
	}
	if _, found := validJournalStatuses[journal.Status]; !found {
		return fmt.Errorf("Journal enthält einen ungültigen Status %q", journal.Status)
	}
	if len(journal.Operations) == 0 || len(journal.Operations) > maxJournalOperations {
		return fmt.Errorf("Journal enthält eine ungültige Anzahl Operationen")
	}
	if journal.CreatedAt.IsZero() || journal.UpdatedAt.IsZero() {
		return fmt.Errorf("Journal enthält ungültige Zeitangaben")
	}
	targetRoot := strings.TrimSpace(journal.TargetRoot)
	if targetRoot != "" {
		if !isCleanAbsolutePath(targetRoot) {
			return fmt.Errorf("Journal enthält einen ungültigen Zielroot")
		}
		targetRoot = filepath.Clean(targetRoot)
	}
	quarantineRoot, err := s.quarantineRoot(journal.ID)
	if err != nil {
		return err
	}
	seenSources := make(map[string]struct{}, len(journal.Operations))
	seenTargets := make(map[string]struct{}, len(journal.Operations))
	allSources := make(map[string]struct{}, len(journal.Operations))
	for _, operation := range journal.Operations {
		allSources[portablePathKey(operation.Source)] = struct{}{}
	}
	for index, operation := range journal.Operations {
		if operation.Action != "move" && operation.Action != "remove" && operation.Action != "deduplicate" {
			return fmt.Errorf("Journaloperation %d enthält eine ungültige Aktion %q", index+1, operation.Action)
		}
		if _, found := validOperationStatuses[operation.Status]; !found {
			return fmt.Errorf("Journaloperation %d enthält einen ungültigen Status %q", index+1, operation.Status)
		}
		if operation.Size < 0 || !isCleanAbsolutePath(operation.Source) || !isCleanAbsolutePath(operation.Target) {
			return fmt.Errorf("Journaloperation %d enthält ungültige Pfad- oder Größenangaben", index+1)
		}
		sourceKey := portablePathKey(operation.Source)
		targetKey := portablePathKey(operation.Target)
		if sourceKey == targetKey {
			return fmt.Errorf("Journaloperation %d verwendet Quelle und Ziel identisch", index+1)
		}
		if _, exists := seenSources[sourceKey]; exists {
			return fmt.Errorf("Journal enthält eine Quelldatei mehrfach")
		}
		if _, exists := seenTargets[targetKey]; exists {
			return fmt.Errorf("Journal enthält eine Zieldatei mehrfach")
		}
		if _, exists := seenTargets[sourceKey]; exists {
			return fmt.Errorf("Journal verkettet Quellen und Ziele mehrerer Operationen")
		}
		if _, exists := seenSources[targetKey]; exists {
			return fmt.Errorf("Journal verkettet Quellen und Ziele mehrerer Operationen")
		}
		seenSources[sourceKey] = struct{}{}
		seenTargets[targetKey] = struct{}{}
		if operation.Action == "deduplicate" {
			if !isCleanAbsolutePath(operation.DuplicateOf) || portablePathKey(operation.DuplicateOf) == sourceKey || portablePathKey(operation.DuplicateOf) == targetKey {
				return fmt.Errorf("Journaloperation %d enthält ein ungültiges Duplikatziel", index+1)
			}
			if _, usedAsSource := allSources[portablePathKey(operation.DuplicateOf)]; usedAsSource {
				return fmt.Errorf("Journaloperation %d verwendet ein Duplikatziel zugleich als Quelle", index+1)
			}
			if targetRoot != "" && !pathInsideRoot(targetRoot, operation.DuplicateOf) {
				return fmt.Errorf("Journaloperation %d enthält ein Duplikatziel außerhalb des Zielroots", index+1)
			}
		} else if operation.DuplicateOf != "" {
			return fmt.Errorf("Journaloperation %d enthält ein unerwartetes Duplikatziel", index+1)
		}
		allowedRoot := targetRoot
		if operation.Action == "remove" || operation.Action == "deduplicate" {
			allowedRoot = quarantineRoot
		}
		if allowedRoot != "" && !pathInsideRoot(allowedRoot, operation.Target) {
			return fmt.Errorf("Journaloperation %d liegt außerhalb des erlaubten Zielroots", index+1)
		}
		if operation.SHA256 != "" {
			decoded, decodeErr := hex.DecodeString(operation.SHA256)
			if decodeErr != nil || len(decoded) != 32 {
				return fmt.Errorf("Journaloperation %d enthält eine ungültige SHA-256-Prüfsumme", index+1)
			}
		}
		if operation.Status == "completed" && operation.SHA256 == "" {
			return fmt.Errorf("Journaloperation %d enthält keine Prüfsumme", index+1)
		}
		if journal.Status == "completed" && operation.Status != "completed" {
			return fmt.Errorf("abgeschlossenes Journal enthält eine nicht abgeschlossene Operation")
		}
		if journal.Status == "undone" && operation.Status != "undone" {
			return fmt.Errorf("rückgängig gemachtes Journal enthält eine nicht rückgängig gemachte Operation")
		}
	}
	return nil
}

func validJournalID(id string) bool {
	if len(id) != len("20060102T150405Z-001122334455") || id[16] != '-' {
		return false
	}
	if _, err := time.Parse("20060102T150405Z", id[:16]); err != nil {
		return false
	}
	decoded, err := hex.DecodeString(id[17:])
	return err == nil && len(decoded) == 6
}

func isCleanAbsolutePath(path string) bool {
	return strings.TrimSpace(path) != "" && filepath.IsAbs(path) && filepath.Clean(path) == path
}

func pathInsideRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func readLimitedFile(path string, maximum int64) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("Datei ist keine reguläre Datei: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		if err == nil {
			err = fmt.Errorf("Datei wurde beim Öffnen ausgetauscht: %s", path)
		}
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("Datei ist größer als das erlaubte Limit von %d Byte", maximum)
	}
	postInfo, err := file.Stat()
	if err != nil || !sameStableFile(openedInfo, postInfo) || int64(len(data)) != openedInfo.Size() {
		if err == nil {
			err = fmt.Errorf("Datei wurde während des Lesens verändert: %s", path)
		}
		return nil, err
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
		if err == nil {
			err = fmt.Errorf("Datei wurde nach dem Lesen ausgetauscht: %s", path)
		}
		return nil, err
	}
	return data, nil
}
