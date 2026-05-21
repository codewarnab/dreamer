package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/state"
)

func TestProviders_MergesAcrossProjects(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	older := now.Add(-2 * time.Hour)

	stA := &state.State{
		Version: state.StateVersion,
		ProviderUsage: map[string]state.ProviderUsage{
			"openclaude-cli": {Runs: 7, Failures: 1, Timeouts: 0, LastSuccessUTC: older, LastError: ""},
			"copilot-sdk":    {Runs: 3, Failures: 2, Timeouts: 1, LastSuccessUTC: older, LastError: "boom"},
		},
	}
	stB := &state.State{
		Version: state.StateVersion,
		ProviderUsage: map[string]state.ProviderUsage{
			"openclaude-cli": {Runs: 5, Failures: 0, LastSuccessUTC: now, LastError: ""},
		},
	}
	if err := state.Save(root, "a", stA); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(root, "b", stB); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Projects: []config.ProjectConfig{
			{Name: "a", Path: "/tmp/a"},
			{Name: "b", Path: "/tmp/b"},
		},
		Daemon: config.DaemonConfig{OutputRoot: root},
		Providers: map[string]config.ProviderBlock{
			"openclaude-cli": {Model: "mimo-v2.5-pro"},
		},
	}

	h := Providers(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp providersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	byID := map[string]ProviderHealth{}
	for _, p := range resp.Providers {
		byID[p.ID] = p
	}
	oc, ok := byID["openclaude-cli"]
	if !ok {
		t.Fatalf("openclaude-cli missing: %+v", resp.Providers)
	}
	if oc.Runs != 12 {
		t.Errorf("openclaude-cli runs = %d, want 12", oc.Runs)
	}
	if oc.Failures != 1 {
		t.Errorf("openclaude-cli failures = %d, want 1", oc.Failures)
	}
	if oc.Model != "mimo-v2.5-pro" {
		t.Errorf("openclaude-cli model = %q, want mimo-v2.5-pro", oc.Model)
	}
	if !oc.Healthy {
		t.Errorf("openclaude-cli should be healthy: %+v", oc)
	}
	if oc.LastSuccessUTC == "" {
		t.Errorf("openclaude-cli last_success_utc empty")
	}

	cp, ok := byID["copilot-sdk"]
	if !ok {
		t.Fatalf("copilot-sdk missing")
	}
	if cp.Healthy {
		t.Errorf("copilot-sdk should NOT be healthy (last_error=%q)", cp.LastError)
	}
	if cp.LastError != "boom" {
		t.Errorf("copilot-sdk last_error = %q, want boom", cp.LastError)
	}
	// Falls back to DefaultModelByProvider when no override is set.
	if cp.Model != config.DefaultModelByProvider[config.ProviderCopilotSDK] {
		t.Errorf("copilot-sdk model = %q, want default %q", cp.Model, config.DefaultModelByProvider[config.ProviderCopilotSDK])
	}
}

func TestProviders_StaleSuccessNotHealthy(t *testing.T) {
	root := t.TempDir()
	stale := time.Now().UTC().Add(-48 * time.Hour)
	st := &state.State{
		Version: state.StateVersion,
		ProviderUsage: map[string]state.ProviderUsage{
			"openclaude-cli": {Runs: 1, LastSuccessUTC: stale},
		},
	}
	if err := state.Save(root, "p", st); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "p", Path: "/tmp/p"}},
		Daemon:   config.DaemonConfig{OutputRoot: root},
	}
	h := Providers(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	var resp providersResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Providers) != 1 || resp.Providers[0].Healthy {
		t.Errorf("stale (48h) should not be healthy: %+v", resp.Providers)
	}
}

func TestProviders_NoProjects(t *testing.T) {
	cfg := &config.Config{Daemon: config.DaemonConfig{OutputRoot: t.TempDir()}}
	h := Providers(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp providersResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Providers) != 0 {
		t.Errorf("providers = %d, want 0", len(resp.Providers))
	}
}
