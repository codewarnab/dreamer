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

func TestExpandUserHome_EmptyString(t *testing.T) {
	got, err := ExpandUserHome("")
	if err != nil {
		t.Fatalf("ExpandUserHome(\"\"): %v", err)
	}
	if got != "" {
		t.Fatalf("ExpandUserHome(\"\") = %q, want empty", got)
	}
}

func TestExpandUserHome_PathWithoutTilde(t *testing.T) {
	path := "/some/absolute/path"
	got, err := ExpandUserHome(path)
	if err != nil {
		t.Fatalf("ExpandUserHome(%q): %v", path, err)
	}
	if got != path {
		t.Fatalf("ExpandUserHome(%q) = %q, want unchanged", path, got)
	}
}

func TestExpandUserHome_TildeOnly(t *testing.T) {
	got, err := ExpandUserHome("~")
	if err != nil {
		t.Fatalf("ExpandUserHome(\"~\"): %v", err)
	}
	home, _ := os.UserHomeDir()
	if got != home {
		t.Fatalf("ExpandUserHome(\"~\") = %q, want %q", got, home)
	}
}

func TestExpandUserHome_TildeSlash(t *testing.T) {
	got, err := ExpandUserHome("~/projects/foo")
	if err != nil {
		t.Fatalf("ExpandUserHome(\"~/projects/foo\"): %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "projects/foo")
	if got != want {
		t.Fatalf("ExpandUserHome(\"~/projects/foo\") = %q, want %q", got, want)
	}
}

func TestExpandUserHome_TildeBackslash(t *testing.T) {
	got, err := ExpandUserHome("~\\projects\\foo")
	if err != nil {
		t.Fatalf("ExpandUserHome(\"~\\\\projects\\\\foo\"): %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "projects\\foo")
	if got != want {
		t.Fatalf("ExpandUserHome = %q, want %q", got, want)
	}
}

func TestProjectConfigPath(t *testing.T) {
	got := ProjectConfigPath("/home/user/project")
	want := filepath.Join("/home/user/project", ".dreamer", "config.yaml")
	if got != want {
		t.Fatalf("ProjectConfigPath = %q, want %q", got, want)
	}
}

func TestProjectRulesPath(t *testing.T) {
	got := ProjectRulesPath("/home/user/project", "lint-rule")
	want := filepath.Join("/home/user/project", ".dreamer", "rules", "lint-rule.yaml")
	if got != want {
		t.Fatalf("ProjectRulesPath = %q, want %q", got, want)
	}
}

func TestConfigDirBase_UsesXDGWhenSet(t *testing.T) {
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	got, err := ConfigDirBase()
	if err != nil {
		t.Fatalf("ConfigDirBase: %v", err)
	}
	if got != xdg {
		t.Fatalf("ConfigDirBase = %q, want %q (XDG override)", got, xdg)
	}
}

func TestUserConfigRoot_AppendsDreamer(t *testing.T) {
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	got, err := UserConfigRoot()
	if err != nil {
		t.Fatalf("UserConfigRoot: %v", err)
	}
	want := filepath.Join(xdg, "dreamer")
	if got != want {
		t.Fatalf("UserConfigRoot = %q, want %q", got, want)
	}
}

func TestLoadConfig_EmptyPath(t *testing.T) {
	_, err := LoadConfig("")
	if err == nil {
		t.Fatal("LoadConfig(\"\") expected error")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Fatalf("error = %q, want 'required'", err)
	}
}

func TestLoadConfig_NonexistentFile(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "no-such-file.yaml"))
	if err == nil {
		t.Fatal("LoadConfig with missing file expected error")
	}
}

func TestLoadProjectFileConfig_MissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadProjectFileConfig(dir)
	if err != nil {
		t.Fatalf("LoadProjectFileConfig: %v", err)
	}
	if got.Provider != "" {
		t.Fatalf("Provider = %q, want empty for missing file", got.Provider)
	}
}

func TestLoadProjectFileConfig_ValidFile(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".dreamer")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "provider: claude-cli\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadProjectFileConfig(dir)
	if err != nil {
		t.Fatalf("LoadProjectFileConfig: %v", err)
	}
	if got.Provider != "claude-cli" {
		t.Fatalf("Provider = %q, want claude-cli", got.Provider)
	}
}

func TestResolveMaxDuration_ProjectOverride(t *testing.T) {
	cfg := &Config{
		Daemon:   DaemonConfig{MaxAnalysisDuration: "8h"},
		Projects: []ProjectConfig{{Name: "fast", MaxAnalysisDuration: "30m"}},
	}
	d, err := cfg.ResolveMaxDuration("fast")
	if err != nil {
		t.Fatalf("ResolveMaxDuration: %v", err)
	}
	if d.Minutes() != 30 {
		t.Fatalf("duration = %v, want 30m", d)
	}
}

