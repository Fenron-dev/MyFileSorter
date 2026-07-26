package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dennis/myfilesorter/internal/domain"
)

func TestExecuteAndUndo(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source", "book.m4b")
	target := filepath.Join(root, "library", "Author", "Book", "01 - Book.m4b")
	writeTestFile(t, source, "audio data")
	service := New(filepath.Join(root, "journals"))
	plan := domain.OperationPlan{Executable: true, TargetRoot: filepath.Join(root, "library"), Operations: []domain.PlannedOperation{{
		ProposalID: "book", Source: source, Target: target, Size: 10,
	}}}

	result, err := service.Execute(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || result.Completed != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source should be removed: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "audio data" {
		t.Fatalf("unexpected target: %q, %v", data, err)
	}

	undo, err := service.Undo(context.Background(), result.JournalID)
	if err != nil {
		t.Fatal(err)
	}
	if undo.Status != "undone" {
		t.Fatalf("unexpected undo: %#v", undo)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "audio data" {
		t.Fatalf("unexpected restored source: %q, %v", data, err)
	}
}

func TestExecuteDoesNotOverwriteTarget(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.m4b")
	target := filepath.Join(root, "target.m4b")
	writeTestFile(t, source, "source")
	writeTestFile(t, target, "existing")
	service := New(filepath.Join(root, "journals"))
	plan := domain.OperationPlan{Executable: true, Operations: []domain.PlannedOperation{{Source: source, Target: target, Size: 6}}}

	result, err := service.Execute(context.Background(), plan)
	if err == nil || result.Status != "failed" {
		t.Fatalf("expected failed execution, got %#v, %v", result, err)
	}
	if data, _ := os.ReadFile(source); string(data) != "source" {
		t.Fatalf("source changed: %q", data)
	}
	if data, _ := os.ReadFile(target); string(data) != "existing" {
		t.Fatalf("target changed: %q", data)
	}
}

func TestUndoRejectsChangedTarget(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.m4b")
	target := filepath.Join(root, "target.m4b")
	writeTestFile(t, source, "original")
	service := New(filepath.Join(root, "journals"))
	result, err := service.Execute(context.Background(), domain.OperationPlan{Executable: true, Operations: []domain.PlannedOperation{{
		Source: source, Target: target, Size: 8,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Undo(context.Background(), result.JournalID); err == nil {
		t.Fatal("expected changed target to block undo")
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source should not be restored: %v", err)
	}
}

func TestCleanupMovesSidecarToUndoableQuarantine(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "book", "folder.jpg")
	writeTestFile(t, source, "cover")
	service := New(filepath.Join(root, "config", "journals"))
	result, err := service.Execute(context.Background(), domain.OperationPlan{Executable: true, Operations: []domain.PlannedOperation{{
		Action: "remove", Category: "sidecar", Source: source, Size: 5,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("sidecar should be removed from source: %v", err)
	}
	journal, err := service.load(result.JournalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(journal.Operations[0].Target, filepath.Join("quarantine", result.JournalID)) {
		t.Fatalf("unexpected quarantine target: %s", journal.Operations[0].Target)
	}
	if _, err := service.Undo(context.Background(), result.JournalID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "cover" {
		t.Fatalf("sidecar was not restored: %q, %v", data, err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
