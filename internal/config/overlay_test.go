package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func TestLoadConfigWithOverlay_NoOverlay_LoadsBase(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	writeYAML(t, base, "default_provider: copilot-sdk\ndaemon:\n  frequency_seconds: 60\n  output_root: "+dir+"\n")
	cfg, err := LoadConfigWithOverlay(base, filepath.Join(dir, "ui-overrides.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DefaultProvider != "copilot-sdk" {
		t.Fatalf("DefaultProvider = %q, want copilot-sdk", cfg.DefaultProvider)
	}
	if cfg.Notices.OverlayApplied {
		t.Fatalf("OverlayApplied should be false when overlay missing")
	}
}

func TestLoadConfigWithOverlay_ScalarOverlayWins(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	overlay := filepath.Join(dir, "ui-overrides.yaml")
	writeYAML(t, base, "default_provider: copilot-sdk\ndaemon:\n  frequency_seconds: 60\n  output_root: "+dir+"\n")
	writeYAML(t, overlay, "default_provider: claude-cli\n")
	cfg, err := LoadConfigWithOverlay(base, overlay)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DefaultProvider != "claude-cli" {
		t.Fatalf("DefaultProvider = %q, want claude-cli (overlay)", cfg.DefaultProvider)
	}
	if !cfg.Notices.OverlayApplied {
		t.Fatalf("OverlayApplied should be true")
	}
}

func TestLoadConfigWithOverlay_ProvidersMerge_PerKey(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	overlay := filepath.Join(dir, "ui-overrides.yaml")
	writeYAML(t, base, `default_provider: copilot-sdk
daemon: {frequency_seconds: 60, output_root: `+dir+`}
providers:
  copilot-sdk: {model: gpt-5}
  claude-cli: {model: claude-old}
`)
	writeYAML(t, overlay, `providers:
  claude-cli: {model: claude-new}
`)
	cfg, err := LoadConfigWithOverlay(base, overlay)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Providers["copilot-sdk"].Model != "gpt-5" {
		t.Fatalf("copilot-sdk.model = %q, want gpt-5 (preserved)", cfg.Providers["copilot-sdk"].Model)
	}
	if cfg.Providers["claude-cli"].Model != "claude-new" {
		t.Fatalf("claude-cli.model = %q, want claude-new (overlay)", cfg.Providers["claude-cli"].Model)
	}
}

func TestLoadConfigWithOverlay_ProjectsListReplaces(t *testing.T) {
	dir := t.TempDir()
	pA := t.TempDir()
	pB := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	overlay := filepath.Join(dir, "ui-overrides.yaml")
	writeYAML(t, base, `default_provider: copilot-sdk
daemon: {frequency_seconds: 60, output_root: `+dir+`}
projects:
  - {name: a, path: `+pA+`}
`)
	writeYAML(t, overlay, `projects:
  - {name: b, path: `+pB+`}
`)
	cfg, err := LoadConfigWithOverlay(base, overlay)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Projects) != 1 || cfg.Projects[0].Name != "b" {
		t.Fatalf("projects = %+v, want [{b}]", cfg.Projects)
	}
}

func TestLoadConfigWithOverlay_ParseErrorSetsNotice(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	overlay := filepath.Join(dir, "ui-overrides.yaml")
	writeYAML(t, base, "default_provider: copilot-sdk\ndaemon: {frequency_seconds: 60, output_root: "+dir+"}\n")
	writeYAML(t, overlay, "this: is: : not: valid: yaml: { ][")
	cfg, err := LoadConfigWithOverlay(base, overlay)
	if err != nil {
		t.Fatalf("LoadConfigWithOverlay must not error on bad overlay; got %v", err)
	}
	if cfg.Notices.OverlayParseError == "" {
		t.Fatalf("OverlayParseError should be set on bad overlay")
	}
	if !strings.Contains(cfg.Notices.OverlayParseError, "overlay") &&
		!strings.Contains(cfg.Notices.OverlayParseError, "yaml") {
		t.Fatalf("OverlayParseError %q should mention overlay or yaml", cfg.Notices.OverlayParseError)
	}
}
