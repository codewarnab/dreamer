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

// B4: two projects resolving to the same output directory (same name)
// would share state.json and todos.md; reject at config-load time.
// B5: an explicit ProviderBoundaryHeadroom of 0 must be respected, not
// silently overwritten with the default sentinel.
func TestLoadConfigPreservesExplicitZeroProviderBoundaryHeadroom(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, configPath, map[string]any{
		"projects": []map[string]string{
			{"name": "example", "path": projectDir},
		},
		"analyzer": map[string]any{
			"chunking": map[string]any{
				"provider_boundary_headroom": 0,
			},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	got := cfg.Analyzer.Chunking.ProviderBoundaryHeadroom
	if got == nil {
		t.Fatalf("explicit zero must survive as &0.0, got nil pointer (defaulted)")
	}
	if *got != 0 {
		t.Fatalf("ProviderBoundaryHeadroom = %v, want 0 (explicit disable)", *got)
	}
}

// B5: an omitted ProviderBoundaryHeadroom must still get the documented default.
func TestLoadConfigDefaultsProviderBoundaryHeadroomWhenOmitted(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, configPath, map[string]any{
		"projects": []map[string]string{
			{"name": "example", "path": projectDir},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	got := cfg.Analyzer.Chunking.ProviderBoundaryHeadroom
	if got == nil {
		t.Fatalf("omitted headroom must be defaulted to %v, got nil pointer", DefaultProviderBoundaryHeadroom)
	}
	if *got != DefaultProviderBoundaryHeadroom {
		t.Fatalf("ProviderBoundaryHeadroom = %v, want %v", *got, DefaultProviderBoundaryHeadroom)
	}
}

func TestLoadConfigRejectsDuplicateProjectNames(t *testing.T) {
	projectA := t.TempDir()
	projectB := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, configPath, map[string]any{
		"projects": []map[string]string{
			{"name": "foo", "path": projectA},
			{"name": "foo", "path": projectB},
		},
	})

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatalf("LoadConfig expected error for duplicate project name")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %q, want duplicate-name validation error", err)
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

func TestLoadConfigDefaultsSinceAndRecordsNotice(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, configPath, map[string]any{
		"projects": []map[string]string{
			{"name": "blank-since", "path": projectDir},
			{"name": "explicit-since", "path": projectDir, "since": "6h"},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got, want := cfg.Projects[0].Since, DefaultSince; got != want {
		t.Fatalf("Projects[0].Since = %q, want %q", got, want)
	}
	if got, want := cfg.Projects[1].Since, "6h"; got != want {
		t.Fatalf("Projects[1].Since = %q, want %q (explicit value preserved)", got, want)
	}
	if got, want := len(cfg.Notices.DefaultedSince), 1; got != want {
		t.Fatalf("len(Notices.DefaultedSince) = %d, want %d", got, want)
	}
	if got, want := cfg.Notices.DefaultedSince[0], "blank-since"; got != want {
		t.Fatalf("Notices.DefaultedSince[0] = %q, want %q", got, want)
	}
}

func TestLoadConfigDefaultsAnalyzerExecutionAndChunking(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigFile(t, configPath, map[string]any{
		"projects": []map[string]string{
			{"name": "p", "path": projectDir, "since": "1h"},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got, want := cfg.Analyzer.Execution.Mode, ExecutionModeSequential; got != want {
		t.Fatalf("Execution.Mode = %q, want %q", got, want)
	}
	if got, want := cfg.Analyzer.Execution.MaxConcurrency, 0; got != want {
		t.Fatalf("Execution.MaxConcurrency = %d, want %d", got, want)
	}
	if got, want := cfg.Analyzer.Chunking.MaxChunkBytes, DefaultMaxChunkBytes; got != want {
		t.Fatalf("Chunking.MaxChunkBytes = %d, want %d", got, want)
	}
	if got := cfg.Analyzer.Chunking.ProviderBoundaryHeadroom; got == nil || *got != DefaultProviderBoundaryHeadroom {
		t.Fatalf("Chunking.ProviderBoundaryHeadroom = %v, want %v", got, DefaultProviderBoundaryHeadroom)
	}
}

func TestIsLifetimeSince(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"lifetime", true},
		{"Lifetime", true},
		{"LIFETIME", true},
		{"  lifetime  ", true},
		{"24h", false},
		{"", false},
		{"life", false},
	}
	for _, tc := range tests {
		if got := IsLifetimeSince(tc.value); got != tc.want {
			t.Fatalf("IsLifetimeSince(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestResolveProviderConfigPicksCLIOverProjectOverGlobal(t *testing.T) {
	cliBlock := ProviderBlock{Model: "gpt-cli"}
	projectBlock := ProviderBlock{Model: "gpt-project"}
	cfg := &Config{
		DefaultProvider: "gemini-cli",
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
	if id != "gemini-cli" {
		t.Fatalf("id = %q, want gemini-cli (global default)", id)
	}
}

// TestDefaultProviderIDIsOpenClaudeCLI pins the v1.5 default-provider choice
// to openclaude-cli so accidental flips back to copilot-sdk get caught.
func TestDefaultProviderIDIsOpenClaudeCLI(t *testing.T) {
	if got, want := DefaultProviderID, "openclaude-cli"; got != want {
		t.Fatalf("DefaultProviderID = %q, want %q", got, want)
	}
}

// TestDefaultProviderIDFallsBackToOpenClaudeCLI exercises the
// applyDefaults branch that fills an empty `default_provider`.
func TestDefaultProviderIDFallsBackToOpenClaudeCLI(t *testing.T) {
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
	if got, want := cfg.DefaultProvider, "openclaude-cli"; got != want {
		t.Fatalf("DefaultProvider = %q, want %q", got, want)
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

func TestWebConfig_DefaultsWhenUnset(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.Web.Enabled == nil || *cfg.Web.Enabled != true {
		t.Fatalf("Web.Enabled = %v, want true", cfg.Web.Enabled)
	}
	if cfg.Web.Port != 7777 {
		t.Fatalf("Web.Port = %d, want 7777", cfg.Web.Port)
	}
	if cfg.Web.Host != "127.0.0.1" {
		t.Fatalf("Web.Host = %q, want 127.0.0.1", cfg.Web.Host)
	}
	if cfg.Web.LogTailKB != 256 {
		t.Fatalf("Web.LogTailKB = %d, want 256", cfg.Web.LogTailKB)
	}
}

func TestWebConfig_ExplicitFalseEnabledSurvives(t *testing.T) {
	f := false
	cfg := &Config{Web: WebConfig{Enabled: &f}}
	applyDefaults(cfg)
	if cfg.Web.Enabled == nil || *cfg.Web.Enabled != false {
		t.Fatalf("Web.Enabled = %v, want false (preserved)", cfg.Web.Enabled)
	}
}

func TestValidate_RejectsNonLoopbackWebHost(t *testing.T) {
	cases := []string{"0.0.0.0", "192.168.1.1", "example.com"}
	for _, host := range cases {
		t.Run(host, func(t *testing.T) {
			cfg := &Config{
				Daemon: DaemonConfig{FrequencySeconds: 60, OutputRoot: t.TempDir()},
				Web:    WebConfig{Host: host, Port: 7777, LogTailKB: 1},
			}
			applyDefaults(cfg)
			err := validateConfig(cfg)
			if err == nil {
				t.Fatalf("validate accepted host %q, want loopback error", host)
			}
			if !strings.Contains(err.Error(), "loopback") {
				t.Fatalf("validate error %v, want mention of loopback", err)
			}
		})
	}
}

func TestValidate_AcceptsLoopbackWebHost(t *testing.T) {
	cases := []string{"127.0.0.1", "localhost", "::1"}
	for _, host := range cases {
		t.Run(host, func(t *testing.T) {
			cfg := &Config{
				Daemon: DaemonConfig{FrequencySeconds: 60, OutputRoot: t.TempDir()},
				Web:    WebConfig{Host: host, Port: 7777, LogTailKB: 1},
			}
			applyDefaults(cfg)
			if err := validateConfig(cfg); err != nil {
				t.Fatalf("validate rejected loopback %q: %v", host, err)
			}
		})
	}
}

func TestConfigNotices_OverlayFieldsExist(t *testing.T) {
	n := ConfigNotices{}
	n.OverlayApplied = true
	n.OverlayParseError = "boom"
	n.RestartRequired = []string{"web.port"}
	if !n.OverlayApplied || n.OverlayParseError != "boom" || len(n.RestartRequired) != 1 {
		t.Fatalf("notice fields missing or wrong: %+v", n)
	}
}

func TestValidateProjectName_AcceptsValid(t *testing.T) {
	for _, name := range []string{"my-project", "alpha1", "hello_world"} {
		if err := ValidateProjectName(name); err != nil {
			t.Fatalf("ValidateProjectName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateProjectName_RejectsEmpty(t *testing.T) {
	if err := ValidateProjectName(""); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestValidateProjectName_RejectsPathSeparators(t *testing.T) {
	for _, name := range []string{"foo/bar", `foo\bar`} {
		if err := ValidateProjectName(name); err == nil {
			t.Fatalf("expected error for %q", name)
		}
	}
}

func TestValidateProjectName_RejectsDotDot(t *testing.T) {
	for _, name := range []string{".", ".."} {
		if err := ValidateProjectName(name); err == nil {
			t.Fatalf("expected error for %q", name)
		}
	}
}

func TestValidateProjectName_RejectsWindowsReserved(t *testing.T) {
	reserved := []string{
		"CON", "con", "Con", "PRN", "prn", "AUX", "aux",
		"NUL", "nul", "COM1", "com1", "LPT9", "lpt9",
		"CON.txt", "COM3.log",
	}
	for _, name := range reserved {
		if err := ValidateProjectName(name); err == nil {
			t.Fatalf("expected error for Windows reserved name %q", name)
		}
	}
}

func TestIsWindowsReservedName(t *testing.T) {
	reserved := []string{
		"CON", "PRN", "AUX", "NUL",
		"COM0", "COM9.txt", "LPT0", "LPT9.txt",
		"CONIN$", "CONOUT$",
		"CON..", "CON...", "CON. . ", // trailing-dot variants
	}
	for _, name := range reserved {
		if !isWindowsReservedName(name) {
			t.Errorf("%q should be reserved", name)
		}
	}
	notReserved := []string{
		"CONSOLE", "com10", "LPT10", "COM", "LPT", "hello",
	}
	for _, name := range notReserved {
		if isWindowsReservedName(name) {
			t.Errorf("%q should not be reserved", name)
		}
	}
}
