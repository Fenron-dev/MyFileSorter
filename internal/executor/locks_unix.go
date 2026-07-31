//go:build !windows

package executor

import (
	"errors"
	"syscall"
)

func processAlive(pid int) (bool, bool) {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return true, true
	case errors.Is(err, syscall.ESRCH):
		return false, true
	case errors.Is(err, syscall.EPERM):
		return true, true
	default:
		return false, false
	}
}
