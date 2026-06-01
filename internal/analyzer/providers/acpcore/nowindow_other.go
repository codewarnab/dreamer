//go:build !windows

package acpcore

import "os/exec"

func setNoWindow(_ *exec.Cmd) {} // no-op on non-Windows
