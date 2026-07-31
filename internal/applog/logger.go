package applog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

const (
	maxMemoryEntries = 2000
	maxLogFiles      = 30
	maxLogAge        = 90 * 24 * time.Hour
	maxLogBytes      = 16 << 20
)

type Logger struct {
	mu        sync.RWMutex
	sessionID string
	filePath  string
	directory string
	warning   string
	entries   []domain.LogEntry
	now       func() time.Time
	fileBytes int64
}

func New(directory string) *Logger {
	now := time.Now
	sessionID := now().UTC().Format("20060102T150405.000000000Z") + "-" + strconv.Itoa(os.Getpid())
	logger := &Logger{sessionID: sessionID, now: now}
	if directory == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			logger.warning = "Logordner konnte nicht bestimmt werden: " + err.Error()
			return logger
		}
		directory = filepath.Join(config, "MyFileSorter", "logs")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		logger.warning = "Logordner konnte nicht angelegt werden: " + err.Error()
		return logger
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		logger.warning = "Logordner ist kein sicheres Verzeichnis"
		return logger
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		logger.warning = "Logordner konnte nicht abgesichert werden: " + err.Error()
		return logger
	}
	if err := pruneLogs(directory, now()); err != nil {
		logger.warning = "Alte Logdateien konnten nicht bereinigt werden: " + err.Error()
	}
	logger.directory = directory
	logger.filePath = filepath.Join(directory, "session-"+sessionID+".jsonl")
	return logger
}

func pruneLogs(directory string, now time.Time) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	type logFile struct {
		name    string
		modTime time.Time
	}
	files := make([]logFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "session-") || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, logFile{name: entry.Name(), modTime: info.ModTime()})
	}
	sort.Slice(files, func(left, right int) bool { return files[left].modTime.After(files[right].modTime) })
	for index, file := range files {
		if index < maxLogFiles && now.Sub(file.modTime) <= maxLogAge {
			continue
		}
		if err := os.Remove(filepath.Join(directory, file.name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (l *Logger) Info(component, message string, details map[string]string) {
	l.write("info", component, message, details)
}

func (l *Logger) Warn(component, message string, details map[string]string) {
	l.write("warning", component, message, details)
}

func (l *Logger) Error(component, message string, details map[string]string) {
	l.write("error", component, message, details)
}

func (l *Logger) Snapshot() domain.LogSnapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	entries := make([]domain.LogEntry, len(l.entries))
	for index, entry := range l.entries {
		entry.Details = cloneDetails(entry.Details)
		entries[index] = entry
	}
	return domain.LogSnapshot{
		SessionID: l.sessionID, FilePath: l.filePath, Warning: l.warning, Entries: entries,
	}
}

func (l *Logger) Sessions() ([]domain.LogSessionInfo, error) {
	l.mu.RLock()
	directory := l.directory
	current := l.sessionID
	l.mu.RUnlock()
	if directory == "" {
		return []domain.LogSessionInfo{{SessionID: current, Current: true}}, nil
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("Logordner lesen: %w", err)
	}
	result := make([]domain.LogSessionInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "session-") || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "session-"), ".jsonl")
		if !validSessionID(id) {
			continue
		}
		result = append(result, domain.LogSessionInfo{SessionID: id, ModifiedAt: info.ModTime(), Size: info.Size(), Current: id == current})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ModifiedAt.After(result[right].ModifiedAt) })
	return result, nil
}

