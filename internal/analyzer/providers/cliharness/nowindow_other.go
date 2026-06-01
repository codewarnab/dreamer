//go:build !windows

package cliharness

import "os/exec"

func setNoWindow(_ *exec.Cmd) {} // no-op on non-Windows
