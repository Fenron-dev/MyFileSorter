//go:build !windows && !darwin && !linux

package executor

import (
	"context"
	"fmt"
)

func installFileNoReplace(ctx context.Context, temporary, target string) (bool, error) {
	if err := installFileViaHardlink(temporary, target); err == nil {
		return true, nil
	} else if !unsupportedNoReplace(err) {
		return false, err
	} else if copyErr := installFileViaExclusiveCopy(ctx, temporary, target); copyErr != nil {
		return false, fmt.Errorf("Hardlink-Veröffentlichung nicht unterstützt (%v); exklusiver Kopier-Rückfall fehlgeschlagen: %w", err, copyErr)
	}
	return false, nil
}
