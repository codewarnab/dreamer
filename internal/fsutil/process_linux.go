//go:build linux

package fsutil

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

// processExecutable returns the canonical executable path for pid. The boolean
// is false when the runtime cannot answer — typically because /proc/<pid>/exe
// is not readable (different uid) or the kernel does not expose it. Callers
// should fall back to a PID-only liveness check in that case.
func processExecutable(pid int) (string, bool) {
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return "", false
	}
	// When a running process's binary is replaced in-place (the normal
	// upgrade path), the kernel appends " (deleted)" to the readlink
	// result. Strip it so ExecPathsMatch can compare against the clean
	// path recorded in the lock file.
	target = strings.TrimSuffix(target, " (deleted)")
	return target, true
}
