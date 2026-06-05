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

func TestBuildConfigYAML_AdvancedFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pdir := t.TempDir()
	a := setupAnswers{
		provider: "openclaude-cli", model: "mimo-v2.5-pro", frequency: 3600, outputRoot: dir,
		logLevel: "debug", ruleTimeout: 90, parallel: true, maxConcurrency: 4, maxChunkBytes: 600000,
		firstProject: true, projectPath: pdir, projectName: "p", projectSince: "7d",
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
	if cfg.Logging.Level != "debug" {
		t.Fatalf("level %q", cfg.Logging.Level)
	}
	if cfg.Analyzer.RuleTimeoutSeconds != 90 {
		t.Fatalf("ruletimeout %d", cfg.Analyzer.RuleTimeoutSeconds)
	}
	if cfg.Analyzer.Execution.Mode != "parallel" {
		t.Fatalf("mode %q", cfg.Analyzer.Execution.Mode)
	}
	if cfg.Analyzer.Execution.MaxConcurrency != 4 {
		t.Fatalf("conc %d", cfg.Analyzer.Execution.MaxConcurrency)
	}
	if cfg.Analyzer.Chunking.MaxChunkBytes != 600000 {
		t.Fatalf("chunkbytes %d", cfg.Analyzer.Chunking.MaxChunkBytes)
	}
	if len(cfg.Projects) != 1 || cfg.Projects[0].Path != pdir {
		t.Fatalf("projects = %+v", cfg.Projects)
	}
	if cfg.Projects[0].Since != "7d" {
		t.Fatalf("since %q", cfg.Projects[0].Since)
	}
}

func TestPrefillFromConfig_PullsAllFields(t *testing.T) {
	pdir := t.TempDir()
	prior := &config.App{
		DefaultProvider: "openclaude-cli",
		Daemon:          config.DaemonConfig{FrequencySeconds: 1800, OutputRoot: "/tmp/out"},
		Logging:         config.LoggingConfig{Level: "debug"},
		Providers: map[string]config.ProviderBlock{
			"openclaude-cli": {Model: "mimo-v2.5-pro"},
		},
		Analyzer: config.AnalyzerConfig{
			RuleTimeoutSeconds: 90,
			Execution:          config.ExecutionConfig{Mode: "parallel", MaxConcurrency: 4},
			Chunking:           config.ChunkingConfig{MaxChunkBytes: 600000},
		},
		Projects: []config.ProjectConfig{{Name: "p", Path: pdir, Since: "7d"}},
	}
	got := prefillFromConfig(prior)
	if got.provider != "openclaude-cli" {
		t.Fatalf("provider %q", got.provider)
	}
	if got.model != "mimo-v2.5-pro" {
		t.Fatalf("model %q", got.model)
	}
	if got.frequency != 1800 {
		t.Fatalf("freq %d", got.frequency)
	}
	if got.outputRoot != "/tmp/out" {
		t.Fatalf("outRoot %q", got.outputRoot)
	}
	if got.logLevel != "debug" {
		t.Fatalf("level %q", got.logLevel)
	}
	if got.ruleTimeout != 90 {
		t.Fatalf("timeout %d", got.ruleTimeout)
	}
	if !got.parallel {
		t.Fatalf("parallel should be true")
	}
	if got.maxConcurrency != 4 {
		t.Fatalf("conc %d", got.maxConcurrency)
	}
	if got.maxChunkBytes != 600000 {
		t.Fatalf("chunkbytes %d", got.maxChunkBytes)
	}
	if !got.firstProject || got.projectPath != pdir || got.projectName != "p" || got.projectSince != "7d" {
		t.Fatalf("project mismatch: %+v", got)
	}
}

func TestPrefillFromConfig_NilReturnsDefaults(t *testing.T) {
	got := prefillFromConfig(nil)
	if got.provider != config.DefaultProviderID {
		t.Fatalf("provider %q", got.provider)
	}
	if got.frequency != 3600 {
		t.Fatalf("freq %d", got.frequency)
	}
	if got.logLevel != config.DefaultLogLevel {
		t.Fatalf("level %q", got.logLevel)
	}
	if got.ruleTimeout != 120 {
		t.Fatalf("timeout %d", got.ruleTimeout)
	}
	if got.firstProject {
		t.Fatalf("firstProject should be false")
	}
}

func TestNewSetupCommand_NonInteractiveErrors(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	_, stderr, err := executeRootCommand("setup", "--non-interactive")
	if err == nil {
		t.Fatalf("expected error")
	}
	// Styled output goes to stderr; the sentinel carries the plain flag message.
	if !strings.Contains(stderr, "provider") && !strings.Contains(err.Error(), "provider") {
		t.Fatalf("wrong error: %v / stderr: %s", err, stderr)
	}
}
