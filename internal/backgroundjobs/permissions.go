package backgroundjobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxPromptSize is the maximum allowed byte length for a background job prompt.
// This constant is intentionally placed here (not in cmd/ or handlers/) so both
// the CLI and the web handler can import it without creating an import cycle.
// Value: 16 KiB — generous for any realistic natural-language task description.
const MaxPromptSize = 16 * 1024

// ParseFileAccess converts the user-facing string representation of a
// FileAccessMode into the typed constant, returning an error for unknown values.
// Centralising this here ensures that the CLI (cmd/jobs.go) and the web
// handlers (internal/web/handlers/jobs.go) always enforce the same allowed set
// and error message.
func ParseFileAccess(s string) (FileAccessMode, error) {
	switch s {
	case "read_only":
		return FileAccessReadOnly, nil
	case "selected_writes":
		return FileAccessSelectedWrites, nil
	case "full_workspace":
		return FileAccessFullWorkspace, nil
	default:
		return "", fmt.Errorf("file_access must be one of: read_only, selected_writes, full_workspace")
	}
}

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
	// Canonicalize the root the same way candidate paths are resolved below.
	// On macOS, t.TempDir()-style roots under /var are symlinks to
	// /private/var; without this, a resolved candidate never carries the
	// unresolved root's prefix and every containment check fails.
	if resolvedRoot, err := resolveAncestorSymlink(cleanRoot); err == nil {
		cleanRoot = resolvedRoot
	}
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

	// Check protected suffixes anywhere in the path (not just root prefix).
	rel, err := filepath.Rel(projectRoot, p)
	if err != nil {
		return fmt.Errorf("path %q: cannot compute relative path: %w", p, err)
	}
	for _, comp := range strings.Split(filepath.ToSlash(rel), "/") {
		for _, suffix := range protectedSuffixes {
			if comp == suffix {
				return fmt.Errorf("path %q targets protected directory %q", p, suffix)
			}
		}
	}

	// Resolve symlinks through deepest existing ancestor.
	resolved, err := resolveAncestorSymlink(p)
	if err != nil {
		return fmt.Errorf("resolve symlinks for %q: %w", p, err)
	}

	// Check containment after resolution.
	if !strings.HasPrefix(filepath.Clean(resolved)+string(filepath.Separator), projectRoot+string(filepath.Separator)) && resolved != projectRoot {
		return fmt.Errorf("path %q resolves outside project root to %q", p, resolved)
	}

	return nil
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
