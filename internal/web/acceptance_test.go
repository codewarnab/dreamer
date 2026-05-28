// Acceptance suite for spec.v1.5 §13 criteria that require cross-component
// assertions not covered by unit tests in individual packages.
//
// Criteria 35–50 and 53–60 are covered by dedicated unit tests in their
// respective packages (setup_test.go, server_test.go, overlay_test.go,
// apply_test.go, pipeline_test.go, etc.). Only criteria 51 and 52 need
// integration-level assertions here.

package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/state"
	"dreamer/internal/web/handlers"
)

func TestAcceptance_V15(t *testing.T) {
	// 51 — Resolve is purely cosmetic: resolved findings still appear,
	// recurred=true when present in the latest run.
	t.Run("AC51_Resolve_CosmeticAndRecurred", func(t *testing.T) {
		root := t.TempDir()
		hash := "abcd111111111111111111111111111111111111111111111111111111111111"
		projDir := filepath.Join(root, "proj-z")
		if err := os.MkdirAll(projDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		todos := "# t\n\n<!-- dreamer:version:1 -->\n\n## Run 2026-05-20T10:00:00Z\n\n### Doc\n- [ ] recurring mistake\n    <!-- dreamer:finding:" + hash + " -->\n"
		if err := os.WriteFile(filepath.Join(projDir, "todos.md"), []byte(todos), 0o644); err != nil {
			t.Fatalf("seed todos: %v", err)
		}
		now := time.Now().UTC()
		st := &state.State{
			Version:       state.StateVersion,
			LastRunUTC:    now,
			FindingHashes: []string{hash},
			Findings: map[string]state.FindingState{
				hash: {Status: state.FindingStatusResolved, ResolvedAt: now},
			},
		}
		if err := state.Save(root, "proj-z", st); err != nil {
			t.Fatalf("save state: %v", err)
		}
		cfg := &config.App{
			Projects: []config.ProjectConfig{{Name: "proj-z", Path: projDir}},
			Daemon:   config.DaemonConfig{OutputRoot: root, FrequencySeconds: 3600},
		}
		h := handlers.ProjectFindings(handlers.Deps{Config: func() *config.App { return cfg }})
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-z/findings?status=resolved", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Findings []handlers.FindingView `json:"findings"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		if len(resp.Findings) != 1 || resp.Findings[0].Hash != hash {
			t.Fatalf("resolved finding not listed: %+v", resp.Findings)
		}
		if !resp.Findings[0].Recurred {
			t.Errorf("expected recurred=true since hash is in the latest run")
		}
	})

	// 52 — Dashboard sparkline unions multiple days and avg_run_seconds
	// is a weighted mean.
	t.Run("AC52_Dashboard_SparklineUnionsAndWeightedMean", func(t *testing.T) {
		root := t.TempDir()
		name := "proj-spark"
		now := time.Now().UTC()
		var totalRunDurationMillis int64
		var totalRuns int64
		for i := 0; i < 7; i++ {
			date := now.AddDate(0, 0, -i).Format("2006-01-02")
			runMillis := int64(10000 + i*2000)
			delta := state.DaySummaryDelta{Runs: 1, RunDurationMillis: runMillis, PerCategory: map[string]int{"doc": 1}}
			if err := state.UpdateHistoryToday(root, name, date, delta); err != nil {
				t.Fatalf("seed %s: %v", date, err)
			}
			totalRunDurationMillis += runMillis
			totalRuns++
		}
		if err := state.Save(root, name, &state.State{Version: state.StateVersion}); err != nil {
			t.Fatalf("save state: %v", err)
		}
		cfg := &config.App{
			Projects: []config.ProjectConfig{{Name: name, Path: t.TempDir()}},
			Daemon:   config.DaemonConfig{OutputRoot: root, FrequencySeconds: 3600},
		}
		h := handlers.Dashboard(handlers.Deps{Config: func() *config.App { return cfg }})
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Sparkline30d []state.DaySummary `json:"sparkline_30d"`
			Stats        struct {
				AvgRunSeconds int `json:"avg_run_seconds"`
			} `json:"stats"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Sparkline30d) != 7 {
			t.Fatalf("sparkline_30d len=%d want 7; days=%+v", len(resp.Sparkline30d), resp.Sparkline30d)
		}
		wantAvg := int(totalRunDurationMillis / totalRuns / 1000)
		if resp.Stats.AvgRunSeconds != wantAvg {
			t.Errorf("avg_run_seconds=%d want %d (weighted mean)", resp.Stats.AvgRunSeconds, wantAvg)
		}
	})
}
