//go:build windows

package acpcore

import (
	"os/exec"

	"dreamer/internal/procutil"
)

// setNoWindow suppresses console window allocation for the child process.
// Also set by sandbox.Prepare when sandbox is active; this call covers
// the non-sandboxed execution path.
func setNoWindow(cmd *exec.Cmd) {
	procutil.SetNoWindow(cmd)
}
