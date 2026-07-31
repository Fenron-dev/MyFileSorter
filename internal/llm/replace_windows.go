//go:build windows

package llm

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

var (
	storeKernel32DLL     = syscall.NewLazyDLL("kernel32.dll")
	storeMoveFileExWProc = storeKernel32DLL.NewProc("MoveFileExW")
)

func replaceStoreFile(temporary, target string) error {
	from, err := syscall.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := storeMoveFileExWProc.Call(
		uintptr(unsafe.Pointer(from)),
		uintptr(unsafe.Pointer(to)),
		moveFileReplaceExisting|moveFileWriteThrough,
	)
	if result != 0 {
		return nil
	}
	if callErr != nil && callErr != syscall.Errno(0) {
		return callErr
	}
	return fmt.Errorf("MoveFileExW für AI-Profile fehlgeschlagen")
}
