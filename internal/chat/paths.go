package chat

import (
	"net/url"
	"path/filepath"
	"strings"

	"dreamer/internal/fsutil"
)

// decodeFileURI strips the file:// scheme and applies URL-decoding so
// percent-encoded characters round-trip correctly. On Windows, file:///C:/...
// has a leading slash before the drive letter — strip it. Returns ("", false)
// if percent-decoding fails.
func decodeFileURI(raw string) (string, bool) {
	stripped := strings.TrimPrefix(raw, "file://")
	decoded, err := url.PathUnescape(stripped)
	if err != nil {
		return "", false
	}
	// Windows: file:///C:/... -> /C:/... -> C:/...
	// Only strip when the second char is a drive letter (a-z or A-Z),
	// not for network paths like //server/share or Unix paths.
	if len(decoded) >= 3 && decoded[0] == '/' && decoded[2] == ':' &&
		((decoded[1] >= 'a' && decoded[1] <= 'z') || (decoded[1] >= 'A' && decoded[1] <= 'Z')) {
		decoded = decoded[1:]
	}
	return decoded, true
}

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
	return fsutil.PathWithinRoot(normalizedPath, normalizedRoot)
}

func normalizeDiscoveryPathForComparison(path string) string {
	return fsutil.CanonicalPath(path)
}