func (l *Logger) LoadSession(sessionID string) (domain.LogSnapshot, error) {
	sessionID = strings.TrimSpace(sessionID)
	if !validSessionID(sessionID) {
		return domain.LogSnapshot{}, fmt.Errorf("ungültige Log-Sitzungs-ID")
	}
	l.mu.RLock()
	if sessionID == l.sessionID {
		l.mu.RUnlock()
		return l.Snapshot(), nil
	}
	directory := l.directory
	l.mu.RUnlock()
	if directory == "" {
		return domain.LogSnapshot{}, fmt.Errorf("historische Logs sind nicht verfügbar")
	}
	path := filepath.Join(directory, "session-"+sessionID+".jsonl")
	info, err := os.Lstat(path)
	if err != nil {
		return domain.LogSnapshot{}, fmt.Errorf("Log-Sitzung prüfen: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxLogBytes {
		return domain.LogSnapshot{}, fmt.Errorf("Log-Sitzung ist ungültig oder zu groß")
	}
	file, err := os.Open(path)
	if err != nil {
		return domain.LogSnapshot{}, fmt.Errorf("Log-Sitzung öffnen: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return domain.LogSnapshot{}, fmt.Errorf("Log-Sitzung wurde beim Öffnen ausgetauscht")
	}
	snapshot := domain.LogSnapshot{SessionID: sessionID, FilePath: path}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		var entry domain.LogEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || !validLogEntry(entry) {
			continue
		}
		if len(snapshot.Entries) >= maxMemoryEntries {
			copy(snapshot.Entries, snapshot.Entries[1:])
			snapshot.Entries = snapshot.Entries[:len(snapshot.Entries)-1]
		}
		snapshot.Entries = append(snapshot.Entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return domain.LogSnapshot{}, fmt.Errorf("Log-Sitzung lesen: %w", err)
	}
	postInfo, err := file.Stat()
	if err != nil || !os.SameFile(openedInfo, postInfo) || postInfo.Size() != openedInfo.Size() {
		return domain.LogSnapshot{}, fmt.Errorf("Log-Sitzung wurde während des Lesens verändert")
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
		return domain.LogSnapshot{}, fmt.Errorf("Log-Sitzung wurde nach dem Lesen ausgetauscht")
	}
	return snapshot, nil
}

func (l *Logger) write(level, component, message string, details map[string]string) {
	entry := domain.LogEntry{
		Timestamp: l.now().UTC(), Level: level, Component: component, Message: message, Details: cloneDetails(details),
	}
	l.mu.Lock()
	if len(l.entries) >= maxMemoryEntries {
		const discardBatch = maxMemoryEntries / 4
		copy(l.entries, l.entries[discardBatch:])
		l.entries = l.entries[:len(l.entries)-discardBatch]
	}
	l.entries = append(l.entries, entry)
	if l.filePath != "" {
		data, err := json.Marshal(entry)
		if err == nil && l.fileBytes+int64(len(data)+1) <= maxLogBytes {
			file, openErr := openLogAppend(l.filePath)
			if openErr == nil {
				_, writeErr := fmt.Fprintln(file, string(data))
				closeErr := file.Close()
				if writeErr != nil {
					l.warning = "Logdatei konnte nicht geschrieben werden: " + writeErr.Error()
				} else if closeErr != nil {
					l.warning = "Logdatei konnte nicht geschlossen werden: " + closeErr.Error()
				} else {
					l.fileBytes += int64(len(data) + 1)
				}
			} else {
				l.warning = "Logdatei konnte nicht geöffnet werden: " + openErr.Error()
			}
		} else if err == nil && l.warning == "" {
			l.warning = "Das Sitzungslog hat das Größenlimit erreicht; neue Einträge bleiben nur in der App sichtbar."
		}
	}
	l.mu.Unlock()
}

func openLogAppend(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Logziel ist keine reguläre Datei")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		file.Close()
		return nil, fmt.Errorf("Logdatei wurde beim Öffnen ausgetauscht")
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
		file.Close()
		return nil, fmt.Errorf("Logdatei wurde nach dem Öffnen ausgetauscht")
	}
	return file, nil
}

func cloneDetails(details map[string]string) map[string]string {
	if len(details) == 0 {
		return nil
	}
	result := make(map[string]string, len(details))
	for key, value := range details {
		result[key] = value
	}
	return result
}

func validSessionID(value string) bool {
	if value == "" || len(value) > 96 {
		return false
	}
	for _, character := range value {
		if (character >= '0' && character <= '9') || character == 'T' || character == 'Z' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validLogEntry(entry domain.LogEntry) bool {
	if len([]rune(entry.Level)) > 16 || len([]rune(entry.Component)) > 128 || len([]rune(entry.Message)) > 4000 || len(entry.Details) > 64 {
		return false
	}
	for key, value := range entry.Details {
		if len([]rune(key)) > 128 || len([]rune(value)) > 4000 || strings.ContainsRune(key, '\x00') || strings.ContainsRune(value, '\x00') {
			return false
		}
	}
	return true
}
