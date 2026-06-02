package backgroundjobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// protectedSuffixes are directory names that background jobs must never write
// to, even when the project root is broad.
var protectedSuffixes = []string{
	".dreamer",
	".git",
	".claude",
	".codex",
	".copilot",
	".gemini",
}

// ValidateWritablePaths checks that every path in writablePaths resolves
// inside projectRoot after symlink resolution, and does not target protected
// Dreamer/provider/scheduler paths.
func ValidateWritablePaths(projectRoot string, writablePaths []string) error {
	cleanRoot := filepath.Clean(projectRoot)
	for _, p := range writablePaths {
		if err := validateSingleWritablePath(cleanRoot, p); err != nil {
			return err
		}
	}
	return nil
}

func validateSingleWritablePath(projectRoot, p string) error {
	if !filepath.IsAbs(p) {
		p = filepath.Join(projectRoot, p)
	}
	p = filepath.Clean(p)

	// Reject protected directories named literally in the requested path.
	if suffix := protectedComponent(projectRoot, p); suffix != "" {
		return fmt.Errorf("path %q targets protected directory %q", p, suffix)
	}

	// Resolve symlinks through deepest existing ancestor.
	resolved, err := resolveAncestorSymlink(p)
	if err != nil {
		return fmt.Errorf("resolve symlinks for %q: %w", p, err)
	}
	resolved = filepath.Clean(resolved)

	// Re-check protected directories on the resolved path. A symlink with an
	// innocent name (e.g. "notes" -> ".git") passes the literal check above
	// but must not grant write access to a protected directory it points at.
	if suffix := protectedComponent(projectRoot, resolved); suffix != "" {
		return fmt.Errorf("path %q resolves to protected directory %q", p, suffix)
	}

	// Check containment after resolution.
	if !strings.HasPrefix(resolved+string(filepath.Separator), projectRoot+string(filepath.Separator)) && resolved != projectRoot {
		return fmt.Errorf("path %q resolves outside project root to %q", p, resolved)
	}

	return nil
}

// protectedComponent returns the first protected directory name that appears
// as a path component of p relative to projectRoot, or "" if none does. A path
// outside projectRoot (rel computation fails or escapes) is treated as having
// no protected component here; containment is enforced separately by the caller.
func protectedComponent(projectRoot, p string) string {
	rel, err := filepath.Rel(projectRoot, p)
	if err != nil {
		return ""
	}
	for _, comp := range strings.Split(filepath.ToSlash(rel), "/") {
		for _, suffix := range protectedSuffixes {
			if comp == suffix {
				return suffix
			}
		}
	}
	return ""
}

// resolveAncestorSymlink resolves symlinks through the deepest existing
// ancestor directory. If the full path exists, EvalSymlinks on it.
// Otherwise, walk up until we find an existing ancestor, resolve that,
// then rejoin the remaining components.
func resolveAncestorSymlink(p string) (string, error) {
	// Try full path first.
	if _, err := os.Stat(p); err == nil {
		return filepath.EvalSymlinks(p)
	}

	// Walk up to find existing ancestor.
	parent := filepath.Dir(p)
	base := filepath.Base(p)
	parts := []string{base}

	// Guard: on Windows, filepath.Dir("C:\\") returns "C:\\" again, so
	// check VolumeName equality to avoid an infinite loop on drive roots.
	for parent != string(filepath.Separator) && parent != "." && parent != filepath.VolumeName(parent) {
		if _, err := os.Stat(parent); err == nil {
			resolved, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", err
			}
			// Rejoin remaining parts in reverse.
			for i := len(parts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, parts[i])
			}
			return resolved, nil
		}
		parts = append([]string{filepath.Base(parent)}, parts...)
		parent = filepath.Dir(parent)
	}

	// Fallback: no existing ancestor found, return cleaned original.
	return filepath.Clean(p), nil
}
