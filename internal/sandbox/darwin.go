//go:build darwin

// macOS sandbox using Apple's Seatbelt framework via sandbox-exec.
// The profile uses (allow default) as the base policy with selective
// (deny file-write*) and (deny file-link) for project directory protection.
//
// File layout:
//
//	darwin.go           — Available(), prepare(), postStart()
//	darwin_seatbelt.go  — buildSeatbeltProfile(), buildSandboxArgs(), validateSBPLPath()
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Available reports whether sandbox-exec is present on this system.
func Available() bool {
	_, err := os.Stat(sandboxExecPath)
	return err == nil
}

// prepare wraps cmd with sandbox-exec to apply a Seatbelt profile. The
// original binary and args are preserved after the "--" separator. cmd.Path
// and cmd.Args are modified in-place.
//
// Returns a no-op cleanup — Seatbelt is self-cleaning (process-inherited,
// kernel-managed, dies with the process tree).
func prepare(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	// Resolve project dir. Empty is allowed (ACP providers).
	var projectDir string
	if cfg.ProjectDir != "" {
		if err := validateSBPLPath(cfg.ProjectDir); err != nil {
			return nil, err
		}
		absDir, err := filepath.Abs(cfg.ProjectDir)
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve project dir %q: %w", cfg.ProjectDir, err)
		}
		resolved, err := filepath.EvalSymlinks(absDir)
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve symlinks in project dir: %w", err)
		}
		projectDir = resolved
	}

	// Validate and resolve writable dirs.
	resolvedDirs := make([]string, 0, len(cfg.WritableDirs))
	seen := make(map[string]bool)
	for _, wdir := range cfg.WritableDirs {
		if wdir == "" {
			continue
		}
		if err := validateSBPLPath(wdir); err != nil {
			return nil, err
		}
		absDir, err := filepath.Abs(wdir)
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve writable dir %q: %w", wdir, err)
		}
		resolved, err := filepath.EvalSymlinks(absDir)
		if err != nil {
			resolved = absDir
		}
		// Reject overlap: writable dir must not contain or equal project dir.
		if projectDir != "" {
			if resolved == projectDir || pathContains(resolved, projectDir) {
				return nil, fmt.Errorf("sandbox: writable dir %s contains project dir %s", resolved, projectDir)
			}
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		// Ensure directory exists.
		if err := os.MkdirAll(resolved, 0o755); err != nil {
			return nil, fmt.Errorf("sandbox: create writable dir %s: %w", resolved, err)
		}
		resolvedDirs = append(resolvedDirs, resolved)
	}

	// Build profile and args.
	profile := buildSeatbeltProfile(resolvedDirs)
	originalBinary := cmd.Path
	var originalArgs []string
	if len(cmd.Args) > 1 {
		originalArgs = cmd.Args[1:]
	}

	cmd.Path = sandboxExecPath
	cmd.Args = buildSandboxArgs(profile, resolvedDirs, originalBinary, originalArgs)

	return func() {}, nil
}

// postStart is a no-op on macOS. Seatbelt sandboxing is process-inherited
// and kernel-managed. No kernel handles to release.
func postStart(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	return func() {}, nil
}

// pathContains reports whether child is under parent (or equal to parent).
// Both paths must be absolute and cleaned.
func pathContains(parent, child string) bool {
	if parent == child {
		return true
	}
	// Ensure trailing separator for prefix match to avoid /foo matching /foobar.
	p := parent
	if p[len(p)-1] != '/' {
		p += "/"
	}
	return len(child) > len(p) && child[:len(p)] == p
}
