//go:build linux

// Linux sandbox using bubblewrap (bwrap). The entire host filesystem is
// mounted read-only via --ro-bind / /, with selective --bind for writable
// directories. User and PID namespaces isolate the child process.
//
// File layout:
//
//	linux.go        — Available(), prepare(), postStart()
//	linux_bwrap.go  — bwrapPath(), userNamespacesEnabled(), isWSL1(), buildBwrapArgs()
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Available reports whether bubblewrap sandboxing is supported on this
// system. Returns true only when bwrap is in PATH, user namespaces are
// enabled, and the host is not WSL1 (which lacks user namespace support).
func Available() bool {
	return bwrapPath() != "" && userNamespacesEnabled() && !isWSL1()
}

// prepare wraps cmd with bwrap to sandbox the child process. The original
// binary and args are preserved after the "--" separator in the bwrap
// argument list. cmd.Path and cmd.Args are modified in-place.
//
// Returns a no-op cleanup — bwrap handles its own lifecycle via
// --die-with-parent and --unshare-pid.
func prepare(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	projectDir, err := filepath.Abs(cfg.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("sandbox: resolve project dir: %w", err)
	}
	projectDir, err = filepath.EvalSymlinks(projectDir)
	if err != nil {
		return nil, fmt.Errorf("sandbox: resolve symlinks in project dir: %w", err)
	}

	// Ensure each writable directory exists before bwrap bind-mounts it.
	for _, wdir := range cfg.WritableDirs {
		absDir, err := filepath.Abs(wdir)
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve writable dir: %w", err)
		}
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			return nil, fmt.Errorf("sandbox: create writable dir %s: %w", absDir, err)
		}
	}

	bwrapBinPath := bwrapPath()
	originalBinary := cmd.Path
	var originalArgs []string
	if len(cmd.Args) > 1 {
		originalArgs = cmd.Args[1:]
	}

	cmd.Path = bwrapBinPath
	cmd.Args = append(
		[]string{bwrapBinPath},
		buildBwrapArgs(cfg, projectDir, originalBinary, originalArgs)...,
	)

	return func() {}, nil
}

// postStart is a no-op on Linux. Unlike Windows (where Job Objects manage
// child lifecycle), bwrap's --die-with-parent and --unshare-pid ensure all
// descendants are killed when the parent exits. No kernel handles to release.
func postStart(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	return func() {}, nil
}
