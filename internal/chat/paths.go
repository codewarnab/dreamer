package chat

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func normalizeDiscoveryPath(raw string) (string, bool) {
	return normalizeDiscoveryPathWithOptions(raw, false)
}

func normalizeDiscoveryEvidencePath(raw string) (string, bool) {
	return normalizeDiscoveryPathWithOptions(raw, true)
}

func normalizeDiscoveryPathWithOptions(raw string, requireAbsolute bool) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}

	normalizedSeparators := strings.ReplaceAll(trimmed, `\`, "/")
	path := filepath.Clean(filepath.FromSlash(normalizedSeparators))
	if requireAbsolute && !filepath.IsAbs(path) {
		return "", false
	}

	absolutePath := path
	if !filepath.IsAbs(absolutePath) {
		var err error
		absolutePath, err = filepath.Abs(path)
		if err != nil {
			return "", false
		}
	}

	cleanPath := filepath.Clean(absolutePath)
	resolvedPath, err := filepath.EvalSymlinks(cleanPath)
	if err == nil {
		cleanPath = filepath.Clean(resolvedPath)
	}

	return cleanPath, true
}

func pathWithinNormalizedRoot(path string, root string) bool {
	normalizedPath := normalizeDiscoveryPathForComparison(path)
	normalizedRoot := normalizeDiscoveryPathForComparison(root)
	if normalizedPath == "" || normalizedRoot == "" {
		return false
	}

	if normalizedPath == normalizedRoot {
		return true
	}

	rootWithSeparator := normalizedRoot
	if !strings.HasSuffix(rootWithSeparator, string(filepath.Separator)) {
		rootWithSeparator += string(filepath.Separator)
	}
	return strings.HasPrefix(normalizedPath, rootWithSeparator)
}

func normalizeDiscoveryPathForComparison(path string) string {
	return CanonicalPath(path)
}

// CanonicalPath resolves ~/ and relative paths to an absolute, cleaned
// path. On Windows it lowercases the result so comparisons are
// case-insensitive. This is the single source of truth for path
// normalization used by both discovery and the add command.
func CanonicalPath(p string) string {
	resolved := p
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				resolved = home
			} else {
				resolved = filepath.Join(home, p[2:])
			}
		}
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
