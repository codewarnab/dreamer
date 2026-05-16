package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultFrequencySeconds = 3600
	DefaultLogLevel         = "info"
	DefaultProviderID       = "copilot-sdk"
	DefaultRuleTimeoutSecs  = 45
	DefaultRuleThreshold    = 0.70

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
}

// ProjectConfig is one entry in `projects:` — daemon iterates these.
type ProjectConfig struct {
	Name  string `yaml:"name" json:"name"`
	Path  string `yaml:"path" json:"path"`
	Since string `yaml:"since,omitempty" json:"since,omitempty"`
}

// DaemonConfig governs daemon mode runtime.
type DaemonConfig struct {
	FrequencySeconds int    `yaml:"frequency_seconds" json:"frequency_seconds"`
	OutputRoot       string `yaml:"output_root,omitempty" json:"output_root,omitempty"`
}

// LoggingConfig configures the structured logger.
type LoggingConfig struct {
	Level string `yaml:"level" json:"level"`
	File  string `yaml:"file,omitempty" json:"file,omitempty"`
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
	MaxInputTokens  int               `yaml:"max_input_tokens,omitempty" json:"max_input_tokens,omitempty"`
}

// AnalyzerConfig configures analyzer-wide knobs that are not provider-specific:
// the default per-rule timeout and per-category enable toggles. Provider
// settings (model, auth, cli url, …) live under `providers:` instead.
type AnalyzerConfig struct {
	RuleTimeoutSeconds int                   `yaml:"rule_timeout_seconds,omitempty" json:"rule_timeout_seconds,omitempty"`
	Rules              map[string]RuleConfig `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// RuleConfig is a global override toggle for a rule category.
type RuleConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	Severity string `yaml:"severity,omitempty" json:"severity,omitempty"`
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
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, configDirName, globalConfigFile), nil
}

// UserConfigRoot returns `<UserConfigDir>/dreamer`.
func UserConfigRoot() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, configDirName), nil
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

func applyDefaults(cfg *Config) error {
	if strings.TrimSpace(cfg.DefaultProvider) == "" {
		cfg.DefaultProvider = DefaultProviderID
	}
	if cfg.Daemon.FrequencySeconds <= 0 {
		cfg.Daemon.FrequencySeconds = DefaultFrequencySeconds
	}
	if strings.TrimSpace(cfg.Logging.Level) == "" {
		cfg.Logging.Level = DefaultLogLevel
	}
	if strings.TrimSpace(cfg.Daemon.OutputRoot) == "" {
		root, err := UserConfigRoot()
		if err != nil {
			return fmt.Errorf("resolve default output root: %w", err)
		}
		cfg.Daemon.OutputRoot = root
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderBlock{}
	}
	if cfg.Analyzer.Rules == nil {
		cfg.Analyzer.Rules = map[string]RuleConfig{}
	}
	return nil
}

func validateConfig(cfg *Config) error {
	for i := range cfg.Projects {
		project := &cfg.Projects[i]
		project.Name = strings.TrimSpace(project.Name)
		if project.Name == "" {
			return fmt.Errorf("project at index %d has empty name", i)
		}
		if strings.TrimSpace(project.Path) == "" {
			return fmt.Errorf("project %q has empty path", project.Name)
		}
		expanded, err := ExpandUserHome(project.Path)
		if err != nil {
			return fmt.Errorf("expand home in project %q path: %w", project.Name, err)
		}
		expanded = filepath.Clean(expanded)
		if !filepath.IsAbs(expanded) {
			return fmt.Errorf("project %q path must be absolute: %q", project.Name, project.Path)
		}
		if _, err := os.Stat(expanded); err != nil {
			return fmt.Errorf("project %q path validation failed for %q: %w", project.Name, expanded, err)
		}
		project.Path = expanded
	}

	outputRoot, err := ExpandUserHome(cfg.Daemon.OutputRoot)
	if err != nil {
		return fmt.Errorf("expand daemon output root: %w", err)
	}
	if !filepath.IsAbs(outputRoot) {
		return fmt.Errorf("daemon output_root must be absolute: %q", cfg.Daemon.OutputRoot)
	}
	cfg.Daemon.OutputRoot = filepath.Clean(outputRoot)
	return nil
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
	if override.MaxInputTokens > 0 {
		out.MaxInputTokens = override.MaxInputTokens
	}
	return out
}
