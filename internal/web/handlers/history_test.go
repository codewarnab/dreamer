package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dreamer/internal/config"
	"dreamer/internal/state"
)

func TestProjectHistory_ReturnsSavedDays(t *testing.T) {
	root := t.TempDir()
	h := &state.History{
		Days: []state.DaySummary{
			{Date: "2026-05-18", Runs: 1, FindingsNew: 2, FindingsTotal: 2},
			{Date: "2026-05-19", Runs: 3, FindingsNew: 1, FindingsTotal: 3},
			{Date: "2026-05-20", Runs: 2, FindingsNew: 0, FindingsTotal: 3},
		},
	}
	if err := state.SaveHistory(root, "p1", h); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "p1", Path: t.TempDir()}},
		Daemon:   config.DaemonConfig{OutputRoot: root},
	}
	handler := ProjectHistory(Deps{Config: func() *config.Config { return cfg }})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/history", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp historyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Days) != 3 {
		t.Errorf("days = %d, want 3", len(resp.Days))
	}
}

func TestProjectHistory_DaysCap(t *testing.T) {
	root := t.TempDir()
	h := &state.History{
		Days: []state.DaySummary{
			{Date: "2026-05-18", Runs: 1},
			{Date: "2026-05-19", Runs: 1},
			{Date: "2026-05-20", Runs: 1},
		},
	}
	if err := state.SaveHistory(root, "p1", h); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "p1", Path: t.TempDir()}},
		Daemon:   config.DaemonConfig{OutputRoot: root},
	}
	handler := ProjectHistory(Deps{Config: func() *config.Config { return cfg }})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/history?days=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp historyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Days) != 2 {
		t.Fatalf("len = %d, want 2", len(resp.Days))
	}
	if resp.Days[0].Date != "2026-05-19" || resp.Days[1].Date != "2026-05-20" {
		t.Errorf("days = %+v, want tail [05-19, 05-20]", resp.Days)
	}
}

func TestProjectHistory_MissingFileReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "p1", Path: t.TempDir()}},
		Daemon:   config.DaemonConfig{OutputRoot: root},
	}
	handler := ProjectHistory(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/history", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp historyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Days) != 0 {
		t.Errorf("days = %d, want 0", len(resp.Days))
	}
}

func TestProjectHistory_UnknownProject(t *testing.T) {
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "p1", Path: "/tmp/x"}},
		Daemon:   config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	handler := ProjectHistory(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/projects/nope/history", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
