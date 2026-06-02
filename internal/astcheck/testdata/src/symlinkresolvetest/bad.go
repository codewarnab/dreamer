package symlinkresolvetest

import (
	"path/filepath"
	"strings"
)

// filepath.Clean in containment check without symlink resolution — should be flagged.
func checkContainmentBad(userPath, root string) bool {
	cleaned := filepath.Clean(userPath)
	return strings.HasPrefix(cleaned, root) // want "filepath\\.Clean used in containment check without symlink resolution; call fsutil\\.ResolveSymlinks or filepath\\.EvalSymlinks first"
}

// Inline filepath.Clean in HasPrefix — should be flagged.
func checkContainmentBadInline(userPath, root string) bool {
	return strings.HasPrefix(filepath.Clean(userPath), root) // want "filepath\\.Clean used in containment check without symlink resolution; call fsutil\\.ResolveSymlinks or filepath\\.EvalSymlinks first"
}

// filepath.Clean with HasSuffix — should be flagged.
func checkContainmentBadSuffix(userPath, root string) bool {
	cleaned := filepath.Clean(userPath)
	return strings.HasSuffix(cleaned, root) // want "filepath\\.Clean used in containment check without symlink resolution; call fsutil\\.ResolveSymlinks or filepath\\.EvalSymlinks first"
}
