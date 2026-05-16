package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"dreamer/internal/config"
)

var projectNameSafePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// deriveProjectName converts an absolute project path into the directory name
// dreamer uses under the output root (spec §11.1). Output is always
// "project-<basename>"; characters outside [A-Za-z0-9._-] become "_" and a
// short hash suffix disambiguates paths whose basename had to be rewritten
// (so /a/foo and /b/foo bar don't collide).
func deriveProjectName(projectPath string) string {
	clean := filepath.Clean(projectPath)
	base := filepath.Base(clean)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "root"
	}
	safe := projectNameSafePattern.ReplaceAllString(base, "_")
	if safe == "" {
		safe = "root"
	}
	if safe == base {
		return "project-" + safe
	}
	sum := sha256.Sum256([]byte(clean))
	return "project-" + safe + "-" + hex.EncodeToString(sum[:])[:8]
}

func resolveAbsoluteProjectPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("--path is required")
	}
	expanded, err := config.ExpandUserHome(trimmed)
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
