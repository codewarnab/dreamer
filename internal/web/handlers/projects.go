package handlers

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/state"
)

// configWriteMu serializes config.yaml writes from web handlers so two
// concurrent DELETE requests (e.g. from multiple browser tabs) can't race on
// the read-modify-write cycle. Cross-process safety against the CLI relies on
// atomic temp+rename writes, matching `dreamer add` / `dreamer remove`.
var configWriteMu sync.Mutex

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
			out.Projects = append(out.Projects, projectRollup(cfg, p, deps.StateCache))
		}
		writeJSON(w, http.StatusOK, out)
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
				rollup := projectRollup(cfg, p, deps.StateCache)
				writeJSON(w, http.StatusOK, rollup)
				return
			}
		}
		http.NotFound(w, r)
	}
}

// ProjectDelete returns an http.HandlerFunc for DELETE /api/projects/{name}.
// It removes the named project from config.yaml using the same
// comment-preserving yaml.v3 Node rewrite that `dreamer remove` uses, then
// clears any stale projects: list from the overlay so it can't shadow the
// base config. The daemon's fsnotify watcher picks up the write and publishes
// a config.reloaded SSE event, which the dashboard listens for to refresh.
func ProjectDelete(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
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

		if deps.ConfigPath == nil || deps.ConfigPath() == "" {
			writeJSONError(w, http.StatusServiceUnavailable, "config path not configured; removal unavailable")
			return
		}
		configPath := deps.ConfigPath()

		cfg := deps.Config()
		if cfg == nil {
			http.Error(w, "config unavailable", http.StatusInternalServerError)
			return
		}
		found := false
		for _, p := range cfg.Projects {
			if p.Name == tail {
				found = true
				break
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}

		// Serialize the read-modify-write of config.yaml against other web
		// writers. fsutil.WriteFileAtomic (temp+rename) guards against the
		// CLI writing concurrently from another process.
		configWriteMu.Lock()
		defer configWriteMu.Unlock()

		configBytes, err := os.ReadFile(configPath)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "read config: "+err.Error())
			return
		}
		updated, err := config.RemoveProjectFromYAML(configBytes, tail)
		if err != nil {
			// RemoveProjectFromYAML returns "project ... not found" when the
			// name is absent from the base config (e.g. it only existed via an
			// overlay). Surface that as a 404; everything else is a 500.
			if strings.Contains(err.Error(), "not found") {
				http.NotFound(w, r)
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "remove project: "+err.Error())
			return
		}
		if err := fsutil.WriteFileAtomic(configPath, updated, fsutil.FilePerms); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "write config: "+err.Error())
			return
		}

		// Clear any projects: key the overlay may carry. A non-empty overlay
		// projects: list REPLACES the base list on reload (see overlay.go),
		// which would resurrect the project we just deleted. Projects are
		// owned by config.yaml, so the overlay should never carry them.
		if deps.OverlayPath != nil && deps.OverlayPath() != "" {
			if err := clearOverlayProjects(deps.OverlayPath()); err != nil && deps.Logger != nil {
				deps.Logger.Warn("clear overlay projects failed: " + err.Error())
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": tail})
	}
}

// clearOverlayProjects removes the projects: key from the overlay file if
// present, leaving all other overlay keys intact. A missing or empty overlay
// is a no-op.
func clearOverlayProjects(overlayPath string) error {
	data, err := os.ReadFile(overlayPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var current map[string]any
	if err := yaml.Unmarshal(data, &current); err != nil || current == nil {
		// Leave a malformed overlay untouched; settings handler surfaces it.
		return nil //nolint:nilerr // intentional: don't clobber an unparseable overlay
	}
	if _, ok := current["projects"]; !ok {
		return nil
	}
	delete(current, "projects")
	out, err := yaml.Marshal(current)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(overlayPath, out, fsutil.FilePerms)
}

func projectRollup(cfg *config.App, p config.ProjectConfig, sc *state.StateCache) ProjectRollup {
	rollup := ProjectRollup{Name: p.Name, Path: p.Path, Since: p.Since}
	var st *state.State
	var err error
	if sc != nil {
		st, err = sc.GetState(cfg.Daemon.OutputRoot, p.Name)
	} else {
		st, err = state.Load(cfg.Daemon.OutputRoot, p.Name)
	}
	if err != nil || st == nil {
		return rollup
	}
	rollup.ChatsCount = len(st.ChatHashes)
	lifecycleTouched := 0
	for _, findingState := range st.Findings {
		switch findingState.Status {
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
