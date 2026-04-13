//go:build windows

package sync

import (
	"syscall"
	"unsafe"
)

// freeDiskBytes returns the number of bytes available to the current user
// on the volume containing path (Windows implementation).
func freeDiskBytes(path string) (uint64, error) {
	kernel32 := syscall.MustLoadDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.MustFindProc("GetDiskFreeSpaceExW")

	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var freeBytesAvailable uint64
	var totalBytes uint64
	var totalFreeBytes uint64

	r1, _, callErr := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if r1 == 0 {
		return 0, callErr
	}
	return freeBytesAvailable, nil
}
