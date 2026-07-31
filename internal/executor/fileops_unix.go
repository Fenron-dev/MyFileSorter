//go:build !windows

package executor

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func installFileViaHardlink(temporary, target string) error {
	// A hard link is an atomic create-if-absent operation. Both files live in
	// the same directory/filesystem, so it remains a safe fallback where the
	// platform's exclusive-rename primitive is unavailable.
	if err := os.Link(temporary, target); err != nil {
		return err
	}
	// The target is already durable and complete. Failure to remove the private
	// temporary name must not turn a successful installation into an untracked
	// failed operation; the caller and startup cleanup can retry it.
	_ = os.Remove(temporary)
	return nil
}

func fallbackToHardlink(temporary, target string, renameErr error) error {
	if !errors.Is(renameErr, syscall.ENOTSUP) &&
		!errors.Is(renameErr, syscall.ENOSYS) &&
		!errors.Is(renameErr, syscall.EINVAL) {
		return renameErr
	}
	if err := installFileViaHardlink(temporary, target); err != nil {
		return fmt.Errorf("exklusives Umbenennen nicht unterstützt (%v); Hardlink-Rückfall fehlgeschlagen: %w", renameErr, err)
	}
	return nil
}

func replaceFile(temporary, target string) error {
	return os.Rename(temporary, target)
}

func syncDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer handle.Close()
	err = handle.Sync()
	// Some otherwise safe filesystems do not implement directory fsync. File
	// data and the atomic publish are still durable to the extent that the
	// filesystem supports them; treat only this explicit capability gap as a
	// best-effort boundary.
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}
