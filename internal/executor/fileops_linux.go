//go:build linux

package executor

import "golang.org/x/sys/unix"

func installFileNoReplace(temporary, target string) error {
	if err := unix.Renameat2(
		unix.AT_FDCWD,
		temporary,
		unix.AT_FDCWD,
		target,
		unix.RENAME_NOREPLACE,
	); err != nil {
		return fallbackToHardlink(temporary, target, err)
	}
	return nil
}
