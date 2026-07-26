package applog

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

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
