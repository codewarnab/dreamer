package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/pipeline"
	"dreamer/internal/state"
)

// seedProject writes a state.json + history.json for one project.
func seedProject(t *testing.T, root, name string, st *state.State, hist *state.History) {
	t.Helper()
	if err := state.Save(root, name, st); err != nil {
		t.Fatalf("save state %s: %v", name, err)
	}
	if hist != nil {
		if err := state.SaveHistory(root, name, hist); err != nil {
			t.Fatalf("save history %s: %v", name, err)
		}
	}
}

func TestDashboard_AggregatesAcrossProjects(t *testing.T) {
	root := t.TempDir()
	// Use absolute config.Daemon.OutputRoot.
	root, _ = filepath.Abs(root)

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	stA := &state.State{
		Version:       state.StateVersion,
		LastRunUTC:    now.Add(-1 * time.Hour),
		ChatHashes:    map[string]string{"a": "1", "b": "2"},
		FindingHashes: []string{"h1", "h2", "h3"},
		Findings: map[string]state.FindingState{
			"h1": {Status: state.FindingStatusApplied, AppliedAt: now},
			"h2": {Status: state.FindingStatusDismissed, DismissedAt: now},
		},
		ProviderUsage: map[string]state.ProviderUsage{
			"copilot": {Runs: 10, Failures: 1, LastError: ""},
		},
	}
	histA := &state.History{Version: 1, Days: []state.DaySummary{
		{Date: yesterday, Runs: 2, FindingsNew: 3, Tokens: 100, AvgRunDurationMillis: 5000, PerCategory: map[string]int{"doc": 2}},
		{Date: today, Runs: 1, FindingsNew: 1, Tokens: 50, AvgRunDurationMillis: 2000, PerCategory: map[string]int{"doc": 1}},
	}}

	stB := &state.State{
		Version:       state.StateVersion,
		LastRunUTC:    now,
		ChatHashes:    map[string]string{"x": "9"},
		FindingHashes: []string{"hX"},
		Findings: map[string]state.FindingState{
			"hX": {Status: state.FindingStatusResolved, ResolvedAt: now},
		},
		ProviderUsage: map[string]state.ProviderUsage{
			"copilot": {Runs: 5, LastError: ""},
			"claude":  {Runs: 1, LastError: "boom"},
		},
	}
	histB := &state.History{Version: 1, Days: []state.DaySummary{
		{Date: today, Runs: 1, FindingsNew: 2, Tokens: 25, AvgRunDurationMillis: 1000, PerCategory: map[string]int{"sec": 5}},
	}}

	seedProject(t, root, "proj-a", stA, histA)
	seedProject(t, root, "proj-b", stB, histB)

	cfg := &config.App{
		Projects: []config.ProjectConfig{
			{Name: "proj-a", Path: t.TempDir()},
			{Name: "proj-b", Path: t.TempDir()},
		},
		Daemon: config.DaemonConfig{
			FrequencySeconds: 3600,
			OutputRoot:       root,
		},
	}

	h := Dashboard(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp dashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if got, want := resp.Stats.ChatsAnalyzedTotal, 3; got != want {
		t.Errorf("chats_analyzed_total = %d, want %d", got, want)
	}
	if got, want := resp.Stats.FindingsApplied, 1; got != want {
		t.Errorf("findings_applied = %d, want %d", got, want)
	}
	if got, want := resp.Stats.FindingsDismissed, 1; got != want {
		t.Errorf("findings_dismissed = %d, want %d", got, want)
	}
	if got, want := resp.Stats.FindingsResolved, 1; got != want {
		t.Errorf("findings_resolved = %d, want %d", got, want)
	}
	// Open: proj-a has 3 hashes - 2 lifecycle = 1. proj-b: 1 - 1 = 0.
	if got, want := resp.Stats.FindingsOpen, 1; got != want {
		t.Errorf("findings_open = %d, want %d", got, want)
	}
	// per_category union: doc=3, sec=5.
	if resp.PerCategory["doc"] != 3 || resp.PerCategory["sec"] != 5 {
		t.Errorf("per_category = %v, want doc=3 sec=5", resp.PerCategory)
	}
	// Sparkline sorted ascending.
	for i := 1; i < len(resp.Sparkline30d); i++ {
		if resp.Sparkline30d[i-1].Date > resp.Sparkline30d[i].Date {
			t.Errorf("sparkline not sorted ascending: %v", resp.Sparkline30d)
			break
		}
	}
	// providers_healthy: copilot healthy (no LastError, Runs>0), claude unhealthy.
	if resp.Stats.ProvidersHealthy != "1/2" {
		t.Errorf("providers_healthy = %q, want 1/2", resp.Stats.ProvidersHealthy)
	}
	if resp.Stats.LastRunUTC == "" {
		t.Errorf("last_run_utc empty; want RFC3339 string")
	}
	if resp.Stats.NextRunUTC == "" {
		t.Errorf("next_run_utc empty; want RFC3339 string")
	}
	if resp.LiveActivity == nil {
		t.Errorf("live_activity must be non-nil empty array, got nil")
	}
}

func TestDashboard_UsesRecentActivity(t *testing.T) {
	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: t.TempDir()}}
	at := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	events := []pipeline.Event{
		{Type: "run.start", At: at, Payload: map[string]any{"project": "p"}},
		{Type: "run.done", At: at.Add(time.Minute), Payload: map[string]any{"project": "p"}},
	}
	h := Dashboard(Deps{
		Config:         func() *config.App { return cfg },
		RecentActivity: func() []pipeline.Event { return events },
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp dashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.LiveActivity) != 2 {
		t.Fatalf("live_activity len = %d, want 2", len(resp.LiveActivity))
	}
	first, ok := resp.LiveActivity[0].(map[string]any)
	if !ok {
		t.Fatalf("live_activity[0] not map: %T", resp.LiveActivity[0])
	}
	if first["type"] != "run.start" {
		t.Errorf("first.type = %v, want run.start", first["type"])
	}
	if first["at"] != "2026-05-20T10:00:00Z" {
		t.Errorf("first.at = %v, want 2026-05-20T10:00:00Z", first["at"])
	}
}

func TestDashboard_MethodNotAllowed(t *testing.T) {
	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: t.TempDir()}}
	h := Dashboard(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard", nil)
	h(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
