package grounding

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultFileCap is the cap on file-list entries per spec §7.4.
const DefaultFileCap = 2000

// DetectFiles walks the project root and returns repo-relative file paths,
// skipping common noise directories. Cap of zero or negative defaults to
// DefaultFileCap.
func DetectFiles(projectRoot string, cap int) ([]string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		return nil, nil
	}
	if cap <= 0 {
		cap = DefaultFileCap
	}
	files := make([]string, 0, 256)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name(), path == root) {
				return fs.SkipDir
			}
			return nil
		}
		if shouldSkipFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) > cap {
		files = files[:cap]
	}
	return files, nil
}

func shouldSkipDir(name string, isRoot bool) bool {
	if isRoot {
		return false
	}
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", ".venv", "venv",
		"__pycache__", "target", "build", "dist", "out", ".next",
		".turbo", ".cache", ".idea", ".vscode", ".gradle", "Pods",
		".direnv", ".tox", ".pytest_cache", ".mypy_cache", ".ruff_cache",
		"coverage", ".bundle":
		return true
	}
	return strings.HasPrefix(name, ".") && name != "."
}

func shouldSkipFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".bmp",
		".svg", ".pdf", ".zip", ".tar", ".gz", ".7z", ".rar",
		".mp3", ".mp4", ".mov", ".wav", ".woff", ".woff2", ".ttf",
		".otf", ".eot", ".class", ".jar", ".so", ".dll", ".dylib",
		".bin", ".exe":
		return true
	}
	return false
}
