package applog

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"path/filepath"

	"github.com/dennis/myfilesorter/internal/domain"
)

func TestLoggerKeepsSessionEntriesAndWritesJSONLines(t *testing.T) {
	logger := New(t.TempDir())
	logger.Info("scan", "Scan gestartet", map[string]string{"source": "/books"})
	logger.Error("scan", "Scan fehlgeschlagen", map[string]string{"error": "test"})

	snapshot := logger.Snapshot()
	if snapshot.SessionID == "" || len(snapshot.Entries) != 2 || snapshot.FilePath == "" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	data, err := os.ReadFile(snapshot.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected log file: %s", data)
	}
	var entry domain.LogEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Level != "error" || entry.Details["error"] != "test" {
		t.Fatalf("unexpected entry: %#v", entry)
	}
}

func TestLoggerRejectsSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "logs")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	logger := New(link)
	logger.Info("test", "keine Dateiausgabe", nil)
	snapshot := logger.Snapshot()
	if snapshot.Warning == "" || snapshot.FilePath != "" {
		t.Fatalf("expected insecure log directory to be rejected: %#v", snapshot)
	}
}
