//go:build darwin

// macOS sandbox using Apple's Seatbelt framework via sandbox-exec.
// The profile uses (allow default) as the base policy with selective
// (deny file-write*) and (deny file-link) for project directory protection.
//
// DEPRECATION NOTE: Apple has deprecated sandbox-exec and it may be removed
// in a future macOS release. If sandbox-exec becomes unavailable, the
// available() check will return false and callers will fall back to the
// provider's native sandbox (if any) or run unsandboxed. A future migration
// to EndpointSecurity or App Sandbox entitlements would require a signed
// helper binary, which is out of scope for the current implementation.
//
// File layout:
//
//	darwin.go           — Available(), prepare(), resolveWritableDirs(), postStart()
//	darwin_seatbelt.go  — buildSeatbeltProfile(), buildSandboxArgs(), validateSBPLPath(), sandboxExecLocator()
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Available reports whether sandbox-exec is present on this system.
func Available() bool {
	return sandboxExecLocator() != ""
}

// resolveWritableDirs validates, creates, deduplicates, and caps writable dirs.
// This is the single source of truth for writable dir canonicalization.
// Returns an error if any dir is invalid or overlaps with projectDir.
func resolveWritableDirs(projectDir string, dirs []string) ([]string, error) {
	resolved := make([]string, 0, len(dirs))
	seen := make(map[string]bool)

	// Canonicalize the project dir so overlap checks compare real paths.
	// Temp-dir style paths may traverse symlinks (/var -> /private/var on
	// macOS); without resolution a writable dir could alias the project dir
	// and slip past the overlap rejection below.
	if projectDir != "" {
		absProj, err := filepath.Abs(projectDir)
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve project dir %q: %w", projectDir, err)
		}
		canonicalProj, err := filepath.EvalSymlinks(absProj)
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve symlinks in project dir %q: %w", absProj, err)
		}
		projectDir = canonicalProj
	}

	for _, wdir := range dirs {
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
		// Create the directory BEFORE resolving symlinks. This ensures
		// EvalSymlinks succeeds for paths whose ancestors exist but the
		// leaf does not yet, and that the resolved path is correct even
		// when the leaf was a symlink created by MkdirAll.
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			return nil, fmt.Errorf("sandbox: create writable dir %s: %w", absDir, err)
		}
		// Resolve symlinks AFTER creating the directory. This is now
		// symmetric with projectDir resolution — both require full
		// EvalSymlinks success, preventing the overlap-check bypass
		// where an unresolved symlink in the writable path's ancestry
		// could hide overlap with the fully-resolved project dir.
		canonical, err := filepath.EvalSymlinks(absDir)
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve symlinks in writable dir %q: %w", absDir, err)
		}
		// Reject overlap: writable dir must not contain or equal project dir.
		if projectDir != "" {
			if canonical == projectDir || pathContains(canonical, projectDir) {
				return nil, fmt.Errorf("sandbox: writable dir %s contains project dir %s", canonical, projectDir)
			}
		}
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		resolved = append(resolved, canonical)

		if len(resolved) >= maxWritableDirs {
			break
		}
	}

	return resolved, nil
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

	// Validate and resolve writable dirs — single source of truth.
	writableDirs, err := resolveWritableDirs(projectDir, cfg.WritableDirs)
	if err != nil {
		return nil, err
	}

	// Build profile and args.
	profile := buildSeatbeltProfile(cfg, writableDirs)
	originalBinary := cmd.Path
	var originalArgs []string
	if len(cmd.Args) > 1 {
		originalArgs = cmd.Args[1:]
	}

	cmd.Path = sandboxExecLocator()
	cmd.Args = buildSandboxArgs(profile, writableDirs, originalBinary, originalArgs)

	return func() {}, nil
}

// postStart is a no-op on macOS. Seatbelt sandboxing is process-inherited
// and kernel-managed. No kernel handles to release.
func postStart(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	return func() {}, nil
}

// postStartWithHandle is a no-op on macOS. Returns 0 for the handle.
func postStartWithHandle(cmd *exec.Cmd, cfg Config) (uintptr, func(), error) {
	return 0, func() {}, nil
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
