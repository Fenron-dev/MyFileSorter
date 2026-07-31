//go:build !windows

package executor

import (
	"context"
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

func unsupportedNoReplace(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.ENOSYS) ||
		errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.EPERM)
}

func fallbackToCompatibleInstall(ctx context.Context, temporary, target string, renameErr error) (bool, error) {
	if !unsupportedNoReplace(renameErr) {
		return false, renameErr
	}
	if err := installFileViaHardlink(temporary, target); err == nil {
		return true, nil
	} else if !unsupportedNoReplace(err) {
		return false, fmt.Errorf("exklusives Umbenennen nicht unterstützt (%v); Hardlink-Rückfall fehlgeschlagen: %w", renameErr, err)
	} else if copyErr := installFileViaExclusiveCopy(ctx, temporary, target); copyErr != nil {
		return false, fmt.Errorf(
			"exklusives Umbenennen nicht unterstützt (%v); Hardlink-Rückfall nicht unterstützt (%v); exklusiver Kopier-Rückfall fehlgeschlagen: %w",
			renameErr,
			err,
			copyErr,
		)
	}
	return false, nil
}

func installFileViaExclusiveCopy(ctx context.Context, temporary, target string) error {
	pathInfo, err := os.Lstat(temporary)
	if err != nil {
		return err
	}
	if !pathInfo.Mode().IsRegular() {
		return fmt.Errorf("temporäre Datei ist keine reguläre Datei: %s", temporary)
	}

	source, err := os.Open(temporary)
	if err != nil {
		return err
	}
	defer source.Close()
	openedInfo, err := source.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		if err == nil {
			err = fmt.Errorf("temporäre Datei wurde beim Öffnen ausgetauscht: %s", temporary)
		}
		return err
	}

	published, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	publishedOpen := true
	defer func() {
		if publishedOpen {
			_ = published.Close()
		}
	}()
	publishedInfo, err := published.Stat()
	if err != nil {
		return err
	}
	removeIncomplete := true
	defer func() {
		if removeIncomplete {
			removePathIfSameFile(target, publishedInfo)
		}
	}()

	written, err := copyWithContext(ctx, published, source)
	if err != nil {
		return err
	}
	postSourceInfo, err := source.Stat()
	if err != nil || !sameStableFile(openedInfo, postSourceInfo) || written != openedInfo.Size() {
		if err == nil {
			err = fmt.Errorf("temporäre Datei wurde während des Kopierens verändert: %s", temporary)
		}
		return err
	}
	if err := published.Sync(); err != nil {
		return err
	}
	if publishedInfo.Mode().Perm() != openedInfo.Mode().Perm() {
		if err := published.Chmod(openedInfo.Mode().Perm()); err != nil && !unsupportedNoReplace(err) {
			return err
		}
	}
	if err := published.Close(); err != nil {
		return err
	}
	publishedOpen = false

	currentTarget, err := os.Lstat(target)
	if err != nil || !currentTarget.Mode().IsRegular() || !os.SameFile(publishedInfo, currentTarget) || currentTarget.Size() != written {
		if err == nil {
			err = fmt.Errorf("exklusiv kopiertes Ziel wurde ausgetauscht oder verändert: %s", target)
		}
		return err
	}
	currentSource, err := os.Lstat(temporary)
	if err != nil || !sameStableFile(openedInfo, currentSource) {
		if err == nil {
			err = fmt.Errorf("temporäre Datei wurde vor dem Entfernen ausgetauscht: %s", temporary)
		}
		return err
	}

	removeIncomplete = false
	_ = os.Remove(temporary)
	return nil
}

func removePathIfSameFile(path string, expected os.FileInfo) {
	current, err := os.Lstat(path)
	if err == nil && current.Mode().IsRegular() && os.SameFile(expected, current) {
		_ = os.Remove(path)
	}
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
