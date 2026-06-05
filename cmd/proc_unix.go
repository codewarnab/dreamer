//go:build !windows

package cmd

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// detachedProcessAttr returns SysProcAttr that detaches the child from the
// parent's terminal by creating a new session.
func detachedProcessAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// suppressConsoleWindow is a no-op on non-Windows platforms.
func suppressConsoleWindow() {}

// killDaemon sends SIGTERM to the daemon's process group. The negative PID
// targets the entire process group when the daemon is a session leader via
// Setsid. Standalone servers discovered by port may not own a process group
// with the same ID, so fall back to signaling the process itself.
func killDaemon(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		return syscall.Kill(pid, syscall.SIGTERM)
	}
	return nil
}

// FindPIDByPort finds the PID of the process listening on the given TCP port.
func FindPIDByPort(port int) (int, error) {
	// Try lsof first
	out, err := exec.Command("lsof", "-t", "-i", fmt.Sprintf(":%d", port)).Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 && lines[0] != "" {
			var pid int
			if _, err := fmt.Sscan(lines[0], &pid); err == nil && pid > 0 {
				return pid, nil
			}
		}
	}
	// Try fuser as fallback
	out, err = exec.Command("fuser", fmt.Sprintf("%d/tcp", port)).Output()
	if err == nil {
		fields := strings.Fields(string(out))
		if len(fields) > 0 {
			var pid int
			if _, err := fmt.Sscan(fields[0], &pid); err == nil && pid > 0 {
				return pid, nil
			}
		}
	}
	return 0, fmt.Errorf("no process found listening on port %d", port)
}
