//go:build windows

package fsutil

import (
	"syscall"
	"unsafe"
)

var (
	modkernel32         = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess     = modkernel32.NewProc("OpenProcess")
	procCloseHandle     = modkernel32.NewProc("CloseHandle")
	procGetExitCodeProcess = modkernel32.NewProc("GetExitCodeProcess")
)

const (
	processQueryInformation = 0x0400
	stillActive             = 259
)

func isProcessAlive(pid int) bool {
	handle, _, _ := procOpenProcess.Call(processQueryInformation, 0, uintptr(pid))
	if handle == 0 {
		return false
	}
	defer procCloseHandle.Call(handle)

	var exitCode uintptr
	ret, _, _ := procGetExitCodeProcess.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
	if ret == 0 {
		return false
	}
	return exitCode == stillActive
}
