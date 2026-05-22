package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"dreamer/internal/errs"
)

const (
	DefaultFrequencySeconds = 3600
	DefaultLogLevel         = "info"
	DefaultModel            = "gpt-5.3-codex"
	DefaultProviderID       = "openclaude-cli"

	// DefaultSince bounds first-run input volume on long-lived projects.
	DefaultSince = "24h"

	// LifetimeSinceValue restores unlimited-lookback behavior.
	LifetimeSinceValue = "lifetime"

	ExecutionModeSequential = "sequential"
	ExecutionModeParallel   = "parallel"

	// DefaultMaxChunkBytes ≈ 160k tokens at 3 bytes/token.
	DefaultMaxChunkBytes = 480_000

	// DefaultProviderBoundaryHeadroom: min free fraction before packing the next provider.
	DefaultProviderBoundaryHeadroom = 0.20

	// DefaultWebPort is the loopback port for the embedded web server.
	DefaultWebPort = 7777
	// DefaultWebHost is the loopback address the web server binds to.
	DefaultWebHost = "127.0.0.1"
	// DefaultLogTailKB is the default number of kilobytes to read from
	// the tail of dreamer.log for the /api/logs/tail endpoint.
	DefaultLogTailKB = 256

	// DefaultMaxConcurrentJobs: one analysis at a time by default.
	DefaultMaxConcurrentJobs = 1
	// DefaultMaxAnalysisDuration caps a single job's wall-clock time.
	DefaultMaxAnalysisDuration = "8h"
	// DefaultJobHistoryRetention: keep completed job records for 30 days.
	DefaultJobHistoryRetention = "720h"

	configDirName     = "dreamer"
	globalConfigFile  = "config.yaml"
	projectConfigDir  = ".dreamer"
	projectConfigFile = "config.yaml"
)

// Config is the v1 global configuration document.
type Config struct {
	DefaultProvider string                   `yaml:"default_provider" json:"default_provider"`
	Projects        []ProjectConfig          `yaml:"projects" json:"projects"`
	Daemon          DaemonConfig             `yaml:"daemon" json:"daemon"`
	Logging         LoggingConfig            `yaml:"logging" json:"logging"`
	Redaction       RedactionConfig          `yaml:"redaction" json:"redaction"`
	Providers       map[string]ProviderBlock `yaml:"providers" json:"providers"`
	Analyzer        AnalyzerConfig           `yaml:"analyzer" json:"analyzer"`
	Web             WebConfig                `yaml:"web,omitempty" json:"web,omitempty"`

	// Notices collects soft signals discovered during config load. Not
	// serialized; callers (cmd/analyze.go, cmd/daemon.go) log them at
	// info level once per process.
	Notices ConfigNotices `yaml:"-" json:"-"`
}

// ConfigNotices collects soft signals discovered during config load.
// Callers log them once per process at info level.
type ConfigNotices struct {
	// DefaultedSince: project names whose `since` field was filled with DefaultSince.
	DefaultedSince    []string
	OverlayApplied    bool
	OverlayParseError string
	RestartRequired   []string
}

// ProjectConfig is one entry in `projects:` — daemon iterates these.
type ProjectConfig struct {
	Name                string `yaml:"name" json:"name"`
	Path                string `yaml:"path" json:"path"`
	Since               string `yaml:"since,omitempty" json:"since,omitempty"`
	MaxAnalysisDuration string `yaml:"max_analysis_duration,omitempty" json:"max_analysis_duration,omitempty"`
}

// DaemonConfig governs daemon mode runtime.
type DaemonConfig struct {
	FrequencySeconds    int    `yaml:"frequency_seconds" json:"frequency_seconds"`
	OutputRoot          string `yaml:"output_root,omitempty" json:"output_root,omitempty"`
	MaxConcurrentJobs   int    `yaml:"max_concurrent_jobs,omitempty" json:"max_concurrent_jobs,omitempty"`
	MaxAnalysisDuration string `yaml:"max_analysis_duration,omitempty" json:"max_analysis_duration,omitempty"`
	JobHistoryRetention string `yaml:"job_history_retention,omitempty" json:"job_history_retention,omitempty"`
}

