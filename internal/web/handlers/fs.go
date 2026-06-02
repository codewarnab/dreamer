package handlers

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"dreamer/internal/fsutil"
)

// FSExists returns an http.HandlerFunc for GET /api/fs/exists.
// Reports whether a given absolute path exists and whether it is a
// directory. Paths are restricted to configured project roots and the
// daemon output root to prevent arbitrary filesystem enumeration.
func FSExists(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := r.URL.Query().Get("path")
		if !filepath.IsAbs(path) {
			writeJSONError(w, http.StatusBadRequest, "path must be absolute")
			return
		}
		abs := filepath.Clean(path)
		// Resolve symlinks before containment check to prevent traversal
		// via symlink pointing outside project roots.
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		if !fsPathAllowed(deps, abs) {
			writeJSONError(w, http.StatusForbidden, "path outside configured project roots")
			return
		}
		fi, err := os.Stat(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeJSON(w, http.StatusOK, map[string]any{
					"exists":   false,
					"is_dir":   false,
					"absolute": abs,
				})
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"exists":   true,
			"is_dir":   fi.IsDir(),
			"absolute": abs,
		})
	}
}

// fsPathAllowed returns true if abs is under any configured project root
// or the daemon output root.
func fsPathAllowed(deps Deps, abs string) bool {
	cfg := deps.Config()
	if cfg == nil {
		return false
	}
	for _, p := range cfg.Projects {
		if fsutil.PathWithinRoot(abs, p.Path) {
			return true
		}
	}
	if cfg.Daemon.OutputRoot != "" {
		if fsutil.PathWithinRoot(abs, cfg.Daemon.OutputRoot) {
			return true
		}
	}
	return false
}
