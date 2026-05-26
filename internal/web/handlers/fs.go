package handlers

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
)

// FSExists returns an http.HandlerFunc for GET /api/fs/exists.
// Reports whether a given absolute path exists and whether it is a
// directory. Refuses to disclose any other filesystem information.
func FSExists(_ Deps) http.HandlerFunc {
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
