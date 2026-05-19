package handlers

import (
	"encoding/json"
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
		w.Header().Set("Content-Type", "application/json")
		if !filepath.IsAbs(path) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "path must be absolute"})
			return
		}
		abs := filepath.Clean(path)
		fi, err := os.Stat(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"exists":   false,
					"is_dir":   false,
					"absolute": abs,
				})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"exists":   true,
			"is_dir":   fi.IsDir(),
			"absolute": abs,
		})
	}
}
