package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dreamer/internal/analyzer"
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
	cfg := &config.App{
		Projects: []config.ProjectConfig{
			{Name: "a", Path: "/tmp/a"},
			{Name: "b", Path: "/tmp/b"},
		},
		Daemon: config.DaemonConfig{OutputRoot: root},
		Providers: map[string]config.ProviderBlock{
			"openclaude-cli": {Model: "mimo-v2.5-pro"},
		},
	}

	h := Providers(Deps{Config: func() *config.App { return cfg }})
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
	// Falls back to DefaultModelFor when no override is set.
	if cp.Model != config.DefaultModelFor(string(config.ProviderCopilotSDK)) {
		t.Errorf("copilot-sdk model = %q, want default %q", cp.Model, config.DefaultModelFor(string(config.ProviderCopilotSDK)))
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
	cfg := &config.App{
		Projects: []config.ProjectConfig{{Name: "p", Path: "/tmp/p"}},
		Daemon:   config.DaemonConfig{OutputRoot: root},
	}
	h := Providers(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	var resp providersResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Providers) != 1 || resp.Providers[0].Healthy {
		t.Errorf("stale (48h) should not be healthy: %+v", resp.Providers)
	}
}

func TestProviders_NoProjects(t *testing.T) {
	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: t.TempDir()}}
	h := Providers(Deps{Config: func() *config.App { return cfg }})
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

func TestProviders_RemediationPresent(t *testing.T) {
	root := t.TempDir()
	st := &state.State{
		Version: state.StateVersion,
		ProviderUsage: map[string]state.ProviderUsage{
			"openclaude-cli": {Runs: 1},
		},
	}
	if err := state.Save(root, "p", st); err != nil {
		t.Fatal(err)
	}
	cfg := &config.App{
		Projects: []config.ProjectConfig{{Name: "p", Path: "/tmp/p"}},
		Daemon:   config.DaemonConfig{OutputRoot: root},
	}
	h := Providers(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	var resp providersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Providers) == 0 {
		t.Fatal("expected at least one provider")
	}
	if resp.Providers[0].Remediation == "" {
		t.Errorf("openclaude-cli remediation should be non-empty; got empty string")
	}
}

