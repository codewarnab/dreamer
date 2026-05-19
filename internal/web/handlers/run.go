// Package handlers — on-demand pipeline run trigger.
package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Run returns the POST /api/projects/{name}/run handler. The actual
// pipeline execution is delegated to Deps.EnqueueRun, which is wired
// to a single-worker-per-project queue in package web.
func Run(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if deps.EnqueueRun == nil {
			http.Error(w, "runner not configured", http.StatusServiceUnavailable)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		// /api/projects/{name}/run
		if len(parts) != 4 || parts[0] != "api" || parts[1] != "projects" || parts[3] != "run" {
			http.NotFound(w, r)
			return
		}
		name := parts[2]
		cfg := deps.Config()
		if cfg == nil {
			http.Error(w, "config unavailable", http.StatusInternalServerError)
			return
		}
		found := false
		for _, p := range cfg.Projects {
			if p.Name == name {
				found = true
				break
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		runID, accepted, err := deps.EnqueueRun(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if !accepted {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "a run is already in flight for this project"})
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"run_id": runID, "project": name})
	}
}
