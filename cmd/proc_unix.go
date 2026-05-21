//go:build !windows

package cmd

import "syscall"

// detachedProcessAttr returns SysProcAttr that detaches the child from the
// parent's terminal by creating a new session.
func detachedProcessAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// killDaemon sends SIGTERM to the daemon's process group. The negative PID
// targets the entire process group (the daemon is a session leader via Setsid).
func killDaemon(pid int) error {
	return syscall.Kill(-pid, syscall.SIGTERM)
}
