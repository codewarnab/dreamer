package fsutil

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultFileListCap is the maximum number of files returned by ListProjectFiles.
const DefaultFileListCap = 2000

// maxWalkDepth is the maximum directory nesting depth for listViaWalkDir.
// Directories deeper than this are skipped to prevent resource exhaustion
// on pathologically deep trees.
const maxWalkDepth = 30

// ListProjectFiles returns repo-relative file paths within projectRoot.
// It tries git ls-files first (respects .gitignore); falls back to
// filepath.WalkDir when git is unavailable or the directory is not a repo.
// Results are capped at maxFiles (0 = DefaultFileListCap), normalized to
// forward slashes, and sorted alphabetically.
func ListProjectFiles(projectRoot string, maxFiles int) ([]string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		return nil, nil
	}

	// Normalize to absolute and resolve symlinks so callers can't trick
	// the walk into escaping the intended tree.
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("list project files %q: resolve absolute: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("list project files %q: eval symlinks: %w", root, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("list project files %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("list project files %q: not a directory", root)
	}
	root = resolved

	if maxFiles <= 0 {
		maxFiles = DefaultFileListCap
	}

	files, err := listViaGit(root)
	if err != nil {
		files, err = listViaWalkDir(root)
	}
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}
	return files, nil
}

// listViaGit runs "git ls-files" to discover tracked and untracked files
// while respecting .gitignore. Returns nil, err if git is unavailable or
// the directory is not a git repo.
func listViaGit(projectRoot string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard")
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return []string{}, nil
	}
	lines := strings.Split(raw, "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, filepath.ToSlash(line))
		}
	}
	return files, nil
}

// listViaWalkDir walks the project tree, skipping common noise directories
// and binary file extensions. Uses the shared ShouldSkipDir/ShouldSkipFile
// predicates with protected Dreamer/provider dirs appended.
func listViaWalkDir(projectRoot string) ([]string, error) {
	files := make([]string, 0, 256)
	err := filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			// Depth-limit: skip directories deeper than maxWalkDepth to
			// bound CPU/memory on pathologically deep trees.
			if path != projectRoot {
				rel, err := filepath.Rel(projectRoot, path)
				if err == nil && strings.Count(rel, string(os.PathSeparator)) >= maxWalkDepth {
					return fs.SkipDir
				}
			}
			if ShouldSkipDir(d.Name(), path == projectRoot, protectedDirs...) {
				return fs.SkipDir
			}
			return nil
		}
		if ShouldSkipFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

// protectedDirs are Dreamer/provider directories that must never appear
// in file listings. Kept separate so ShouldSkipDir can be reused by
// callers that don't need these (e.g. grounding.DetectFiles).
var protectedDirs = []string{".dreamer", ".claude", ".codex", ".copilot", ".gemini"}

// ShouldSkipDir returns true for directories that are noise for file picking.
// extraDirs (if any) are appended to the built-in skip list — use this to
// add project-specific protected directories (e.g. ".dreamer", ".claude").
func ShouldSkipDir(name string, isRoot bool, extraDirs ...string) bool {
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
	for _, d := range extraDirs {
		if name == d {
			return true
		}
	}
	return strings.HasPrefix(name, ".") && name != "."
}

// ShouldSkipFile returns true for binary and media files that should not
// appear in file listings.
func ShouldSkipFile(name string) bool {
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
