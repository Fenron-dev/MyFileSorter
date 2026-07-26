package applog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

const maxMemoryEntries = 2000

type Logger struct {
	mu        sync.RWMutex
	sessionID string
	filePath  string
	warning   string
	entries   []domain.LogEntry
	now       func() time.Time
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
	logger.filePath = filepath.Join(directory, "session-"+sessionID+".jsonl")
	return logger
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

func (l *Logger) write(level, component, message string, details map[string]string) {
	entry := domain.LogEntry{
		Timestamp: l.now().UTC(), Level: level, Component: component, Message: message, Details: cloneDetails(details),
	}
	l.mu.Lock()
	if len(l.entries) >= maxMemoryEntries {
		copy(l.entries, l.entries[len(l.entries)-maxMemoryEntries+1:])
		l.entries = l.entries[:maxMemoryEntries-1]
	}
	l.entries = append(l.entries, entry)
	if l.filePath != "" {
		data, err := json.Marshal(entry)
		if err == nil {
			file, openErr := os.OpenFile(l.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if openErr == nil {
				_, writeErr := fmt.Fprintln(file, string(data))
				closeErr := file.Close()
				if writeErr != nil {
					l.warning = "Logdatei konnte nicht geschrieben werden: " + writeErr.Error()
				} else if closeErr != nil {
					l.warning = "Logdatei konnte nicht geschlossen werden: " + closeErr.Error()
				}
			} else {
				l.warning = "Logdatei konnte nicht geöffnet werden: " + openErr.Error()
			}
		}
	}
	l.mu.Unlock()
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
