package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigAppliesDefaults(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")

	writeConfigFile(t, configPath, map[string]any{
		"projects": []map[string]string{
			{"name": "example", "path": projectDir},
		},
		"analyzer": map[string]any{},
		"daemon":   map[string]any{},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if got, want := cfg.Daemon.FrequencySeconds, DefaultFrequencySeconds; got != want {
		t.Fatalf("FrequencySeconds = %d, want %d", got, want)
	}
	if got, want := cfg.Logging.Level, DefaultLogLevel; got != want {
		t.Fatalf("Logging.Level = %q, want %q", got, want)
	}
	if got, want := cfg.DefaultProvider, DefaultProviderID; got != want {
		t.Fatalf("DefaultProvider = %q, want %q", got, want)
	}

	root, err := UserConfigRoot()
	if err != nil {
		t.Fatalf("resolve user config root: %v", err)
	}
	if got, want := cfg.Daemon.OutputRoot, root; got != want {
		t.Fatalf("OutputRoot = %q, want %q", got, want)
	}
	if cfg.Analyzer.Rules == nil {
		t.Fatalf("Analyzer.Rules should not be nil")
	}
	if cfg.Analyzer.UseLoggedInUser == nil || !*cfg.Analyzer.UseLoggedInUser {
		t.Fatalf("Analyzer.UseLoggedInUser should default to true")
	}
	if cfg.Analyzer.AutoStart == nil || *cfg.Analyzer.AutoStart {
		t.Fatalf("Analyzer.AutoStart should default to false")
	}
}

func TestLoadConfigRejectsEmptyProjectName(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, configPath, map[string]any{
		"projects": []map[string]string{
			{"name": "  ", "path": projectDir},
		},
	})

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatalf("LoadConfig expected error for empty project name")
	}
}

func TestLoadConfigRejectsNonAbsoluteProjectPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, configPath, map[string]any{
		"projects": []map[string]string{
			{"name": "example", "path": "relative/path"},
		},
	})

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatalf("LoadConfig expected error for non-absolute project path")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("error = %q, want absolute path validation error", err)
	}
}

func TestLoadConfigRejectsMissingProjectPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	missingPath := filepath.Join(t.TempDir(), "missing-project-dir")
	writeConfigFile(t, configPath, map[string]any{
		"projects": []map[string]string{
			{"name": "example", "path": missingPath},
		},
	})

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatalf("LoadConfig expected error for missing project path")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("error = %q, want path validation failure", err)
	}
}

func TestLoadConfigSupportsYAML(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	yamlContent := `
projects:
  - name: example
    path: ` + projectDir + `
analyzer:
  rules: {}
logging:
  level: debug
daemon:
  frequency_seconds: 60
  output_root: ` + filepath.ToSlash(t.TempDir()) + `
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write yaml config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if got, want := len(cfg.Projects), 1; got != want {
		t.Fatalf("len(cfg.Projects) = %d, want %d", got, want)
	}
	if got, want := cfg.Projects[0].Name, "example"; got != want {
		t.Fatalf("project name = %q, want %q", got, want)
	}
	if got, want := cfg.Daemon.FrequencySeconds, 60; got != want {
		t.Fatalf("frequency_seconds = %d, want %d", got, want)
	}
	if got, want := cfg.Logging.Level, "debug"; got != want {
		t.Fatalf("logging.level = %q, want %q", got, want)
	}
}

func TestResolveProviderConfigPicksCLIOverProjectOverGlobal(t *testing.T) {
	cliBlock := ProviderBlock{Model: "gpt-cli"}
	projectBlock := ProviderBlock{Model: "gpt-project"}
	cfg := &Config{
		DefaultProvider: "gemini-sdk",
		Providers: map[string]ProviderBlock{
			"copilot-sdk": cliBlock,
			"claude-cli":  projectBlock,
		},
	}
	project := &ProjectFileConfig{Provider: "claude-cli"}

	id, block := cfg.ResolveProviderConfig(project, "")
	if id != "claude-cli" {
		t.Fatalf("id = %q, want claude-cli (project override)", id)
	}
	if block.Model != "gpt-project" {
		t.Fatalf("block.Model = %q, want gpt-project", block.Model)
	}

	id, _ = cfg.ResolveProviderConfig(project, "copilot-sdk")
	if id != "copilot-sdk" {
		t.Fatalf("id = %q, want copilot-sdk (cli override)", id)
	}

	id, _ = cfg.ResolveProviderConfig(nil, "")
	if id != "gemini-sdk" {
		t.Fatalf("id = %q, want gemini-sdk (global default)", id)
	}
}

func writeConfigFile(t *testing.T, path string, cfg map[string]any) {
	t.Helper()

	payload, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}