func TestResolveMaxDuration_DaemonDefault(t *testing.T) {
	cfg := &Config{
		Daemon:   DaemonConfig{MaxAnalysisDuration: "4h"},
		Projects: []ProjectConfig{{Name: "normal"}},
	}
	d, err := cfg.ResolveMaxDuration("normal")
	if err != nil {
		t.Fatalf("ResolveMaxDuration: %v", err)
	}
	if d.Hours() != 4 {
		t.Fatalf("duration = %v, want 4h", d)
	}
}

func TestResolveMaxDuration_InvalidDuration(t *testing.T) {
	cfg := &Config{
		Daemon: DaemonConfig{MaxAnalysisDuration: "not-a-duration"},
	}
	_, err := cfg.ResolveMaxDuration("anything")
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestMergeProviderBlocks_OverlayWins(t *testing.T) {
	base := ProviderBlock{
		Model:       "gpt-5",
		CopilotHome: "/base/home",
	}
	override := ProviderBlock{
		Model: "claude-sonnet-4-5-20250929",
		Env:   map[string]string{"FOO": "bar"},
	}
	got := MergeProviderBlock(base, override)
	if got.Model != "claude-sonnet-4-5-20250929" {
		t.Fatalf("Model = %q, want claude-sonnet-4-5-20250929", got.Model)
	}
	if got.CopilotHome != "/base/home" {
		t.Fatalf("CopilotHome = %q, want /base/home (preserved from base)", got.CopilotHome)
	}
	if got.Env["FOO"] != "bar" {
		t.Fatalf("Env[FOO] = %q, want bar", got.Env["FOO"])
	}
}

func TestMergeProviderBlocks_NilOverrideEnvPreservesBase(t *testing.T) {
	base := ProviderBlock{
		Model: "gpt-5",
		Env:   map[string]string{"KEY": "val"},
	}
	override := ProviderBlock{
		Model: "new-model",
	}
	got := MergeProviderBlock(base, override)
	if got.Env["KEY"] != "val" {
		t.Fatalf("Env[KEY] = %q, want val (preserved from base)", got.Env["KEY"])
	}
}

func TestMergeProviderBlocks_UseLoggedInUserOverride(t *testing.T) {
	f := false
	base := ProviderBlock{}
	override := ProviderBlock{UseLoggedInUser: &f}
	got := MergeProviderBlock(base, override)
	if got.UseLoggedInUser == nil || *got.UseLoggedInUser != false {
		t.Fatalf("UseLoggedInUser = %v, want false", got.UseLoggedInUser)
	}
}

func TestMergeProviderBlocks_AutoStartOverride(t *testing.T) {
	tVal := true
	base := ProviderBlock{}
	override := ProviderBlock{AutoStart: &tVal}
	got := MergeProviderBlock(base, override)
	if got.AutoStart == nil || *got.AutoStart != true {
		t.Fatalf("AutoStart = %v, want true", got.AutoStart)
	}
}

func TestMergeProviderBlocks_CopilotHomeOverride(t *testing.T) {
	base := ProviderBlock{CopilotHome: "/old"}
	override := ProviderBlock{CopilotHome: "/new"}
	got := MergeProviderBlock(base, override)
	if got.CopilotHome != "/new" {
		t.Fatalf("CopilotHome = %q, want /new", got.CopilotHome)
	}
}

func TestMergeProviderBlocks_CLIURLOverride(t *testing.T) {
	base := ProviderBlock{CLIURL: "http://old"}
	override := ProviderBlock{CLIURL: "http://new"}
	got := MergeProviderBlock(base, override)
	if got.CLIURL != "http://new" {
		t.Fatalf("CLIURL = %q, want http://new", got.CLIURL)
	}
}

func TestMergeProviderBlocks_CommandOverride(t *testing.T) {
	base := ProviderBlock{Command: []string{"old"}}
	override := ProviderBlock{Command: []string{"new", "cmd"}}
	got := MergeProviderBlock(base, override)
	if len(got.Command) != 2 || got.Command[0] != "new" {
		t.Fatalf("Command = %v, want [new cmd]", got.Command)
	}
}

func TestMergeProviderBlocks_EnvMerge(t *testing.T) {
	base := ProviderBlock{Env: map[string]string{"A": "1", "B": "2"}}
	override := ProviderBlock{Env: map[string]string{"B": "override", "C": "3"}}
	got := MergeProviderBlock(base, override)
	if got.Env["A"] != "1" || got.Env["B"] != "override" || got.Env["C"] != "3" {
		t.Fatalf("Env = %v, want merged", got.Env)
	}
}

func TestMergeProviderBlocks_APIKeyEnvOverride(t *testing.T) {
	base := ProviderBlock{APIKeyEnv: "OLD_KEY"}
	override := ProviderBlock{APIKeyEnv: "NEW_KEY"}
	got := MergeProviderBlock(base, override)
	if got.APIKeyEnv != "NEW_KEY" {
		t.Fatalf("APIKeyEnv = %q, want NEW_KEY", got.APIKeyEnv)
	}
}

func TestMergeProviderBlocks_BaseURLOverride(t *testing.T) {
	base := ProviderBlock{BaseURL: "http://old"}
	override := ProviderBlock{BaseURL: "http://new"}
	got := MergeProviderBlock(base, override)
	if got.BaseURL != "http://new" {
		t.Fatalf("BaseURL = %q, want http://new", got.BaseURL)
	}
}

func TestMergeProviderBlocks_PasswordOverride(t *testing.T) {
	base := ProviderBlock{Password: "old"}
	override := ProviderBlock{Password: "new"}
	got := MergeProviderBlock(base, override)
	if got.Password != "new" {
		t.Fatalf("Password = %q, want new", got.Password)
	}
}

func TestMergeProviderBlocks_MaxInputTokensOverride(t *testing.T) {
	base := ProviderBlock{MaxInputTokens: 100}
	override := ProviderBlock{MaxInputTokens: 200}
	got := MergeProviderBlock(base, override)
	if got.MaxInputTokens != 200 {
		t.Fatalf("MaxInputTokens = %d, want 200", got.MaxInputTokens)
	}
}

func TestMergeProviderBlocks_SandboxOverride(t *testing.T) {
	mode := "true"
	base := ProviderBlock{}
	override := ProviderBlock{Sandbox: &mode}
	got := MergeProviderBlock(base, override)
	if got.Sandbox == nil || *got.Sandbox != "true" {
		t.Fatalf("Sandbox = %v, want true", got.Sandbox)
	}
}

func TestMergeProviderBlocks_EmptyOverridePreservesBase(t *testing.T) {
	base := ProviderBlock{
		Model:       "model",
		CopilotHome: "/home",
		CLIURL:      "http://url",
		APIKeyEnv:   "KEY",
		BaseURL:     "http://base",
		Password:    "pw",
		MaxInputTokens: 500,
		Command:     []string{"cmd"},
	}
	override := ProviderBlock{}
	got := MergeProviderBlock(base, override)
	if got.Model != "model" || got.CopilotHome != "/home" || got.CLIURL != "http://url" {
		t.Fatal("base fields not preserved")
	}
	if got.APIKeyEnv != "KEY" || got.BaseURL != "http://base" || got.Password != "pw" {
		t.Fatal("base fields not preserved (2)")
	}
	if got.MaxInputTokens != 500 || len(got.Command) != 1 {
		t.Fatal("base fields not preserved (3)")
	}
}

func TestValidateWebHost_IPv6Loopback(t *testing.T) {
	if err := validateWebHost("::1"); err != nil {
		t.Fatalf("validateWebHost(\"::1\"): %v", err)
	}
}

func TestValidateWebHost_Localhost(t *testing.T) {
	if err := validateWebHost("localhost"); err != nil {
		t.Fatalf("validateWebHost(\"localhost\"): %v", err)
	}
}

func TestResolveProviderConfig_FallbackToDefault(t *testing.T) {
	cfg := &Config{DefaultProvider: ""}
	id, _ := cfg.ResolveProviderConfig(nil, "")
	if id != DefaultProviderID {
		t.Fatalf("id = %q, want %q (default fallback)", id, DefaultProviderID)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(configPath, []byte("{{bad yaml"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadProjectFileConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".dreamer")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("{{bad"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadProjectFileConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestValidateConfig_RejectsInvalidProjectDuration(t *testing.T) {
	projectDir := t.TempDir()
	cfg := &Config{
		Projects: []ProjectConfig{
			{Name: "p", Path: projectDir, MaxAnalysisDuration: "bad"},
		},
		Daemon: DaemonConfig{FrequencySeconds: 60, OutputRoot: projectDir, MaxAnalysisDuration: "8h", JobHistoryRetention: "720h"},
	}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid project max_analysis_duration")
	}
}

func TestValidateConfig_Valid(t *testing.T) {
	projectDir := t.TempDir()
	cfg := &Config{
		Projects: []ProjectConfig{
			{Name: "p", Path: projectDir, MaxAnalysisDuration: "30m"},
		},
		Daemon: DaemonConfig{FrequencySeconds: 60, OutputRoot: projectDir, MaxAnalysisDuration: "8h", JobHistoryRetention: "720h"},
		Web:    WebConfig{Host: "127.0.0.1", Port: 7777, LogTailKB: 1},
	}
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("validateConfig: %v", err)
	}
}

func TestValidateConfig_RejectsEmptyProjectPath(t *testing.T) {
	cfg := &Config{
		Projects: []ProjectConfig{
			{Name: "p", Path: ""},
		},
		Daemon: DaemonConfig{FrequencySeconds: 60, OutputRoot: t.TempDir(), MaxAnalysisDuration: "8h", JobHistoryRetention: "720h"},
	}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for empty project path")
	}
}

func TestValidateConfig_RejectsRelativeOutputRoot(t *testing.T) {
	projectDir := t.TempDir()
	cfg := &Config{
		Projects: []ProjectConfig{
			{Name: "p", Path: projectDir},
		},
		Daemon: DaemonConfig{FrequencySeconds: 60, OutputRoot: "relative/path", MaxAnalysisDuration: "8h", JobHistoryRetention: "720h"},
	}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for relative output root")
	}
}

func TestValidateConfig_RejectsInvalidRetention(t *testing.T) {
	projectDir := t.TempDir()
	cfg := &Config{
		Projects: []ProjectConfig{
			{Name: "p", Path: projectDir},
		},
		Daemon: DaemonConfig{FrequencySeconds: 60, OutputRoot: projectDir, MaxAnalysisDuration: "8h", JobHistoryRetention: "bad"},
	}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid job_history_retention")
	}
}

