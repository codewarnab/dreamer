package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"dreamer/internal/config"
)

func TestSettings_GETSanitizesProviderEnv(t *testing.T) {
	cfg := &config.Config{
		Providers: map[string]config.ProviderBlock{
			"openclaude-cli": {Env: map[string]string{
				"ANTHROPIC_API_KEY": "sk-secret",
				"HTTP_PROXY":        "http://proxy",
			}},
		},
	}
	deps := Deps{Config: func() *config.Config { return cfg }}
	r := httptest.NewRequest("GET", "/api/settings", nil)
	w := httptest.NewRecorder()
	Settings(deps)(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	env := got["providers"].(map[string]any)["openclaude-cli"].(map[string]any)["env"].(map[string]any)
	if env["ANTHROPIC_API_KEY"] != "***redacted***" {
		t.Fatalf("API_KEY not redacted: %v", env["ANTHROPIC_API_KEY"])
	}
	if env["HTTP_PROXY"] != "http://proxy" {
		t.Fatalf("HTTP_PROXY clobbered: %v", env["HTTP_PROXY"])
	}
}

func TestSettings_PUTPartialMergePreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "ui-overrides.yaml")
	if err := os.WriteFile(overlayPath, []byte("logging:\n  level: info\nweb:\n  port: 7777\n"), 0o644); err != nil {
		t.Fatalf("seed overlay: %v", err)
	}
	deps := Deps{
		Config:      func() *config.Config { return &config.Config{} },
		OverlayPath: func() string { return overlayPath },
	}
	body := strings.NewReader(`{"logging":{"level":"debug"}}`)
	r := httptest.NewRequest("PUT", "/api/settings", body)
	w := httptest.NewRecorder()
	Settings(deps)(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body)
	}
	data, _ := os.ReadFile(overlayPath)
	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal overlay: %v", err)
	}
	if got["logging"].(map[string]any)["level"] != "debug" {
		t.Fatalf("level not updated: %+v", got)
	}
	if got["web"].(map[string]any)["port"].(int) != 7777 {
		t.Fatalf("web.port clobbered: %+v", got)
	}
}

func TestSettings_PUTNullClearsKey(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "ui-overrides.yaml")
	if err := os.WriteFile(overlayPath, []byte("default_provider: claude-cli\nlogging:\n  level: debug\n"), 0o644); err != nil {
		t.Fatalf("seed overlay: %v", err)
	}
	deps := Deps{
		Config:      func() *config.Config { return &config.Config{} },
		OverlayPath: func() string { return overlayPath },
	}
	body := strings.NewReader(`{"default_provider": null}`)
	r := httptest.NewRequest("PUT", "/api/settings", body)
	w := httptest.NewRecorder()
	Settings(deps)(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body)
	}
	data, _ := os.ReadFile(overlayPath)
	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal overlay: %v", err)
	}
	if _, ok := got["default_provider"]; ok {
		t.Fatalf("default_provider not cleared: %+v", got)
	}
	if got["logging"].(map[string]any)["level"] != "debug" {
		t.Fatalf("logging.level lost: %+v", got)
	}
}

func TestSettings_PUTCreatesMissingOverlay(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "ui-overrides.yaml")
	deps := Deps{
		Config:      func() *config.Config { return &config.Config{} },
		OverlayPath: func() string { return overlayPath },
	}
	body := strings.NewReader(`{"logging":{"level":"warn"}}`)
	r := httptest.NewRequest("PUT", "/api/settings", body)
	w := httptest.NewRecorder()
	Settings(deps)(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body)
	}
	if _, err := os.Stat(overlayPath); err != nil {
		t.Fatalf("overlay file not created: %v", err)
	}
}
