//go:build linux

package executor

import (
	"context"

	"golang.org/x/sys/unix"
)

func installFileNoReplace(ctx context.Context, temporary, target string) (bool, error) {
	if err := unix.Renameat2(
		unix.AT_FDCWD,
		temporary,
		unix.AT_FDCWD,
		target,
		unix.RENAME_NOREPLACE,
	); err != nil {
		return fallbackToCompatibleInstall(ctx, temporary, target, err)
	}
	return true, nil
}