func TestRemediationMessage_KnownProvider(t *testing.T) {
	msg := RemediationMessage("copilot-sdk")
	if msg == "" {
		t.Fatal("expected non-empty remediation message")
	}
}

func TestRemediationMessage_UnknownProvider(t *testing.T) {
	msg := RemediationMessage("unknown-provider")
	if !strings.Contains(msg, "unknown-provider") {
		t.Fatalf("expected provider name in message: %q", msg)
	}
}

func TestDefaultModelByProvider_AllProviders(t *testing.T) {
	allIDs := []ProviderID{
		ProviderCopilotSDK, ProviderCopilotACP,
		ProviderClaudeCLI, ProviderClaudeACP,
		ProviderGeminiCLI, ProviderGeminiACP,
		ProviderKiroACP, ProviderCodexCLI, ProviderCodexACP,
		ProviderOpenClaudeCLI, ProviderOpenCodeACP, ProviderOpenCodeServer,
		ProviderCodebuffSDK,
	}
	for _, id := range allIDs {
		if _, ok := DefaultModelByProvider[id]; !ok {
			t.Errorf("DefaultModelByProvider missing entry for %q", id)
		}
	}
}

func TestDefaultSandboxByProvider_AllProviders(t *testing.T) {
	allIDs := []ProviderID{
		ProviderCopilotSDK, ProviderCopilotACP,
		ProviderClaudeCLI, ProviderClaudeACP,
		ProviderGeminiCLI, ProviderGeminiACP,
		ProviderKiroACP, ProviderCodexCLI, ProviderCodexACP,
		ProviderOpenClaudeCLI, ProviderOpenCodeACP, ProviderOpenCodeServer,
		ProviderCodebuffSDK,
	}
	for _, id := range allIDs {
		if _, ok := DefaultSandboxByProvider[id]; !ok {
			t.Errorf("DefaultSandboxByProvider missing entry for %q", id)
		}
	}
}

