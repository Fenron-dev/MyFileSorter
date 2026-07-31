//go:build !windows

package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func availableDiskBytes(path string) (uint64, bool, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0, false, err
	}
	return uint64(stats.Bavail) * uint64(stats.Bsize), true, nil
}

func diskSpaceKey(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	stats, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "path:" + filepath.Clean(path), nil
	}
	return fmt.Sprintf("device:%d", stats.Dev), nil
}
