package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

func TestExecuteAndUndo(t *testing.T) {
	root := physicalTempDir(t)
	source := filepath.Join(root, "source", "book.m4b")
	target := filepath.Join(root, "library", "Author", "Book", "01 - Book.m4b")
	writeTestFile(t, source, "audio data")
	service := New(filepath.Join(root, "journals"))
	plan := domain.OperationPlan{Executable: true, TargetRoot: filepath.Join(root, "library"), Operations: []domain.PlannedOperation{{
		ProposalID: "book", SourceRoot: filepath.Dir(source), Source: source, Target: target, Size: 10,
	}}}

	result, err := service.Execute(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || result.Completed != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(result.ProposalIDs) != 1 || result.ProposalIDs[0] != "book" || len(result.ProposalResults) != 1 || result.ProposalResults[0].Status != "completed" {
		t.Fatalf("proposal result was not rebuilt from the journal: %#v", result)
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

func TestExecuteKeepsVerifiedTargetWhenSourceCannotBeRemoved(t *testing.T) {
	root := physicalTempDir(t)
	firstSource := filepath.Join(root, "readonly-source", "01.mp3")
	secondSource := filepath.Join(root, "source", "02.mp3")
	firstTarget := filepath.Join(root, "library", "01.mp3")
	secondTarget := filepath.Join(root, "library", "02.mp3")
	writeTestFile(t, firstSource, "first")
	writeTestFile(t, secondSource, "second")
	service := New(filepath.Join(root, "journals"))
	service.removeFile = func(path string) error {
		if path == firstSource {
			return os.ErrPermission
		}
		return os.Remove(path)
	}

	result, err := service.Execute(context.Background(), domain.OperationPlan{
		Executable: true,
		Operations: []domain.PlannedOperation{
			{SourceRoot: filepath.Dir(firstSource), Source: firstSource, Target: firstTarget, Size: 5},
			{SourceRoot: filepath.Dir(secondSource), Source: secondSource, Target: secondTarget, Size: 6},
		},
	})
	if err != nil || result.Status != "completed" || result.Completed != 2 {
		t.Fatalf("unexpected copy-only fallback result: %#v, %v", result, err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "fehlender Löschrechte") {
		t.Fatalf("missing retained-source warning: %#v", result.Warnings)
	}
	if data, readErr := os.ReadFile(firstSource); readErr != nil || string(data) != "first" {
		t.Fatalf("retained source changed: %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(firstTarget); readErr != nil || string(data) != "first" {
		t.Fatalf("verified target missing: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(secondSource); !os.IsNotExist(statErr) {
		t.Fatalf("second source should have moved normally: %v", statErr)
	}

	journal, loadErr := service.load(result.JournalID)
	if loadErr != nil || !journal.Operations[0].SourceRetained || journal.Operations[0].SourceRemoveError == "" {
		t.Fatalf("retained source not journaled: %#v, %v", journal, loadErr)
	}
	undo, undoErr := service.Undo(context.Background(), result.JournalID)
	if undoErr != nil || undo.Status != "undone" {
		t.Fatalf("copy-only fallback could not be undone: %#v, %v", undo, undoErr)
	}
	if _, statErr := os.Stat(firstTarget); !os.IsNotExist(statErr) {
		t.Fatalf("copied target should be removed by undo: %v", statErr)
	}
	if data, readErr := os.ReadFile(firstSource); readErr != nil || string(data) != "first" {
		t.Fatalf("original retained source should remain after undo: %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(secondSource); readErr != nil || string(data) != "second" {
		t.Fatalf("normally moved source should be restored: %q, %v", data, readErr)
	}
}

func TestExecuteDoesNotOverwriteTarget(t *testing.T) {
	root := physicalTempDir(t)
	source := filepath.Join(root, "source.m4b")
	target := filepath.Join(root, "target.m4b")
	writeTestFile(t, source, "source")
	writeTestFile(t, target, "existing")
	service := New(filepath.Join(root, "journals"))
	plan := domain.OperationPlan{Executable: true, Operations: []domain.PlannedOperation{{SourceRoot: filepath.Dir(source), Source: source, Target: target, Size: 6}}}

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

func TestInstallFileNoReplaceRejectsLateCollision(t *testing.T) {
	root := physicalTempDir(t)
	temporary := filepath.Join(root, "transfer.part")
	target := filepath.Join(root, "book.m4b")
	writeTestFile(t, temporary, "new")
	writeTestFile(t, target, "existing")

	if err := installFileNoReplace(temporary, target); err == nil {
		t.Fatal("expected atomic finalization to reject an existing target")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "existing" {
		t.Fatalf("existing target was changed: %q, %v", data, err)
	}
	if data, err := os.ReadFile(temporary); err != nil || string(data) != "new" {
		t.Fatalf("temporary source was lost after collision: %q, %v", data, err)
	}
}

func TestExecuteRejectsSourceSwapBetweenPathCheckAndOpen(t *testing.T) {
	root := physicalTempDir(t)
	source := filepath.Join(root, "source", "book.m4b")
	original := source + ".original"
	target := filepath.Join(root, "library", "book.m4b")
	writeTestFile(t, source, "source")
	service := New(filepath.Join(root, "journals"))
	service.openSource = func(path string) (*os.File, error) {
		if err := os.Rename(path, original); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte("attack"), 0o600); err != nil {
			return nil, err
		}
		return os.Open(path)
	}

	result, err := service.Execute(context.Background(), domain.OperationPlan{
		Executable: true,
		TargetRoot: filepath.Join(root, "library"),
		Operations: []domain.PlannedOperation{{SourceRoot: filepath.Dir(source), Source: source, Target: target, Size: 6}},
	})
	if err == nil || result.Status != "failed" {
		t.Fatalf("expected swapped source to fail, got %#v, %v", result, err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target should not be published after a source swap: %v", statErr)
	}
	if data, readErr := os.ReadFile(original); readErr != nil || string(data) != "source" {
		t.Fatalf("original source should remain untouched: %q, %v", data, readErr)
	}
}

func TestExecuteRejectsSourceRootRedirectedAfterPlanning(t *testing.T) {
	root := physicalTempDir(t)
	boundRoot := filepath.Join(root, "incoming")
	displacedRoot := filepath.Join(root, "incoming-original")
	redirectRoot := filepath.Join(root, "redirect")
	source := filepath.Join(boundRoot, "book.m4b")
	target := filepath.Join(root, "library", "book.m4b")
	writeTestFile(t, source, "trusted")
	writeTestFile(t, filepath.Join(redirectRoot, "book.m4b"), "attack!")
	plan := domain.OperationPlan{
		Executable: true,
		TargetRoot: filepath.Join(root, "library"),
		Operations: []domain.PlannedOperation{{
			SourceRoot: boundRoot, Source: source, Target: target, Size: 7,
		}},
	}
	if err := os.Rename(boundRoot, displacedRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(redirectRoot, boundRoot); err != nil {
		t.Skipf("symlinks are not available in this test environment: %v", err)
	}

	result, err := New(filepath.Join(root, "journals")).Execute(context.Background(), plan)
	if err == nil || result.Status != "failed" || result.Completed != 0 {
		t.Fatalf("expected redirected source root to fail closed: %#v, %v", result, err)
	}
	if data, readErr := os.ReadFile(filepath.Join(displacedRoot, "book.m4b")); readErr != nil || string(data) != "trusted" {
		t.Fatalf("original source changed after root redirect: %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(filepath.Join(redirectRoot, "book.m4b")); readErr != nil || string(data) != "attack!" {
		t.Fatalf("redirected file changed: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target should not be created after root redirect: %v", statErr)
	}
}

func TestExecuteRejectsChangedSourceModificationTime(t *testing.T) {
	root := physicalTempDir(t)
	source := filepath.Join(root, "incoming", "book.m4b")
	target := filepath.Join(root, "library", "book.m4b")
	writeTestFile(t, source, "source")
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.OperationPlan{
		Executable: true,
		TargetRoot: filepath.Join(root, "library"),
		Operations: []domain.PlannedOperation{{
			SourceRoot: filepath.Dir(source), Source: source,
			SourceModifiedNanos: info.ModTime().UnixNano(), Target: target, Size: 6,
		}},
	}
	if err := os.WriteFile(source, []byte("attack"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedTime := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(source, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}

	result, err := New(filepath.Join(root, "journals")).Execute(context.Background(), plan)
	if err == nil || result.Status != "failed" || result.Completed != 0 {
		t.Fatalf("expected changed source timestamp to fail closed: %#v, %v", result, err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target should not be created for a changed source: %v", statErr)
	}
}

func TestExecuteRejectsSymlinkInTargetPath(t *testing.T) {
	root := physicalTempDir(t)
	source := filepath.Join(root, "source", "book.m4b")
	targetRoot := filepath.Join(root, "library")
	external := filepath.Join(root, "external")
	link := filepath.Join(targetRoot, "Author")
	writeTestFile(t, source, "audio")
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlinks are not available in this test environment: %v", err)
	}
	target := filepath.Join(link, "Book", "01 - Book.m4b")
	service := New(filepath.Join(root, "journals"))

	result, err := service.Execute(context.Background(), domain.OperationPlan{
		Executable: true,
		TargetRoot: targetRoot,
		Operations: []domain.PlannedOperation{{SourceRoot: filepath.Dir(source), Source: source, Target: target, Size: 5}},
	})
	if err == nil || result.Status != "failed" {
		t.Fatalf("expected symlinked target parent to fail, got %#v, %v", result, err)
	}
	if data, readErr := os.ReadFile(source); readErr != nil || string(data) != "audio" {
		t.Fatalf("source changed despite target preflight failure: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(external, "Book", "01 - Book.m4b")); !os.IsNotExist(statErr) {
		t.Fatalf("file escaped through target symlink: %v", statErr)
	}
}

func TestUndoRejectsChangedTarget(t *testing.T) {
	root := physicalTempDir(t)
	source := filepath.Join(root, "source.m4b")
	target := filepath.Join(root, "target.m4b")
	writeTestFile(t, source, "original")
	service := New(filepath.Join(root, "journals"))
	result, err := service.Execute(context.Background(), domain.OperationPlan{Executable: true, Operations: []domain.PlannedOperation{{
		SourceRoot: filepath.Dir(source), Source: source, Target: target, Size: 8,
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
	root := physicalTempDir(t)
	source := filepath.Join(root, "book", "folder.jpg")
	writeTestFile(t, source, "cover")
	service := New(filepath.Join(root, "config", "journals"))
	result, err := service.Execute(context.Background(), domain.OperationPlan{Executable: true, Operations: []domain.PlannedOperation{{
		Action: "remove", Category: "sidecar", SourceRoot: filepath.Dir(source), Source: source, Size: 5,
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

func TestDeduplicateQuarantinesSourceAndUndoRestoresIt(t *testing.T) {
	root := physicalTempDir(t)
	source := filepath.Join(root, "incoming", "book.m4b")
	targetRoot := filepath.Join(root, "library")
	existing := filepath.Join(targetRoot, "Author", "Book", "01 - Book.m4b")
	writeTestFile(t, source, "same audio")
	writeTestFile(t, existing, "same audio")
	service := New(filepath.Join(root, "config", "journals"))

	result, err := service.Execute(context.Background(), domain.OperationPlan{
		Executable: true,
		TargetRoot: targetRoot,
		Operations: []domain.PlannedOperation{{
			Action: "deduplicate", SourceRoot: filepath.Dir(source), Source: source, Target: existing, Size: 10,
		}},
	})
	if err != nil || result.Status != "completed" || result.Completed != 1 {
		t.Fatalf("unexpected deduplication result: %#v, %v", result, err)
	}
	if _, statErr := os.Stat(source); !os.IsNotExist(statErr) {
		t.Fatalf("duplicate source should be quarantined: %v", statErr)
	}
	if data, readErr := os.ReadFile(existing); readErr != nil || string(data) != "same audio" {
		t.Fatalf("existing target was changed: %q, %v", data, readErr)
	}
	journal, loadErr := service.load(result.JournalID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	operation := journal.Operations[0]
	if operation.Action != "deduplicate" || operation.DuplicateOf != existing || !strings.Contains(operation.Target, "quarantine") {
		t.Fatalf("deduplication was not safely journaled: %#v", operation)
	}
	if _, statErr := os.Stat(operation.Target); statErr != nil {
		t.Fatalf("quarantined source is missing: %v", statErr)
	}

	undo, undoErr := service.Undo(context.Background(), result.JournalID)
	if undoErr != nil || undo.Status != "undone" {
		t.Fatalf("deduplication undo failed: %#v, %v", undo, undoErr)
	}
	if data, readErr := os.ReadFile(source); readErr != nil || string(data) != "same audio" {
		t.Fatalf("duplicate source was not restored: %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(existing); readErr != nil || string(data) != "same audio" {
		t.Fatalf("existing target changed during undo: %q, %v", data, readErr)
	}
}

func TestDeduplicateMismatchStopsPlanBeforeAnyMove(t *testing.T) {
	root := physicalTempDir(t)
	firstSource := filepath.Join(root, "incoming", "first.mp3")
	duplicateSource := filepath.Join(root, "incoming", "duplicate.mp3")
	targetRoot := filepath.Join(root, "library")
	firstTarget := filepath.Join(targetRoot, "first.mp3")
	existing := filepath.Join(targetRoot, "duplicate.mp3")
	writeTestFile(t, firstSource, "first")
	writeTestFile(t, duplicateSource, "incoming")
	writeTestFile(t, existing, "different")
	service := New(filepath.Join(root, "journals"))

	result, err := service.Execute(context.Background(), domain.OperationPlan{
		Executable: true,
		TargetRoot: targetRoot,
		Operations: []domain.PlannedOperation{
			{SourceRoot: filepath.Dir(firstSource), Source: firstSource, Target: firstTarget, Size: 5},
			{Action: "deduplicate", SourceRoot: filepath.Dir(duplicateSource), Source: duplicateSource, Target: existing, Size: 8},
		},
	})
	if err == nil || result.Status != "failed" || result.Completed != 0 {
		t.Fatalf("expected mismatched deduplication to fail preflight: %#v, %v", result, err)
	}
	if data, readErr := os.ReadFile(firstSource); readErr != nil || string(data) != "first" {
		t.Fatalf("earlier source moved before deduplication preflight completed: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(firstTarget); !os.IsNotExist(statErr) {
		t.Fatalf("earlier target should not exist: %v", statErr)
	}
	if data, readErr := os.ReadFile(duplicateSource); readErr != nil || string(data) != "incoming" {
		t.Fatalf("mismatched duplicate source changed: %q, %v", data, readErr)
	}
}

func TestDeduplicateRechecksTargetImmediatelyBeforeQuarantine(t *testing.T) {
	root := physicalTempDir(t)
	source := filepath.Join(root, "incoming", "book.m4b")
	targetRoot := filepath.Join(root, "library")
	existing := filepath.Join(targetRoot, "book.m4b")
	writeTestFile(t, source, "same audio")
	writeTestFile(t, existing, "same audio")
	service := New(filepath.Join(root, "journals"))
	changed := false

	result, err := service.ExecuteWithProgress(context.Background(), domain.OperationPlan{
		Executable: true,
		TargetRoot: targetRoot,
		Operations: []domain.PlannedOperation{{
			Action: "deduplicate", SourceRoot: filepath.Dir(source), Source: source, Target: existing, Size: 10,
		}},
	}, func(update domain.ExecutionProgress) {
		if !changed && update.CompletedBytes > 0 {
			changed = true
			if writeErr := os.WriteFile(existing, []byte("new target"), 0o600); writeErr != nil {
				t.Fatalf("could not simulate changed duplicate target: %v", writeErr)
			}
		}
	})
	if err == nil || result.Status != "failed" {
		t.Fatalf("expected changed duplicate target to stop quarantine: %#v, %v", result, err)
	}
	if data, readErr := os.ReadFile(source); readErr != nil || string(data) != "same audio" {
		t.Fatalf("source was removed after duplicate target changed: %q, %v", data, readErr)
	}
}

func TestExecutePreflightsEverySourceBeforeMovingAnything(t *testing.T) {
	root := physicalTempDir(t)
	firstSource := filepath.Join(root, "source", "01.mp3")
	missingSource := filepath.Join(root, "source", "02.mp3")
	firstTarget := filepath.Join(root, "target", "01.mp3")
	writeTestFile(t, firstSource, "first")
	service := New(filepath.Join(root, "journals"))
	plan := domain.OperationPlan{Executable: true, Operations: []domain.PlannedOperation{
		{SourceRoot: filepath.Dir(firstSource), Source: firstSource, Target: firstTarget, Size: 5},
		{SourceRoot: filepath.Dir(missingSource), Source: missingSource, Target: filepath.Join(root, "target", "02.mp3"), Size: 6},
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

func TestExecutePreflightsEveryTargetBeforeMovingAnything(t *testing.T) {
	root := physicalTempDir(t)
	firstSource := filepath.Join(root, "source", "01.mp3")
	secondSource := filepath.Join(root, "source", "02.mp3")
	firstTarget := filepath.Join(root, "target", "01.mp3")
	conflictingTarget := filepath.Join(root, "target", "02.mp3")
	writeTestFile(t, firstSource, "first")
	writeTestFile(t, secondSource, "second")
	writeTestFile(t, conflictingTarget, "existing")
	service := New(filepath.Join(root, "journals"))
	plan := domain.OperationPlan{Executable: true, TargetRoot: filepath.Join(root, "target"), Operations: []domain.PlannedOperation{
		{SourceRoot: filepath.Dir(firstSource), Source: firstSource, Target: firstTarget, Size: 5},
		{SourceRoot: filepath.Dir(secondSource), Source: secondSource, Target: conflictingTarget, Size: 6},
	}}

	result, err := service.Execute(context.Background(), plan)
	if err == nil || result.Completed != 0 || result.Status != "failed" {
		t.Fatalf("expected target preflight failure, got %#v, %v", result, err)
	}
	if data, readErr := os.ReadFile(firstSource); readErr != nil || string(data) != "first" {
		t.Fatalf("first source changed before complete target preflight: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(firstTarget); !os.IsNotExist(statErr) {
		t.Fatalf("first target should not exist: %v", statErr)
	}
	if data, readErr := os.ReadFile(conflictingTarget); readErr != nil || string(data) != "existing" {
		t.Fatalf("conflicting target changed: %q, %v", data, readErr)
	}
}

func TestExecuteRejectsDuplicateTargetsBeforeMovingAnything(t *testing.T) {
	root := physicalTempDir(t)
	firstSource := filepath.Join(root, "source", "01.mp3")
	secondSource := filepath.Join(root, "source", "02.mp3")
	target := filepath.Join(root, "target", "Book.mp3")
	writeTestFile(t, firstSource, "first")
	writeTestFile(t, secondSource, "second")
	service := New(filepath.Join(root, "journals"))

	result, err := service.Execute(context.Background(), domain.OperationPlan{
		Executable: true,
		TargetRoot: filepath.Join(root, "target"),
		Operations: []domain.PlannedOperation{
			{SourceRoot: filepath.Dir(firstSource), Source: firstSource, Target: target, Size: 5},
			{SourceRoot: filepath.Dir(secondSource), Source: secondSource, Target: target, Size: 6},
		},
	})
	if err == nil || result.Completed != 0 || result.Status != "failed" {
		t.Fatalf("expected duplicate target preflight failure, got %#v, %v", result, err)
	}
	for path, expected := range map[string]string{firstSource: "first", secondSource: "second"} {
		if data, readErr := os.ReadFile(path); readErr != nil || string(data) != expected {
			t.Fatalf("source changed before duplicate target was rejected: %s: %q, %v", path, data, readErr)
		}
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("duplicate target should not be created: %v", statErr)
	}
}

func TestExecuteResolvesEquivalentUnicodeSourcePath(t *testing.T) {
	root := physicalTempDir(t)
	actualDirectory := filepath.Join(root, "Ungeku\u0308rzt")
	actualSource := filepath.Join(actualDirectory, "01 - Kapitel.mp3")
	plannedSource := filepath.Join(root, "Ungekürzt", "01 - Kapitel.mp3")
	target := filepath.Join(root, "target", "01 - Kapitel.mp3")
	writeTestFile(t, actualSource, "audio")
	service := New(filepath.Join(root, "journals"))
	result, err := service.Execute(context.Background(), domain.OperationPlan{Executable: true, Operations: []domain.PlannedOperation{{
		SourceRoot: actualDirectory, Source: plannedSource, Target: target, Size: 5,
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
	root := physicalTempDir(t)
	firstSource := filepath.Join(root, "source", "01.mp3")
	secondSource := filepath.Join(root, "source", "02.mp3")
	writeTestFile(t, firstSource, "first")
	writeTestFile(t, secondSource, "second")
	service := New(filepath.Join(root, "journals"))
	progress := make([]domain.ExecutionProgress, 0)
	result, err := service.ExecuteWithProgress(context.Background(), domain.OperationPlan{
		Executable: true,
		Operations: []domain.PlannedOperation{
			{SourceRoot: filepath.Dir(firstSource), Source: firstSource, Target: filepath.Join(root, "target", "01.mp3"), Size: 5},
			{SourceRoot: filepath.Dir(secondSource), Source: secondSource, Target: filepath.Join(root, "target", "02.mp3"), Size: 6},
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

func TestExecuteReportsBytesWithinOneLargeOperation(t *testing.T) {
	root := physicalTempDir(t)
	source := filepath.Join(root, "source", "book.m4b")
	target := filepath.Join(root, "target", "book.m4b")
	writeTestFile(t, source, "audio")
	const size = int64(20 * 1024 * 1024)
	if err := os.Truncate(source, size); err != nil {
		t.Fatal(err)
	}
	service := New(filepath.Join(root, "journals"))
	progress := make([]domain.ExecutionProgress, 0)
	result, err := service.ExecuteWithProgress(context.Background(), domain.OperationPlan{
		Executable: true,
		TargetRoot: filepath.Join(root, "target"),
		Operations: []domain.PlannedOperation{{SourceRoot: filepath.Dir(source), Source: source, Target: target, Size: size}},
	}, func(update domain.ExecutionProgress) {
		progress = append(progress, update)
	})
	if err != nil || result.Status != "completed" {
		t.Fatalf("unexpected execution result: %#v, %v", result, err)
	}
	intermediate := false
	for _, update := range progress {
		if update.Status == "moving" && update.Completed == 0 && update.CompletedBytes > 0 && update.CompletedBytes < size {
			intermediate = true
			break
		}
	}
	if !intermediate {
		t.Fatalf("expected byte-level progress during the operation, got %#v", progress)
	}
}

func TestExecutionLockExcludesAnotherService(t *testing.T) {
	root := physicalTempDir(t)
	journalDirectory := filepath.Join(root, "journals")
	first := New(journalDirectory)
	second := New(journalDirectory)
	lock, err := first.acquireExecutionLock("target:" + filepath.Join(root, "library"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.acquireExecutionLock("target:" + filepath.Join(root, "library")); err == nil {
		lock.Release()
		t.Fatal("expected another service to be excluded")
	}
	lock.Release()
	replacement, err := second.acquireExecutionLock("target:" + filepath.Join(root, "library"))
	if err != nil {
		t.Fatalf("released lock could not be reacquired: %v", err)
	}
	replacement.Release()
}

func TestValidateJournalRejectsTargetOutsideRoot(t *testing.T) {
	root := physicalTempDir(t)
	service := New(filepath.Join(root, "journals"))
	id := "20260731T000000Z-001122334455"
	journal := &Journal{
		Version: journalVersion,
		ID: id,
		Status: "completed",
		TargetRoot: filepath.Join(root, "library"),
		CreatedAt: service.now(),
		UpdatedAt: service.now(),
		Operations: []JournalOperation{{
			Action: "move",
			Source: filepath.Join(root, "source", "book.m4b"),
			Target: filepath.Join(root, "outside", "book.m4b"),
			Size: 1,
			SHA256: strings.Repeat("0", 64),
			Status: "completed",
		}},
	}
	if err := service.validateJournal(journal, id); err == nil {
		t.Fatal("expected journal target outside the recorded root to be rejected")
	}
}

func TestResultFromJournalRebuildsProposalOutcomes(t *testing.T) {
	journal := &Journal{
		ID: "journal",
		Status: "failed",
		Error: "transfer failed",
		Operations: []JournalOperation{
			{ProposalID: "first", Status: "completed", Size: 5},
			{ProposalID: "first", Status: "completed", Size: 6},
			{ProposalID: "second", Status: "failed", Size: 7},
			{ProposalID: "third", Status: "pending", Size: 8},
		},
	}
	result := resultFromJournal(journal)
	if strings.Join(result.ProposalIDs, ",") != "first,second,third" {
		t.Fatalf("unexpected proposal order: %#v", result.ProposalIDs)
	}
	if len(result.ProposalResults) != 3 {
		t.Fatalf("unexpected proposal results: %#v", result.ProposalResults)
	}
	first, second, third := result.ProposalResults[0], result.ProposalResults[1], result.ProposalResults[2]
	if first.Status != "completed" || first.Completed != 2 || first.Total != 2 {
		t.Fatalf("unexpected completed proposal result: %#v", first)
	}
	if second.Status != "failed" || second.Completed != 0 || second.Total != 1 || second.Error != journal.Error {
		t.Fatalf("unexpected failed proposal result: %#v", second)
	}
	if third.Status != "pending" || third.Completed != 0 || third.Total != 1 {
		t.Fatalf("unexpected pending proposal result: %#v", third)
	}
}

func TestHistorySurvivesServiceRestartAndTracksUndo(t *testing.T) {
	root := physicalTempDir(t)
	journalDirectory := filepath.Join(root, "journals")
	source := filepath.Join(root, "source", "book.m4b")
	target := filepath.Join(root, "library", "book.m4b")
	writeTestFile(t, source, "audio")
	service := New(journalDirectory)
	result, err := service.Execute(context.Background(), domain.OperationPlan{
		Executable: true, TargetRoot: filepath.Join(root, "library"),
		Operations: []domain.PlannedOperation{{SourceRoot: filepath.Dir(source), Source: source, Target: target, Size: 5}},
	})
	if err != nil {
		t.Fatal(err)
	}

	restarted := New(journalDirectory)
	snapshot, err := restarted.Result(result.JournalID)
	if err != nil || snapshot.JournalID != result.JournalID || snapshot.Status != "completed" || snapshot.Completed != 1 {
		t.Fatalf("unexpected persisted result: %#v, %v", snapshot, err)
	}
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

func physicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(root)
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
