// Package gitutil centralizes safe git subprocess defaults.
package gitutil

import (
	"context"
	"os"
	"os/exec"
	"time"

	"dreamer/internal/procutil"
)

// CommandTimeout bounds local git metadata probes so slow filesystems or
// credential helpers cannot stall an entire Dreamer run.
const CommandTimeout = 10 * time.Second

// Command returns a git command with a timeout and terminal prompts disabled.
// The caller must call the returned cancel function after the command exits.
func Command(parent context.Context, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, CommandTimeout)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	// Git is a console-subsystem binary; without this every probe flashes a
	// console window when Dreamer runs detached (daemon/background jobs).
	procutil.SetNoWindow(cmd)
	return cmd, cancel
}
