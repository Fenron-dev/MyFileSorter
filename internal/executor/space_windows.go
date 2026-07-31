//go:build windows

package executor

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	getDiskFreeSpaceExWProc = kernel32DLL.NewProc("GetDiskFreeSpaceExW")
	getVolumePathNameWProc  = kernel32DLL.NewProc("GetVolumePathNameW")
)

func availableDiskBytes(path string) (uint64, bool, error) {
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false, err
	}
	var available uint64
	result, _, callErr := getDiskFreeSpaceExWProc.Call(
		uintptr(unsafe.Pointer(pathPointer)),
		uintptr(unsafe.Pointer(&available)),
		0,
		0,
	)
	if result != 0 {
		return available, true, nil
	}
	if callErr != nil && callErr != syscall.Errno(0) {
		return 0, false, callErr
	}
	return 0, false, fmt.Errorf("GetDiskFreeSpaceExW fehlgeschlagen")
}

func diskSpaceKey(path string) (string, error) {
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, 32768)
	result, _, callErr := getVolumePathNameWProc.Call(
		uintptr(unsafe.Pointer(pathPointer)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
	)
	if result != 0 {
		return "volume:" + strings.ToLower(filepath.Clean(syscall.UTF16ToString(buffer))), nil
	}
	if callErr != nil && callErr != syscall.Errno(0) {
		return "", callErr
	}
	return "", fmt.Errorf("GetVolumePathNameW fehlgeschlagen")
}
