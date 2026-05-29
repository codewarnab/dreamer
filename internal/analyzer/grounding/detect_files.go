package grounding

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"dreamer/internal/fsutil"
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
			if fsutil.ShouldSkipDir(d.Name(), path == root) {
				return fs.SkipDir
			}
			return nil
		}
		if fsutil.ShouldSkipFile(d.Name()) {
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

