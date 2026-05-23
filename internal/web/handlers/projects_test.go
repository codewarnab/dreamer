package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/state"
)

func buildProjectsCfg(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.Abs(root)
	now := time.Now().UTC()

	stA := &state.State{
		Version:       state.StateVersion,
		LastRunUTC:    now,
		ChatHashes:    map[string]string{"a": "1", "b": "2"},
		FindingHashes: []string{"h1", "h2", "h3"},
		Findings: map[string]state.FindingState{
			"h1": {Status: state.FindingStatusApplied, AppliedAt: now},
		},
	}
	stB := &state.State{
		Version:       state.StateVersion,
		LastRunUTC:    time.Time{}, // never run
		ChatHashes:    map[string]string{},
		FindingHashes: []string{},
	}
	if err := state.Save(root, "proj-a", stA); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(root, "proj-b", stB); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		Projects: []config.ProjectConfig{
			{Name: "proj-a", Path: "/tmp/proj-a", Since: "2026-01-01"},
			{Name: "proj-b", Path: "/tmp/proj-b"},
		},
		Daemon: config.DaemonConfig{FrequencySeconds: 3600, OutputRoot: root},
	}
}

func TestProjectsList(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectsList(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp projectsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Projects) != 2 {
		t.Fatalf("len(projects) = %d, want 2", len(resp.Projects))
	}
	byName := map[string]ProjectRollup{}
	for _, p := range resp.Projects {
		byName[p.Name] = p
	}
	a := byName["proj-a"]
	if a.ChatsCount != 2 {
		t.Errorf("proj-a chats_count = %d, want 2", a.ChatsCount)
	}
	if a.FindingsApplied != 1 {
		t.Errorf("proj-a findings_applied = %d, want 1", a.FindingsApplied)
	}
	if a.FindingsOpen != 2 {
		t.Errorf("proj-a findings_open = %d, want 2", a.FindingsOpen)
	}
	if a.Since != "2026-01-01" {
		t.Errorf("proj-a since = %q, want 2026-01-01", a.Since)
	}
	if a.LastRunUTC == "" || a.NextRunUTC == "" {
		t.Errorf("proj-a last_run/next_run empty: %+v", a)
	}
	b := byName["proj-b"]
	if b.LastRunUTC != "" {
		t.Errorf("proj-b last_run_utc = %q, want empty", b.LastRunUTC)
	}
}

func TestProjectDetail_Found(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectDetail(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var rollup ProjectRollup
	if err := json.Unmarshal(rec.Body.Bytes(), &rollup); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rollup.Name != "proj-a" || rollup.ChatsCount != 2 || rollup.FindingsApplied != 1 {
		t.Errorf("rollup mismatch: %+v", rollup)
	}
}

func TestProjectDetail_Unknown(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectDetail(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestProjectDetail_RejectSubpath(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectDetail(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-a/findings", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (sub-path is E.16's job)", rec.Code)
	}
}
