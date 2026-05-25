//go:build windows

package fsutil

import (
	"syscall"
	"unsafe"
)

var (
	modkernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess                = modkernel32.NewProc("OpenProcess")
	procCloseHandle                = modkernel32.NewProc("CloseHandle")
	procGetExitCodeProcess         = modkernel32.NewProc("GetExitCodeProcess")
	procQueryFullProcessImageNameW = modkernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

func isProcessAlive(pid int) bool {
	handle, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
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

// processExecutable returns the canonical executable path for pid. The boolean
// is false when the runtime cannot answer (no permission, or the process is
// already gone). Uses QueryFullProcessImageNameW with PROCESS_QUERY_LIMITED_INFORMATION
// so non-elevated callers can still query peer dreamer instances.
func processExecutable(pid int) (string, bool) {
	handle, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 {
		return "", false
	}
	defer procCloseHandle.Call(handle)

	buf := make([]uint16, syscall.MAX_PATH)
	size := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImageNameW.Call(
		handle,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == 0 {
		return "", false
	}
	return syscall.UTF16ToString(buf[:size]), true
}
