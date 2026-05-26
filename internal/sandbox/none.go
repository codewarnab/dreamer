//go:build !windows && !linux && !darwin

// Package sandbox no-op fallback for platforms without a sandbox backend.
// Linux uses bubblewrap (bwrap). Windows uses WRITE_RESTRICTED tokens.
package sandbox

import "os/exec"

// Available reports whether the OS-level sandbox is supported.
// Returns false — this build tag only applies to platforms without a backend.
func Available() bool { return false }

func prepare(cmd *exec.Cmd, cfg Config) (func(), error) { return func() {}, nil }

func postStart(cmd *exec.Cmd, cfg Config) (func(), error) { return func() {}, nil }
