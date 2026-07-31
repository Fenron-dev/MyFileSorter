//go:build !windows

package executor

import (
	"errors"
	"os"
	"syscall"
)

func installFileNoReplace(temporary, target string) error {
	// A hard link is an atomic create-if-absent operation. Both files live in
	// the same directory/filesystem, so a successful link publishes the fully
	// synced inode without the overwrite semantics of os.Rename on Unix.
	if err := os.Link(temporary, target); err != nil {
		return err
	}
	// The target is already durable and complete. Failure to remove the private
	// temporary name must not turn a successful installation into an untracked
	// failed operation; the caller and startup cleanup can retry it.
	_ = os.Remove(temporary)
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
