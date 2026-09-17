//go:build windows

package worker

import (
	"fmt"
	"syscall"
	"unsafe"
)

// freeDiskSpace retorna el espacio libre en bytes en el directorio dado.
// En Windows usa GetDiskFreeSpaceEx. El archivo está taggeado (//go:build
// windows) porque syscall.NewLazyDLL solo existe en Windows: mantenerlo en
// worker.go rompía el build en Linux/Docker.
func freeDiskSpace(path string) (int64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes int64
	r1, _, err := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if r1 == 0 {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx failed: %v", err)
	}
	return freeBytesAvailable, nil
}
