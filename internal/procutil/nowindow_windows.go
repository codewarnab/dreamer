//go:build windows

// Package procutil provides cross-platform process creation helpers.
package procutil

import (
	"os/exec"
	"syscall"
)

// CreateNoWindow is the Windows CREATE_NO_WINDOW process creation flag.
// It prevents console window allocation for headless child processes.
// Safe for all CLI providers — they communicate via anonymous pipes,
// not console handles. The Codex sandbox Rust code uses the same flag.
const CreateNoWindow = 0x08000000

// SetNoWindow sets CREATE_NO_WINDOW on cmd.SysProcAttr to suppress
// the console window that Windows would otherwise allocate for the
// child process. Idempotent — safe to call when sandbox.Prepare also
// sets the same flag via |=.
func SetNoWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= CreateNoWindow
}
