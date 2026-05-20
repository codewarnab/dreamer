//go:build !linux && !windows

package fsutil

import "syscall"

func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

// processExecutable cannot answer on this OS — darwin, freebsd, etc. expose
// no portable /proc/<pid>/exe equivalent. Returning (\"\", false) makes the
// caller fall back to a PID-only liveness check; the PID-reuse-jam scenario
// PR #18 hardens against on Linux/Windows is therefore not fully closed here.
func processExecutable(_ int) (string, bool) {
	return "", false
}
