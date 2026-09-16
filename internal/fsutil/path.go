package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ExpandUserHome expands a leading ~ or ~/ into the user's home directory.
// Returns the original path unchanged if it does not start with ~.
func ExpandUserHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// CanonicalPath resolves ~/ and relative paths to an absolute, cleaned path.
// On Windows it lowercases the result so comparisons are case-insensitive.
// Does NOT evaluate symlinks — use ResolveSymlinks for that.
func CanonicalPath(p string) string {
	if p == "" {
		return ""
	}
	resolved, err := ExpandUserHome(p)
	if err != nil {
		resolved = p
	}
	if !filepath.IsAbs(resolved) {
		if abs, err := filepath.Abs(resolved); err == nil {
			resolved = abs
		}
	}
	resolved = filepath.Clean(resolved)
	if runtime.GOOS == "windows" {
		return strings.ToLower(resolved)
	}
	return resolved
}

// NormalizeRootPath returns the absolute, symlink-resolved, cleaned path.
// Rejects null bytes (security hardening). Empty input returns ("", nil).
// Symlink resolution errors are returned to the caller (fail-closed).
func NormalizeRootPath(root string) (string, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return "", nil
	}
	if strings.ContainsRune(trimmed, '\x00') {
		return "", fmt.Errorf("path contains null byte")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks for %q: %w", abs, err)
	}
	return filepath.Clean(resolved), nil
}

// ResolveSymlinks resolves abs, walking up to the deepest existing ancestor
// when the leaf does not exist. Non-ENOENT errors are returned.
func ResolveSymlinks(abs string) (string, error) {
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve symlinks for %q: %w", abs, err)
	}

	tail := []string{}
	currentPath := abs
	for {
		parent := filepath.Dir(currentPath)
		if parent == currentPath {
			return abs, nil
		}
		base := filepath.Base(currentPath)
		// Build path in reverse order to avoid prepend reallocation overhead.
		// Appending to slice is O(1) amortized, but prepending requires copying
		// all existing elements forward on each iteration - O(n) per prepend.
		tail = append(tail, base)
		resolvedParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			// Reconstruct path by walking backwards through tail since we appended (not prepended)
			out := resolvedParent
			for i := len(tail) - 1; i >= 0; i-- {
				out = filepath.Join(out, tail[i])
			}
			return filepath.Clean(out), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolve symlinks for ancestor %q of %q: %w", parent, abs, err)
		}
		currentPath = parent
	}
}

// PathWithinRoot reports whether path is within root (or equal to it).
// Both sides are canonicalized first: cleaned, made absolute, and resolved
// through symlinks (deepest existing ancestor for paths that do not exist
// yet), so a symlinked prefix on either side (macOS /var -> /private/var,
// Windows 8.3 short names, user symlinks inside a project root) cannot
// defeat or break containment. Case-insensitive on Windows.
// Returns false if either path or root is empty. Callers that intend
// "no restriction" when root is unset should branch before calling.
func PathWithinRoot(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	sep := string(filepath.Separator)
	canonical := func(p string) string {
		if !filepath.IsAbs(p) {
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
		}
		if resolved, err := ResolveSymlinks(p); err == nil {
			p = resolved
		} else {
			p = filepath.Clean(p)
		}
		if runtime.GOOS == "windows" {
			p = strings.ToLower(p)
		}
		return p
	}
	canonicalPath := canonical(path)
	canonicalRoot := canonical(root)
	return canonicalPath == canonicalRoot || strings.HasPrefix(canonicalPath, canonicalRoot+sep)
}
