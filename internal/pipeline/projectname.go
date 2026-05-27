package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"dreamer/internal/fsutil"
)

var projectNameSafePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// DeriveProjectName converts an absolute project path into the directory name
// dreamer uses under the output root.
//
// The name starts as "project-<basename>" with unsafe characters replaced by
// underscores. If usedNames already maps that candidate to a different path,
// the returned name gets a short hash suffix so projects with the same basename
// do not share state or todos files.
func DeriveProjectName(projectPath string, usedNames map[string]string) string {
	clean := filepath.Clean(projectPath)
	base := filepath.Base(clean)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "root"
	}
	safe := projectNameSafePattern.ReplaceAllString(base, "_")
	if safe == "" {
		safe = "root"
	}
	candidate := "project-" + safe
	if safe == base && !projectNameCollides(candidate, clean, usedNames) {
		return candidate
	}
	sum := sha256.Sum256([]byte(clean))
	return candidate + "-" + hex.EncodeToString(sum[:])[:8]
}

func projectNameCollides(candidate string, projectPath string, usedNames map[string]string) bool {
	if len(usedNames) == 0 {
		return false
	}
	existingPath, ok := usedNames[candidate]
	if !ok {
		return false
	}
	return filepath.Clean(existingPath) != projectPath
}

func resolveAbsoluteProjectPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("--path is required")
	}
	expanded, err := fsutil.ExpandUserHome(trimmed)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}
