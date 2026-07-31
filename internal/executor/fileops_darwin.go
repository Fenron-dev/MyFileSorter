//go:build darwin

package executor

import (
	"context"

	"golang.org/x/sys/unix"
)

func installFileNoReplace(ctx context.Context, temporary, target string) (bool, error) {
	// RENAME_EXCL asks the mounted filesystem (including capable SMB/NAS
	// volumes) to publish the completed temporary file only if the final name
	// is still unused. Unlike the former hardlink-only implementation, this
	// does not require Unix hardlink support on the share.
	if err := unix.RenamexNp(temporary, target, unix.RENAME_EXCL); err != nil {
		return fallbackToCompatibleInstall(ctx, temporary, target, err)
	}
	return true, nil
}
