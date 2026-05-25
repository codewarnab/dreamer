//go:build !windows

// Package sandbox no-op fallback for non-Windows platforms.
// Linux (bubblewrap) and macOS (Seatbelt) backends are planned for future PRs.
package sandbox

import "os/exec"

// Available reports whether the OS-level sandbox is supported.
// Currently only Windows is implemented.
func Available() bool { return false }

func prepare(cmd *exec.Cmd, cfg Config) error { return nil }

func postStart(cmd *exec.Cmd, cfg Config) error { return nil }
