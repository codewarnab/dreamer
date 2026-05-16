package chat

import (
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
	normalizedSeparators := strings.ReplaceAll(path, `\`, "/")
	cleanPath := filepath.Clean(filepath.FromSlash(normalizedSeparators))
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleanPath)
	}
	return cleanPath
}