func TestLoadConfig_RejectsInvalidMaxAnalysisDuration(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	yamlContent := `
projects:
  - name: example
    path: ` + projectDir + `
daemon:
  frequency_seconds: 60
  output_root: ` + projectDir + `
  max_analysis_duration: "not-a-duration"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("expected error for invalid max_analysis_duration")
	}
	if !strings.Contains(err.Error(), "parse duration") {
		t.Fatalf("error = %q, want 'parse duration'", err)
	}
}

func TestLoadConfig_DefaultsNegativeFrequency(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	yamlContent := `
projects:
  - name: example
    path: ` + projectDir + `
daemon:
  frequency_seconds: -1
  output_root: ` + projectDir + `
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// applyDefaults replaces negative frequency with the default
	if got, want := cfg.Daemon.FrequencySeconds, DefaultFrequencySeconds; got != want {
		t.Fatalf("FrequencySeconds = %d, want %d (should be defaulted from negative)", got, want)
	}
}

func TestGlobalConfigPath(t *testing.T) {
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	got, err := GlobalConfigPath()
	if err != nil {
		t.Fatalf("GlobalConfigPath: %v", err)
	}
	want := filepath.Join(xdg, "dreamer", "config.yaml")
	if got != want {
		t.Fatalf("GlobalConfigPath = %q, want %q", got, want)
	}
}