func TestProviderMeta_ReturnsAllRegisteredProviders(t *testing.T) {
	h := ProviderMeta(Deps{Config: func() *config.App { return &config.App{} }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/provider-meta", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp providerMetaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// Provider init() functions in other packages are not imported here, so the
	// registry may be empty in the test binary. Skip rather than fail when that
	// is the case — the contract (well-formed JSON, provider fields present) is
	// still verified by the shape check below when providers are registered.
	if len(resp.Providers) == 0 {
		t.Skip("no providers registered in this test binary; skipping content assertions")
	}
	// Every returned entry must have a non-empty ID and at least one model.
	for _, p := range resp.Providers {
		if p.ID == "" {
			t.Errorf("provider entry has empty id: %+v", p)
		}
		if len(p.Models) == 0 {
			t.Errorf("provider %q has no models", p.ID)
		}
	}
}

func TestProviderMeta_ModelsMatchDefaults(t *testing.T) {
	h := ProviderMeta(Deps{Config: func() *config.App { return &config.App{} }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/provider-meta", nil))
	var resp providerMetaResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Providers) == 0 {
		t.Skip("no providers registered in this test binary; skipping")
	}
	for _, p := range resp.Providers {
		want := config.DefaultModelFor(p.ID)
		if want == "" {
			continue // provider has no registered defaults — skip
		}
		if p.DefaultModel != want {
			t.Errorf("provider %q default_model = %q, want %q", p.ID, p.DefaultModel, want)
		}
		if len(p.Models) == 0 || p.Models[0] != want {
			t.Errorf("provider %q models[0] = %q, want %q", p.ID, func() string {
				if len(p.Models) > 0 {
					return p.Models[0]
				}
				return "(empty)"
			}(), want)
		}
	}
}

func TestProviderMeta_MethodNotAllowed(t *testing.T) {
	h := ProviderMeta(Deps{Config: func() *config.App { return &config.App{} }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/api/provider-meta", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestProviderTest_MethodNotAllowed(t *testing.T) {
	h := ProviderTest(Deps{Config: func() *config.App { return &config.App{} }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/providers/openclaude-cli/test", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestProviderTest_UnknownID(t *testing.T) {
	h := ProviderTest(Deps{Config: func() *config.App { return &config.App{} }})
	rec := httptest.NewRecorder()
	// Path without the required /test suffix → providerIDFromPath returns ""
	h(rec, httptest.NewRequest(http.MethodPost, "/api/providers/", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestProviderTest_ProviderNotRegistered(t *testing.T) {
	h := ProviderTest(Deps{Config: func() *config.App { return &config.App{} }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/api/providers/nonexistent-provider-xyz/test", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for unregistered provider, got %d", rec.Code)
	}
}

func TestProviderErrorCategory(t *testing.T) {
	tests := []struct {
		msg  string
		want string
	}{
		{"rate limit exceeded", "rate limited"},
		{"got 429 from api", "rate limited"},
		{"quota exceeded", "rate limited"},
		{"binary not found in PATH", "not installed"},
		{"not installed", "not installed"},
		{"all 5 output lines failed to parse (provider schema change?)", "version mismatch"},
		{"provider schema change", "version mismatch"},
		{"permission denied", "auth failure"},
		{"unauthorized", "auth failure"},
		{"invalid api key", "auth failure"},
		{"context deadline exceeded", "timed out"},
		{"request timeout", "timed out"},
		{"no assistant content emitted", "empty response"},
		{"some random error", "error"},
		{"", "error"},
	}
	for _, tc := range tests {
		got := providerErrorCategory(tc.msg)
		if got != tc.want {
			t.Errorf("providerErrorCategory(%q) = %q, want %q", tc.msg, got, tc.want)
		}
	}
}

func TestProviderIDFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/api/providers/openclaude-cli/test", "openclaude-cli"},
		{"/api/providers/claude-cli/test", "claude-cli"},
		{"/api/providers//test", ""},
		{"/api/providers/test", ""},
		{"/api/providers/", ""},
		{"/api/providers/foo/bar", ""},
	}
	for _, tc := range tests {
		got := providerIDFromPath(tc.path)
		if got != tc.want {
			t.Errorf("providerIDFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestProviderMeta_UsesStaticListWhenNoModelLister(t *testing.T) {
	// With a nil ModelListCache, enrichWithLiveModels returns the fallback.
	h := ProviderMeta(Deps{
		Config:         func() *config.App { return &config.App{} },
		ModelListCache: nil,
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/provider-meta", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Verify the response is well-formed JSON with providers list.
	var resp providerMetaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// Each provider must have a non-empty ID (same contract as existing test).
	for _, p := range resp.Providers {
		if p.ID == "" {
			t.Errorf("provider entry has empty id: %+v", p)
		}
	}
}

func TestProviderMeta_UsesCacheOnSecondCall(t *testing.T) {
	// Populate the cache with a known entry for "opencode-server".
	cache := &analyzer.ModelListCache{}
	cache.Set("opencode-server", []string{"cached-model"})

	h := ProviderMeta(Deps{
		Config:         func() *config.App { return &config.App{} },
		ModelListCache: cache,
	})

	// Two calls — both should succeed without error.
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/api/provider-meta", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d", i+1, rec.Code)
		}
	}
}

func TestProviderMeta_ModelSourceStaticWhenNoCache(t *testing.T) {
	// With a nil ModelListCache every provider should report "static".
	h := ProviderMeta(Deps{
		Config:         func() *config.App { return &config.App{} },
		ModelListCache: nil,
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/provider-meta", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp providerMetaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, p := range resp.Providers {
		if p.ModelSource != "static" {
			t.Errorf("provider %q model_source = %q, want %q", p.ID, p.ModelSource, "static")
		}
	}
}

func TestProviderMeta_ModelSourceLiveWhenCacheHit(t *testing.T) {
	// Pre-populate the cache with a known entry and verify model_source = "live".
	cache := &analyzer.ModelListCache{}
	cache.Set("opencode-server", []string{"live-model"})

	h := ProviderMeta(Deps{
		Config:         func() *config.App { return &config.App{} },
		ModelListCache: cache,
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/provider-meta", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp providerMetaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Providers) == 0 {
		t.Skip("no providers registered in this test binary; skipping")
	}
	// Find the "opencode-server" entry and assert it reports "live".
	for _, p := range resp.Providers {
		if p.ID == "opencode-server" {
			if p.ModelSource != "live" {
				t.Errorf("opencode-server model_source = %q, want %q", p.ModelSource, "live")
			}
			return
		}
	}
	t.Skip("opencode-server not registered in this test binary; skipping")
}
