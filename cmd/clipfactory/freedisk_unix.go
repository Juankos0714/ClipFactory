//go:build !windows

package main

import "syscall"

// freeDiskSpace retorna el espacio libre en bytes en el directorio dado.
// En Unix usa statfs (ver internal/worker/freedisk_unix.go).
func freeDiskSpace(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
