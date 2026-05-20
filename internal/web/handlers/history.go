package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"dreamer/internal/state"
)

const defaultHistoryDays = 90

type historyResponse struct {
	Days []state.DaySummary `json:"days"`
}

// ProjectHistory returns GET /api/projects/{name}/history — the project's
// history.json daily rollup, optionally tail-capped by ?days=N. A missing
// history.json (no runs yet) returns an empty days list with 200; only an
// unknown project name returns 404.
func ProjectHistory(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg := deps.Config()
		if cfg == nil {
			http.Error(w, "config unavailable", http.StatusInternalServerError)
			return
		}
		name := historyProjectName(r.URL.Path)
		if name == "" {
			http.NotFound(w, r)
			return
		}
		known := false
		for _, p := range cfg.Projects {
			if p.Name == name {
				known = true
				break
			}
		}
		if !known {
			http.NotFound(w, r)
			return
		}

		days := defaultHistoryDays
		if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				days = n
			}
		}

		hist, err := state.LoadHistory(cfg.Daemon.OutputRoot, name)
		if err != nil {
			http.Error(w, "load history: "+err.Error(), http.StatusInternalServerError)
			return
		}
		out := historyResponse{Days: []state.DaySummary{}}
		if hist != nil && len(hist.Days) > 0 {
			tail := hist.Days
			if len(tail) > days {
				tail = tail[len(tail)-days:]
			}
			out.Days = tail
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// historyProjectName extracts <name> from /api/projects/<name>/history.
func historyProjectName(path string) string {
	const prefix = "/api/projects/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	tail := strings.TrimPrefix(path, prefix)
	tail = strings.TrimSuffix(tail, "/")
	parts := strings.Split(tail, "/")
	if len(parts) != 2 || parts[1] != "history" || parts[0] == "" {
		return ""
	}
	return parts[0]
}
