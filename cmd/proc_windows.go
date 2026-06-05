//go:build windows

package cmd

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// detachedProcessAttr returns SysProcAttr that detaches the child from the
// parent's console. DETACHED_PROCESS prevents the child from inheriting the
// console; CREATE_NEW_PROCESS_GROUP gives it its own group (enables Ctrl+C
// independence).
func detachedProcessAttr() *syscall.SysProcAttr {
	const detachedProcess = 0x00000008 // DETACHED_PROCESS — child gets no inherited console
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
	}
}

// suppressConsoleWindow detaches the current process from its console,
// closing the window. Used by background job commands that log to files.
func suppressConsoleWindow() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	if proc := kernel32.NewProc("FreeConsole"); proc.Find() == nil {
		// FreeConsole returns nonzero on success; ignore error — best-effort
		// cleanup and the caller has no actionable recovery.
		proc.Call() //nolint:errcheck
	}
}

// killDaemon uses taskkill to terminate the daemon and its entire process tree.
// /T kills child processes (exec.CommandContext on Windows only kills the direct
// child). /F forces termination since console apps don't receive WM_CLOSE.
// Returns nil if the process is already gone (taskkill exit code 128).
func killDaemon(pid int) error {
	out, err := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/T", "/F").CombinedOutput()
	if err != nil {
		msg := string(out)
		// taskkill exit code 128 = "process not found". Treat as success since
		// the process is already gone — a race between IsProcessAlive and here.
		if strings.Contains(msg, "not found") {
			return nil
		}
		if strings.Contains(msg, "Access is denied") {
			return fmt.Errorf("access denied killing PID %d: run as Administrator or use the same user session that started the daemon: %w", pid, err)
		}
		if len(out) > 0 {
			return fmt.Errorf("%s: %w", strings.TrimSpace(msg), err)
		}
		return err
	}
	return nil
}

// FindPIDByPort finds the PID of the process listening on the given TCP port.
func FindPIDByPort(port int) (int, error) {
	out, err := exec.Command("cmd", "/c", fmt.Sprintf("netstat -ano | findstr LISTENING | findstr :%d", port)).Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 5 {
			// Check if local address ends with :port
			localAddr := fields[1]
			if strings.HasSuffix(localAddr, fmt.Sprintf(":%d", port)) {
				pidStr := fields[len(fields)-1]
				var pid int
				if _, err := fmt.Sscan(pidStr, &pid); err == nil && pid > 0 {
					return pid, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("no process found listening on port %d", port)
}
