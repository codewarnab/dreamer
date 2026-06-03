package symlinkresolvetest

import (
	"os"
	"path/filepath"
	"strings"

	"fsutil"
)

// Correct: ResolveSymlinks before filepath.Clean.
func checkContainmentGood(userPath, root string) (bool, error) {
	resolved, err := fsutil.ResolveSymlinks(userPath)
	if err != nil {
		return false, err
	}
	cleaned := filepath.Clean(resolved)
	return strings.HasPrefix(cleaned, root), nil
}

// Correct: EvalSymlinks before filepath.Clean.
func checkContainmentGoodEval(userPath, root string) (bool, error) {
	resolved, err := filepath.EvalSymlinks(userPath)
	if err != nil {
		return false, err
	}
	cleaned := filepath.Clean(resolved)
	return strings.HasPrefix(cleaned, root), nil
}

// Not flagged: filepath.Clean without containment check.
func justClean(userPath string) string {
	return filepath.Clean(userPath)
}

// Not flagged: HasPrefix without filepath.Clean.
func justHasPrefix(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}

// Not flagged: os.Readlink before filepath.Clean.
func checkWithReadlink(userPath, root string) (bool, error) {
	resolved, err := os.Readlink(userPath)
	if err != nil {
		return false, err
	}
	cleaned := filepath.Clean(resolved)
	return strings.HasPrefix(cleaned, root), nil
}
