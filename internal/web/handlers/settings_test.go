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
	cfg := &config.App{
		Providers: map[string]config.ProviderBlock{
			"openclaude-cli": {
				Password: "super-secret-password",
				Env: map[string]string{
					"ANTHROPIC_API_KEY": "sk-secret",
					"HTTP_PROXY":        "http://proxy",
				},
			},
		},
	}
	deps := Deps{Config: func() *config.App { return cfg }}
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
	provider := got["providers"].(map[string]any)["openclaude-cli"].(map[string]any)
	if provider["password"] != "***redacted***" {
		t.Fatalf("Password not redacted: %v", provider["password"])
	}
	env := provider["env"].(map[string]any)
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
		Config:      func() *config.App { return &config.App{} },
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
		Config:      func() *config.App { return &config.App{} },
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
		Config:      func() *config.App { return &config.App{} },
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

// TestSettings_PUTRulePromptOverrideRoundTrip locks in the per-category prompt
// editing contract: a prompt template writes to the overlay, sending null
// clears it, and the sibling severity survives both — relying on the recursive
// merge in mergePartial.
func TestSettings_PUTRulePromptOverrideRoundTrip(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "ui-overrides.yaml")
	if err := os.WriteFile(overlayPath, []byte("analyzer:\n  rules:\n    test:\n      severity: high\n"), 0o644); err != nil {
		t.Fatalf("seed overlay: %v", err)
	}
	deps := Deps{
		Config:      func() *config.App { return &config.App{} },
		OverlayPath: func() string { return overlayPath },
	}

	// Write a mistake prompt override; severity must survive.
	body := strings.NewReader(`{"analyzer":{"rules":{"test":{"mistake_prompt_template":"custom prompt"}}}}`)
	r := httptest.NewRequest("PUT", "/api/settings", body)
	w := httptest.NewRecorder()
	Settings(deps)(w, r)
	if w.Code != 200 {
		t.Fatalf("write status %d body=%s", w.Code, w.Body)
	}
	rule := readRuleOverlay(t, overlayPath)
	if rule["mistake_prompt_template"] != "custom prompt" {
		t.Fatalf("mistake_prompt_template not written: %+v", rule)
	}
	if rule["severity"] != "high" {
		t.Fatalf("severity clobbered by prompt write: %+v", rule)
	}

	// Clear the override with null; severity must still survive.
	body = strings.NewReader(`{"analyzer":{"rules":{"test":{"mistake_prompt_template":null}}}}`)
	r = httptest.NewRequest("PUT", "/api/settings", body)
	w = httptest.NewRecorder()
	Settings(deps)(w, r)
	if w.Code != 200 {
		t.Fatalf("clear status %d body=%s", w.Code, w.Body)
	}
	rule = readRuleOverlay(t, overlayPath)
	if _, ok := rule["mistake_prompt_template"]; ok {
		t.Fatalf("mistake_prompt_template not cleared: %+v", rule)
	}
	if rule["severity"] != "high" {
		t.Fatalf("severity lost on clear: %+v", rule)
	}
}

func readRuleOverlay(t *testing.T, overlayPath string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal overlay: %v", err)
	}
	analyzerBlock, ok := got["analyzer"].(map[string]any)
	if !ok {
		t.Fatalf("missing analyzer block: %+v", got)
	}
	rules, ok := analyzerBlock["rules"].(map[string]any)
	if !ok {
		t.Fatalf("missing rules block: %+v", got)
	}
	rule, ok := rules["test"].(map[string]any)
	if !ok {
		t.Fatalf("missing test rule: %+v", got)
	}
	return rule
}

func TestSettings_PUTRejectsRestrictedFields(t *testing.T) {
	for _, field := range []string{"command", "env", "base_url", "cli_url"} {
		t.Run(field, func(t *testing.T) {
			dir := t.TempDir()
			overlayPath := filepath.Join(dir, "ui-overrides.yaml")
			deps := Deps{
				Config:      func() *config.App { return &config.App{} },
				OverlayPath: func() string { return overlayPath },
			}
			var payload string
			if field == "command" {
				payload = `{"providers":{"openclaude-cli":{"command":["rm","-rf","/"]}}}`
			} else if field == "env" {
				payload = `{"providers":{"openclaude-cli":{"env":{"PATH":"/bin"}}}}`
			} else {
				payload = `{"providers":{"openclaude-cli":{"` + field + `":"http://evil.com"}}}`
			}
			body := strings.NewReader(payload)
			r := httptest.NewRequest("PUT", "/api/settings", body)
			w := httptest.NewRecorder()
			Settings(deps)(w, r)
			if w.Code != 400 {
				t.Fatalf("expected status 400, got %d body=%s", w.Code, w.Body)
			}
			expectedErr := "modifying " + field + " for provider"
			if !strings.Contains(w.Body.String(), expectedErr) {
				t.Fatalf("expected error message about provider %s, got=%s", field, w.Body)
			}
			if _, err := os.Stat(overlayPath); err == nil {
				data, _ := os.ReadFile(overlayPath)
				if strings.Contains(string(data), field) {
					t.Fatalf("%s was persisted to overlay file: %s", field, string(data))
				}
			}
		})
	}
}
