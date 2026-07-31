//go:build windows

package executor

import (
	"context"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

var (
	kernel32DLL     = syscall.NewLazyDLL("kernel32.dll")
	moveFileExWProc = kernel32DLL.NewProc("MoveFileExW")
)

func installFileNoReplace(ctx context.Context, temporary, target string) (bool, error) {
	// Without MOVEFILE_REPLACE_EXISTING, MoveFileEx fails atomically if the
	// destination appeared after preflight.
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := moveFileEx(temporary, target, moveFileWriteThrough); err != nil {
		return false, err
	}
	return true, nil
}

func replaceFile(temporary, target string) error {
	// MoveFileEx replaces the journal in one operation and avoids the former
	// delete-then-rename window that could leave no recovery record.
	return moveFileEx(temporary, target, moveFileReplaceExisting|moveFileWriteThrough)
}

func moveFileEx(fromPath, toPath string, flags uintptr) error {
	from, err := syscall.UTF16PtrFromString(fromPath)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(toPath)
	if err != nil {
		return err
	}
	result, _, callErr := moveFileExWProc.Call(
		uintptr(unsafe.Pointer(from)),
		uintptr(unsafe.Pointer(to)),
		flags,
	)
	if result != 0 {
		return nil
	}
	if callErr != nil && callErr != syscall.Errno(0) {
		return callErr
	}
	return fmt.Errorf("MoveFileExW fehlgeschlagen")
}

func syncDirectory(string) error {
	// MOVEFILE_WRITE_THROUGH above covers the file replacement. Opening and
	// flushing directory handles portably requires additional Windows APIs.
	return nil
}
