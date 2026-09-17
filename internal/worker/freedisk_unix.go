//go:build !windows

package worker

import "syscall"

// freeDiskSpace retorna el espacio libre en bytes en el directorio dado.
// En Unix usa statfs (Bavail = bloques disponibles para usuarios no
// privilegiados).
func freeDiskSpace(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
