package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/state"
)

// ProjectRollup is the per-project summary surfaced by both the list and
// detail endpoints. Fields align with the spec.v1.5 §7.2 project rollup.
type ProjectRollup struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	Since             string `json:"since,omitempty"`
	FindingsOpen      int    `json:"findings_open"`
	FindingsApplied   int    `json:"findings_applied"`
	FindingsDismissed int    `json:"findings_dismissed"`
	FindingsResolved  int    `json:"findings_resolved"`
	LastRunUTC        string `json:"last_run_utc,omitempty"`
	NextRunUTC        string `json:"next_run_utc,omitempty"`
	ChatsCount        int    `json:"chats_count"`
}

type projectsListResponse struct {
	Projects []ProjectRollup `json:"projects"`
}

// ProjectsList returns an http.HandlerFunc for GET /api/projects.
func ProjectsList(deps Deps) http.HandlerFunc {
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
		out := projectsListResponse{Projects: make([]ProjectRollup, 0, len(cfg.Projects))}
		for _, p := range cfg.Projects {
			out.Projects = append(out.Projects, projectRollup(cfg, p))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// ProjectDetail returns an http.HandlerFunc for GET /api/projects/{name}.
// Sub-paths under {name} (findings/, history/, chats/, run/) route via the
// dispatcher landing in E.16; this handler only responds to the bare detail
// path and returns 404 for trailing segments.
func ProjectDetail(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Path shape: /api/projects/<name> -> tail after /api/projects/ must be a
		// single non-empty segment with no further '/'.
		const prefix = "/api/projects/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		tail := strings.TrimPrefix(r.URL.Path, prefix)
		tail = strings.TrimSuffix(tail, "/")
		if tail == "" || strings.Contains(tail, "/") {
			http.NotFound(w, r)
			return
		}
		cfg := deps.Config()
		if cfg == nil {
			http.Error(w, "config unavailable", http.StatusInternalServerError)
			return
		}
		for _, p := range cfg.Projects {
			if p.Name == tail {
				rollup := projectRollup(cfg, p)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rollup)
				return
			}
		}
		http.NotFound(w, r)
	}
}

func projectRollup(cfg *config.Config, p config.ProjectConfig) ProjectRollup {
	rollup := ProjectRollup{Name: p.Name, Path: p.Path, Since: p.Since}
	st, err := state.Load(cfg.Daemon.OutputRoot, p.Name)
	if err != nil || st == nil {
		return rollup
	}
	rollup.ChatsCount = len(st.ChatHashes)
	lifecycleTouched := 0
	for _, fs := range st.Findings {
		switch fs.Status {
		case state.FindingStatusApplied:
			rollup.FindingsApplied++
			lifecycleTouched++
		case state.FindingStatusDismissed:
			rollup.FindingsDismissed++
			lifecycleTouched++
		case state.FindingStatusResolved:
			rollup.FindingsResolved++
			lifecycleTouched++
		}
	}
	open := len(st.FindingHashes) - lifecycleTouched
	if open < 0 {
		open = 0
	}
	rollup.FindingsOpen = open
	if !st.LastRunUTC.IsZero() {
		rollup.LastRunUTC = st.LastRunUTC.UTC().Format(time.RFC3339)
		if cfg.Daemon.FrequencySeconds > 0 {
			rollup.NextRunUTC = st.LastRunUTC.UTC().Add(time.Duration(cfg.Daemon.FrequencySeconds) * time.Second).Format(time.RFC3339)
		}
	}
	return rollup
}