// LoggingConfig configures the structured logger.
type LoggingConfig struct {
	Level     string `yaml:"level" json:"level"`
	File      string `yaml:"file,omitempty" json:"file,omitempty"`
	MaxSizeMB int    `yaml:"max_size_mb,omitempty" json:"max_size_mb,omitempty"`
}

// RedactionConfig configures the secret-redaction pass.
type RedactionConfig struct {
	Patterns []string `yaml:"patterns,omitempty" json:"patterns,omitempty"`
}

// ProviderBlock is a per-provider configuration entry under `providers:`.
// Field semantics vary by provider id; unused fields are ignored per-provider.
type ProviderBlock struct {
	Model           string            `yaml:"model,omitempty" json:"model,omitempty"`
	UseLoggedInUser *bool             `yaml:"use_logged_in_user,omitempty" json:"use_logged_in_user,omitempty"`
	AutoStart       *bool             `yaml:"auto_start,omitempty" json:"auto_start,omitempty"`
	CopilotHome     string            `yaml:"copilot_home,omitempty" json:"copilot_home,omitempty"`
	CLIURL          string            `yaml:"cli_url,omitempty" json:"cli_url,omitempty"`
	Command         []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Env             map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	APIKeyEnv       string            `yaml:"api_key_env,omitempty" json:"api_key_env,omitempty"`
	BaseURL         string            `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	Password        string            `yaml:"password,omitempty" json:"password,omitempty"`
	MaxInputTokens  int               `yaml:"max_input_tokens,omitempty" json:"max_input_tokens,omitempty"`
}

// AnalyzerConfig configures analyzer-wide knobs that are not provider-specific.
type AnalyzerConfig struct {
	RuleTimeoutSeconds int `yaml:"rule_timeout_seconds,omitempty" json:"rule_timeout_seconds,omitempty"`
	// IncludeSubagentTranscripts controls whether subagent/child chat
	// transcripts are included in analysis. When false (default), sources
	// with a non-empty ParentID are skipped.
	IncludeSubagentTranscripts bool                  `yaml:"include_subagent_transcripts,omitempty" json:"include_subagent_transcripts,omitempty"`
	Rules                      map[string]RuleConfig `yaml:"rules,omitempty" json:"rules,omitempty"`
	Execution                  ExecutionConfig       `yaml:"execution,omitempty" json:"execution,omitempty"`
	Chunking                   ChunkingConfig        `yaml:"chunking,omitempty" json:"chunking,omitempty"`
}

// ExecutionConfig: how the orchestrator dispatches per-chunk provider calls.
type ExecutionConfig struct {
	// Mode is "sequential" (default) or "parallel". Unknown -> sequential with warning.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// MaxConcurrency caps parallel sessions. 0 = len(chunks). Ignored when sequential.
	MaxConcurrency int `yaml:"max_concurrency,omitempty" json:"max_concurrency,omitempty"`
}

// ChunkingConfig: how to split the redacted transcript before phase-1.
type ChunkingConfig struct {
	// MaxChunkBytes caps transcript bytes per chunk. 0 disables chunking.
	MaxChunkBytes int `yaml:"max_chunk_bytes,omitempty" json:"max_chunk_bytes,omitempty"`
	// ProviderBoundaryHeadroom: min free fraction [0.0, 1.0) before packing
	// the next provider. Pointer so an explicit zero (disable) can be
	// distinguished from "unset" (use the documented default) — B5.
	ProviderBoundaryHeadroom *float64 `yaml:"provider_boundary_headroom,omitempty" json:"provider_boundary_headroom,omitempty"`
}

// WebConfig governs the daemon's embedded HTTP server.
// Enabled is a pointer so applyDefaults can distinguish "unset" (flip to
// true) from explicit "enabled: false" (preserve). Same pattern as
// ProviderBlock.UseLoggedInUser.
type WebConfig struct {
	Enabled   *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Port      int    `yaml:"port,omitempty" json:"port,omitempty"`
	Host      string `yaml:"host,omitempty" json:"host,omitempty"`
	LogTailKB int    `yaml:"log_tail_kb,omitempty" json:"log_tail_kb,omitempty"`
}

// RuleConfig is a global override toggle for a rule category.
type RuleConfig struct {
	Enabled  *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Severity string `yaml:"severity,omitempty" json:"severity,omitempty"`

	// Template overrides (optional). When set, these replace the
	// corresponding fields from the embedded YAML rule pack.
	MistakePromptTemplate     string `yaml:"mistake_prompt_template,omitempty" json:"mistake_prompt_template,omitempty"`
	GuardrailPromptTemplate   string `yaml:"guardrail_prompt_template,omitempty" json:"guardrail_prompt_template,omitempty"`
	Phase1Preamble            string `yaml:"phase1_preamble,omitempty" json:"phase1_preamble,omitempty"`
	Phase1CategoryDescription string `yaml:"phase1_category_description,omitempty" json:"phase1_category_description,omitempty"`
	Phase1ResponseSchema      string `yaml:"phase1_response_schema,omitempty" json:"phase1_response_schema,omitempty"`
	Phase2Preamble            string `yaml:"phase2_preamble,omitempty" json:"phase2_preamble,omitempty"`
	Phase2ResponseSchema      string `yaml:"phase2_response_schema,omitempty" json:"phase2_response_schema,omitempty"`
}

// ProjectFileConfig is the per-project `<project>/.dreamer/config.yaml`.
type ProjectFileConfig struct {
	Provider  string                   `yaml:"provider,omitempty" json:"provider,omitempty"`
	Providers map[string]ProviderBlock `yaml:"providers,omitempty" json:"providers,omitempty"`
	Rules     map[string]RuleConfig    `yaml:"rules,omitempty" json:"rules,omitempty"`
	Redaction RedactionConfig          `yaml:"redaction,omitempty" json:"redaction,omitempty"`
}

// GlobalConfigPath returns the canonical global config path.
func GlobalConfigPath() (string, error) {
	root, err := UserConfigRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, globalConfigFile), nil
}

// ConfigDirBase returns the platform config directory (XDG_CONFIG_HOME
// or os.UserConfigDir) without the "dreamer" subdirectory appended.
func ConfigDirBase() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return xdg, nil
	}
	return os.UserConfigDir()
}

// UserConfigRoot returns `<UserConfigDir>/dreamer`.
func UserConfigRoot() (string, error) {
	base, err := ConfigDirBase()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(base, configDirName), nil
}

// ProjectConfigPath returns `<projectPath>/.dreamer/config.yaml`.
func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, projectConfigDir, projectConfigFile)
}

// ProjectRulesPath returns `<projectPath>/.dreamer/rules/<category>.yaml`.
func ProjectRulesPath(projectPath string, category string) string {
	return filepath.Join(projectPath, projectConfigDir, "rules", category+".yaml")
}

// LoadConfig loads + validates the global config. Returns a Config with
// defaults applied. A missing file is treated as "no config" only when path
// is empty; otherwise an explicit path that does not exist errors out.
func LoadConfig(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("config path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config file %q: %w", path, err)
	}
	if err := applyDefaults(&cfg); err != nil {
		return nil, err
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadProjectFileConfig loads the per-project config file. Missing file is
// not an error; returns an empty ProjectFileConfig in that case.
func LoadProjectFileConfig(projectPath string) (*ProjectFileConfig, error) {
	path := ProjectConfigPath(projectPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ProjectFileConfig{}, nil
		}
		return nil, fmt.Errorf("read project config %q: %w", path, err)
	}
	var pfc ProjectFileConfig
	if err := yaml.Unmarshal(data, &pfc); err != nil {
		return nil, fmt.Errorf("unmarshal project config %q: %w", path, err)
	}
	return &pfc, nil
}

func applyDefaults(appConfig *Config) error {
	if strings.TrimSpace(appConfig.DefaultProvider) == "" {
		appConfig.DefaultProvider = DefaultProviderID
	}
	if appConfig.Daemon.FrequencySeconds <= 0 {
		appConfig.Daemon.FrequencySeconds = DefaultFrequencySeconds
	}
	if strings.TrimSpace(appConfig.Logging.Level) == "" {
		appConfig.Logging.Level = DefaultLogLevel
	}
	if strings.TrimSpace(appConfig.Daemon.OutputRoot) == "" {
		root, err := UserConfigRoot()
		if err != nil {
			return fmt.Errorf("resolve default output root: %w", err)
		}
		appConfig.Daemon.OutputRoot = root
	}
	if appConfig.Providers == nil {
		appConfig.Providers = map[string]ProviderBlock{}
	}
	if appConfig.Analyzer.Rules == nil {
		appConfig.Analyzer.Rules = map[string]RuleConfig{}
	}
	applyAnalyzerExecutionDefaults(&appConfig.Analyzer.Execution)
	applyAnalyzerChunkingDefaults(&appConfig.Analyzer.Chunking)
	applyProjectSinceDefaults(appConfig)
	if appConfig.Web.Enabled == nil {
		t := true
		appConfig.Web.Enabled = &t
	}
	if appConfig.Web.Port == 0 {
		appConfig.Web.Port = DefaultWebPort
	}
	if appConfig.Web.Host == "" {
		appConfig.Web.Host = DefaultWebHost
	}
	if appConfig.Web.LogTailKB == 0 {
		appConfig.Web.LogTailKB = DefaultLogTailKB
	}
	applyDaemonJobQueueDefaults(appConfig)
	return nil
}

// applyAnalyzerExecutionDefaults: empty mode -> sequential; unknown left for warn.
func applyAnalyzerExecutionDefaults(exec *ExecutionConfig) {
	if strings.TrimSpace(exec.Mode) == "" {
		exec.Mode = ExecutionModeSequential
	}
	if exec.MaxConcurrency < 0 {
		exec.MaxConcurrency = 0
	}
}

// applyAnalyzerChunkingDefaults fills MaxChunkBytes and ProviderBoundaryHeadroom.
// CLI --chunk-size=0 is the disable-chunking escape hatch. For headroom
// the pointer's nil-ness distinguishes "unset" from explicit `0`.
func applyAnalyzerChunkingDefaults(chunk *ChunkingConfig) {
	if chunk.MaxChunkBytes == 0 {
		chunk.MaxChunkBytes = DefaultMaxChunkBytes
	}
	if chunk.ProviderBoundaryHeadroom == nil {
		def := DefaultProviderBoundaryHeadroom
		chunk.ProviderBoundaryHeadroom = &def
	}
}

// applyDaemonJobQueueDefaults fills job-queue fields with sane defaults.
func applyDaemonJobQueueDefaults(appConfig *Config) {
	if appConfig.Daemon.MaxConcurrentJobs <= 0 {
		appConfig.Daemon.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}
	if strings.TrimSpace(appConfig.Daemon.MaxAnalysisDuration) == "" {
		appConfig.Daemon.MaxAnalysisDuration = DefaultMaxAnalysisDuration
	}
	if strings.TrimSpace(appConfig.Daemon.JobHistoryRetention) == "" {
		appConfig.Daemon.JobHistoryRetention = DefaultJobHistoryRetention
	}
}

// applyProjectSinceDefaults fills blank `since` with DefaultSince and records the notice.
func applyProjectSinceDefaults(appConfig *Config) {
	for i := range appConfig.Projects {
		project := &appConfig.Projects[i]
		if strings.TrimSpace(project.Since) != "" {
			continue
		}
		project.Since = DefaultSince
		name := strings.TrimSpace(project.Name)
		if name == "" {
			name = project.Path
		}
		appConfig.Notices.DefaultedSince = append(appConfig.Notices.DefaultedSince, name)
	}
}

// IsLifetimeSince reports whether `value` (case-insensitive, trimmed) requests unlimited lookback.
func IsLifetimeSince(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), LifetimeSinceValue)
}

func validateConfig(cfg *Config) error {
	seenNames := make(map[string]int, len(cfg.Projects))
	for i := range cfg.Projects {
		project := &cfg.Projects[i]
		project.Name = strings.TrimSpace(project.Name)
		if project.Name == "" {
			return errs.ConfigInvalid(fmt.Sprintf("projects[%d].name", i), "", fmt.Errorf("project at index %d has empty name", i))
		}
		if prior, ok := seenNames[project.Name]; ok {
			// B4: duplicate names collide on <outputRoot>/<name>/ for both
			// state.json and todos.md, silently corrupting the daemon's
			// state. Reject at load time.
			return errs.ConfigInvalid(fmt.Sprintf("projects[%d].name", i), project.Name, fmt.Errorf("duplicate project name %q (first declared at projects[%d])", project.Name, prior))
		}
		seenNames[project.Name] = i
		if strings.TrimSpace(project.Path) == "" {
			return errs.ConfigInvalid(fmt.Sprintf("projects.%s.path", project.Name), "", fmt.Errorf("project %q has empty path", project.Name))
		}
		expanded, err := ExpandUserHome(project.Path)
		if err != nil {
			return errs.ConfigInvalid(fmt.Sprintf("projects.%s.path", project.Name), project.Path, fmt.Errorf("expand home in project %q path: %w", project.Name, err))
		}
		expanded = filepath.Clean(expanded)
		if !filepath.IsAbs(expanded) {
			return errs.ConfigInvalid(fmt.Sprintf("projects.%s.path", project.Name), project.Path, fmt.Errorf("project %q path must be absolute: %q", project.Name, project.Path))
		}
		if _, err := os.Stat(expanded); err != nil {
			return errs.ConfigInvalid(fmt.Sprintf("projects.%s.path", project.Name), expanded, fmt.Errorf("project %q path validation failed for %q: %w", project.Name, expanded, err))
		}
		project.Path = expanded
	}

	outputRoot, err := ExpandUserHome(cfg.Daemon.OutputRoot)
	if err != nil {
		return errs.ConfigInvalid("daemon.output_root", cfg.Daemon.OutputRoot, fmt.Errorf("expand daemon output root: %w", err))
	}
	if !filepath.IsAbs(outputRoot) {
		return errs.ConfigInvalid("daemon.output_root", cfg.Daemon.OutputRoot, fmt.Errorf("daemon output_root must be absolute: %q", cfg.Daemon.OutputRoot))
	}
	cfg.Daemon.OutputRoot = filepath.Clean(outputRoot)
	if err := validateWebHost(cfg.Web.Host); err != nil {
		return err
	}

	if cfg.Daemon.FrequencySeconds <= 0 {
		return errs.ConfigInvalid("daemon.frequency_seconds", cfg.Daemon.FrequencySeconds, fmt.Errorf("must be positive"))
	}
	if _, err := time.ParseDuration(cfg.Daemon.MaxAnalysisDuration); err != nil {
		return errs.ConfigInvalid("daemon.max_analysis_duration", cfg.Daemon.MaxAnalysisDuration, fmt.Errorf("parse duration: %w", err))
	}
	if _, err := time.ParseDuration(cfg.Daemon.JobHistoryRetention); err != nil {
		return errs.ConfigInvalid("daemon.job_history_retention", cfg.Daemon.JobHistoryRetention, fmt.Errorf("parse duration: %w", err))
	}
	for i := range cfg.Projects {
		if dur := cfg.Projects[i].MaxAnalysisDuration; dur != "" {
			if _, err := time.ParseDuration(dur); err != nil {
				return errs.ConfigInvalid(fmt.Sprintf("projects.%s.max_analysis_duration", cfg.Projects[i].Name), dur, fmt.Errorf("parse duration: %w", err))
			}
		}
	}
	return nil
}

// validateWebHost rejects any host that isn't loopback. v1.5 ships
// without auth; v1.6 may relax this behind a token-auth flag.
func validateWebHost(host string) error {
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("web.host %q must be loopback (127.0.0.1, ::1, or localhost); non-loopback binds are reserved for v1.6", host)
}

// ExpandUserHome expands a leading `~` or `~/` into the user's home directory.
func ExpandUserHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// ResolveProviderConfig merges global + per-project + CLI overrides per §3.1.
// Returns the resolved provider id and ProviderBlock for that id.
func (cfg *Config) ResolveProviderConfig(projectFile *ProjectFileConfig, cliProvider string) (string, ProviderBlock) {
	id := strings.TrimSpace(cliProvider)
	if id == "" && projectFile != nil {
		id = strings.TrimSpace(projectFile.Provider)
	}
	if id == "" {
		id = strings.TrimSpace(cfg.DefaultProvider)
	}
	if id == "" {
		id = DefaultProviderID
	}

	block := cfg.Providers[id]
	if projectFile != nil {
		if override, ok := projectFile.Providers[id]; ok {
			block = mergeProviderBlocks(block, override)
		}
	}
	return id, block
}

// ResolveMaxDuration returns the analysis timeout for the named project.
// A per-project override takes precedence over the daemon default.
func (cfg *Config) ResolveMaxDuration(projectName string) (time.Duration, error) {
	for _, p := range cfg.Projects {
		if p.Name == projectName && p.MaxAnalysisDuration != "" {
			return time.ParseDuration(p.MaxAnalysisDuration)
		}
	}
	return time.ParseDuration(cfg.Daemon.MaxAnalysisDuration)
}

func mergeProviderBlocks(base, override ProviderBlock) ProviderBlock {
	out := base
	if override.Model != "" {
		out.Model = override.Model
	}
	if override.UseLoggedInUser != nil {
		out.UseLoggedInUser = override.UseLoggedInUser
	}
	if override.AutoStart != nil {
		out.AutoStart = override.AutoStart
	}
	if override.CopilotHome != "" {
		out.CopilotHome = override.CopilotHome
	}
	if override.CLIURL != "" {
		out.CLIURL = override.CLIURL
	}
	if len(override.Command) > 0 {
		out.Command = append([]string(nil), override.Command...)
	}
	if len(override.Env) > 0 {
		merged := map[string]string{}
		for k, v := range base.Env {
			merged[k] = v
		}
		for k, v := range override.Env {
			merged[k] = v
		}
		out.Env = merged
	}
	if override.APIKeyEnv != "" {
		out.APIKeyEnv = override.APIKeyEnv
	}
	if override.BaseURL != "" {
		out.BaseURL = override.BaseURL
	}
	if override.Password != "" {
		out.Password = override.Password
	}
	if override.MaxInputTokens > 0 {
		out.MaxInputTokens = override.MaxInputTokens
	}
	return out
}

// ValidateProjectName checks that a project name is safe to use as a
// directory name under the output root. Shared by state.PathForProject
// and output.todosPathForProject.
func ValidateProjectName(projectName string) error {
	name := strings.TrimSpace(projectName)
	if name == "" {
		return fmt.Errorf("project name is required")
	}
	if strings.ContainsAny(name, `\/`) {
		return fmt.Errorf("project name contains invalid path separator: %q", projectName)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("project name is invalid: %q", projectName)
	}
	return nil
}
