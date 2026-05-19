package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/config"
)

func TestBuildConfigYAML_RoundTripsViaLoadConfig(t *testing.T) {
	dir := t.TempDir()
	a := setupAnswers{
		provider:       "openclaude-cli",
		model:          "mimo-v2.5-pro",
		frequency:      3600,
		outputRoot:     dir,
		startupInstall: false,
	}
	data := buildConfigYAML(a)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DefaultProvider != "openclaude-cli" {
		t.Fatalf("provider %q", cfg.DefaultProvider)
	}
	if cfg.Daemon.FrequencySeconds != 3600 {
		t.Fatalf("frequency %d", cfg.Daemon.FrequencySeconds)
	}
	if cfg.Providers["openclaude-cli"].Model != "mimo-v2.5-pro" {
		t.Fatalf("model %q", cfg.Providers["openclaude-cli"].Model)
	}
}

func TestDefaultModelsFor_ReturnsKnownDefault(t *testing.T) {
	got := defaultModelsFor("openclaude-cli")
	if len(got) == 0 {
		t.Fatalf("got empty list")
	}
	if got[0] != "mimo-v2.5-pro" {
		t.Fatalf("model[0] = %q want mimo-v2.5-pro", got[0])
	}
}

func TestDefaultModelsFor_UnknownProviderFallsBackToDefault(t *testing.T) {
	got := defaultModelsFor("bogus-provider")
	if len(got) != 1 || got[0] != config.DefaultModel {
		t.Fatalf("unexpected fallback: %#v", got)
	}
}

func TestNewSetupCommand_NonInteractiveErrors(t *testing.T) {
	cmd := newSetupCommand()
	cmd.SetArgs([]string{"--non-interactive"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "non-interactive") {
		t.Fatalf("wrong error: %v", err)
	}
}
