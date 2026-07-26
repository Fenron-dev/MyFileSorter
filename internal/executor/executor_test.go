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

func TestExecutePreflightsEverySourceBeforeMovingAnything(t *testing.T) {
	root := t.TempDir()
	firstSource := filepath.Join(root, "source", "01.mp3")
	missingSource := filepath.Join(root, "source", "02.mp3")
	firstTarget := filepath.Join(root, "target", "01.mp3")
	writeTestFile(t, firstSource, "first")
	service := New(filepath.Join(root, "journals"))
	plan := domain.OperationPlan{Executable: true, Operations: []domain.PlannedOperation{
		{Source: firstSource, Target: firstTarget, Size: 5},
		{Source: missingSource, Target: filepath.Join(root, "target", "02.mp3"), Size: 6},
	}}

	result, err := service.Execute(context.Background(), plan)
	if err == nil || result.Completed != 0 || result.Status != "failed" {
		t.Fatalf("expected preflight failure, got %#v, %v", result, err)
	}
	if data, readErr := os.ReadFile(firstSource); readErr != nil || string(data) != "first" {
		t.Fatalf("first source changed before complete preflight: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(firstTarget); !os.IsNotExist(statErr) {
		t.Fatalf("first target should not exist: %v", statErr)
	}
}

func TestExecuteResolvesEquivalentUnicodeSourcePath(t *testing.T) {
	root := t.TempDir()
	actualDirectory := filepath.Join(root, "Ungeku\u0308rzt")
	actualSource := filepath.Join(actualDirectory, "01 - Kapitel.mp3")
	plannedSource := filepath.Join(root, "Ungekürzt", "01 - Kapitel.mp3")
	target := filepath.Join(root, "target", "01 - Kapitel.mp3")
	writeTestFile(t, actualSource, "audio")
	service := New(filepath.Join(root, "journals"))
	result, err := service.Execute(context.Background(), domain.OperationPlan{Executable: true, Operations: []domain.PlannedOperation{{
		Source: plannedSource, Target: target, Size: 5,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "audio" {
		t.Fatalf("unicode-equivalent source was not moved: %q, %v", data, readErr)
	}
}

func TestExecuteReportsCompletedOperationsAndBytes(t *testing.T) {
	root := t.TempDir()
	firstSource := filepath.Join(root, "source", "01.mp3")
	secondSource := filepath.Join(root, "source", "02.mp3")
	writeTestFile(t, firstSource, "first")
	writeTestFile(t, secondSource, "second")
	service := New(filepath.Join(root, "journals"))
	progress := make([]domain.ExecutionProgress, 0)
	result, err := service.ExecuteWithProgress(context.Background(), domain.OperationPlan{
		Executable: true,
		Operations: []domain.PlannedOperation{
			{Source: firstSource, Target: filepath.Join(root, "target", "01.mp3"), Size: 5},
			{Source: secondSource, Target: filepath.Join(root, "target", "02.mp3"), Size: 6},
		},
	}, func(update domain.ExecutionProgress) {
		progress = append(progress, update)
	})
	if err != nil || result.Status != "completed" {
		t.Fatalf("unexpected execution result: %#v, %v", result, err)
	}
	last := progress[len(progress)-1]
	if last.Status != "completed" || last.Completed != 2 || last.Total != 2 || last.CompletedBytes != 11 || last.TotalBytes != 11 {
		t.Fatalf("unexpected final progress: %#v", last)
	}
	if progress[0].Status != "checking" {
		t.Fatalf("expected source-checking progress first, got %#v", progress[0])
	}
}

func TestHistorySurvivesServiceRestartAndTracksUndo(t *testing.T) {
	root := t.TempDir()
	journalDirectory := filepath.Join(root, "journals")
	source := filepath.Join(root, "source", "book.m4b")
	target := filepath.Join(root, "library", "book.m4b")
	writeTestFile(t, source, "audio")
	service := New(journalDirectory)
	result, err := service.Execute(context.Background(), domain.OperationPlan{
		Executable: true, TargetRoot: filepath.Join(root, "library"),
		Operations: []domain.PlannedOperation{{Source: source, Target: target, Size: 5}},
	})
	if err != nil {
		t.Fatal(err)
	}

	restarted := New(journalDirectory)
	history, err := restarted.History()
	if err != nil || len(history) != 1 {
		t.Fatalf("unexpected history: %#v, %v", history, err)
	}
	if history[0].JournalID != result.JournalID || history[0].Status != "completed" || history[0].Completed != 1 || !history[0].CanUndo {
		t.Fatalf("unexpected completed run: %#v", history[0])
	}
	if _, err := restarted.Undo(context.Background(), result.JournalID); err != nil {
		t.Fatal(err)
	}
	history, err = restarted.History()
	if err != nil || history[0].Status != "undone" || history[0].CanUndo {
		t.Fatalf("unexpected undone history: %#v, %v", history, err)
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
