//go:build !windows

package fsutil

import (
	"fmt"
	"os"
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
	return target, true
}
