//go:build !windows

package sync

import "syscall"

// freeDiskBytes returns the number of bytes available to the current user
// on the filesystem containing path.
func freeDiskBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// Bavail = blocks available to unprivileged user
	return stat.Bavail * uint64(stat.Bsize), nil //nolint:unconvert
}
