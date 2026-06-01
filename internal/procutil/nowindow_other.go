//go:build !windows

// Package procutil provides cross-platform process creation helpers.
package procutil

import "os/exec"

// SetNoWindow is a no-op on non-Windows platforms.
func SetNoWindow(_ *exec.Cmd) {}
