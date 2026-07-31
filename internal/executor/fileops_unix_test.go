//go:build !windows

package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallFileViaExclusiveCopyPublishesWithoutHardlinks(t *testing.T) {
	root := physicalTempDir(t)
	temporary := filepath.Join(root, "transfer.part")
	target := filepath.Join(root, "book.m4b")
	writeTestFile(t, temporary, "complete audiobook")

	if err := installFileViaExclusiveCopy(context.Background(), temporary, target); err != nil {
		t.Fatalf("expected exclusive copy fallback to publish the file: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "complete audiobook" {
		t.Fatalf("published target is invalid: %q, %v", data, err)
	}
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary file should be removed after publication: %v", err)
	}
}

func TestInstallFileViaExclusiveCopyRejectsExistingTarget(t *testing.T) {
	root := physicalTempDir(t)
	temporary := filepath.Join(root, "transfer.part")
	target := filepath.Join(root, "book.m4b")
	writeTestFile(t, temporary, "new")
	writeTestFile(t, target, "existing")

	if err := installFileViaExclusiveCopy(context.Background(), temporary, target); err == nil {
		t.Fatal("expected exclusive copy fallback to reject an existing target")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "existing" {
		t.Fatalf("existing target was changed: %q, %v", data, err)
	}
	if data, err := os.ReadFile(temporary); err != nil || string(data) != "new" {
		t.Fatalf("temporary source was lost after collision: %q, %v", data, err)
	}
}

func TestInstallFileViaExclusiveCopyRemovesOwnedPartialTargetOnCancel(t *testing.T) {
	root := physicalTempDir(t)
	temporary := filepath.Join(root, "transfer.part")
	target := filepath.Join(root, "book.m4b")
	writeTestFile(t, temporary, "complete audiobook")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := installFileViaExclusiveCopy(ctx, temporary, target); err == nil {
		t.Fatal("expected canceled exclusive copy fallback to fail")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("owned partial target should be removed after cancellation: %v", err)
	}
	if data, err := os.ReadFile(temporary); err != nil || string(data) != "complete audiobook" {
		t.Fatalf("temporary source was lost after cancellation: %q, %v", data, err)
	}
}
