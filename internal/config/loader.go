package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultFrequencySeconds = 300
	defaultLogLevel         = "info"
	defaultAnalyzerModel    = "gpt-5"
	rootDirName             = ".dreamer"
)

type Config struct {
	Projects []ProjectConfig `json:"projects" yaml:"projects"`
	Analyzer AnalyzerConfig  `json:"analyzer" yaml:"analyzer"`
	Daemon   DaemonConfig    `json:"daemon" yaml:"daemon"`
}

type ProjectConfig struct {
	Name string `json:"name" yaml:"name"`
	Path string `json:"path" yaml:"path"`
}

type AnalyzerConfig struct {
	Model           string                `json:"model" yaml:"model"`
	CopilotHome     string                `json:"copilot_home,omitempty" yaml:"copilot_home,omitempty"`
	CLIURL          string                `json:"cli_url,omitempty" yaml:"cli_url,omitempty"`
	UseLoggedInUser *bool                 `json:"use_logged_in_user,omitempty" yaml:"use_logged_in_user,omitempty"`
	AutoStart       *bool                 `json:"auto_start,omitempty" yaml:"auto_start,omitempty"`
	Rules           map[string]RuleConfig `json:"rules" yaml:"rules"`
}

type RuleConfig struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Severity string `json:"severity,omitempty" yaml:"severity,omitempty"`
}

type DaemonConfig struct {
	FrequencySeconds int    `json:"frequency_seconds" yaml:"frequency_seconds"`
	LogLevel         string `json:"log_level" yaml:"log_level"`
	OutputRoot       string `json:"output_root" yaml:"output_root"`
}

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

func applyDefaults(cfg *Config) error {
	if cfg.Daemon.FrequencySeconds <= 0 {
		cfg.Daemon.FrequencySeconds = defaultFrequencySeconds
	}
	if strings.TrimSpace(cfg.Daemon.LogLevel) == "" {
		cfg.Daemon.LogLevel = defaultLogLevel
	}
	if strings.TrimSpace(cfg.Daemon.OutputRoot) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve user home for default output root: %w", err)
		}
		cfg.Daemon.OutputRoot = filepath.Join(home, rootDirName)
	}
	if strings.TrimSpace(cfg.Analyzer.Model) == "" {
		cfg.Analyzer.Model = defaultAnalyzerModel
	}
	if cfg.Analyzer.UseLoggedInUser == nil {
		cfg.Analyzer.UseLoggedInUser = boolPointer(true)
	}
	if cfg.Analyzer.AutoStart == nil {
		cfg.Analyzer.AutoStart = boolPointer(false)
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

		expandedPath, err := expandUserHome(project.Path)
		if err != nil {
			return fmt.Errorf("expand home in project %q path: %w", project.Name, err)
		}
		expandedPath = filepath.Clean(expandedPath)

		if !filepath.IsAbs(expandedPath) {
			return fmt.Errorf("project %q path must be absolute: %q", project.Name, project.Path)
		}
		if _, err := os.Stat(expandedPath); err != nil {
			return fmt.Errorf("project %q path validation failed for %q: %w", project.Name, expandedPath, err)
		}

		project.Path = expandedPath
	}

	outputRoot, err := expandUserHome(cfg.Daemon.OutputRoot)
	if err != nil {
		return fmt.Errorf("expand daemon output root: %w", err)
	}
	if !filepath.IsAbs(outputRoot) {
		return fmt.Errorf("daemon output_root must be absolute: %q", cfg.Daemon.OutputRoot)
	}
	cfg.Daemon.OutputRoot = filepath.Clean(outputRoot)

	return nil
}

func expandUserHome(path string) (string, error) {
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

func boolPointer(value bool) *bool {
	return &value
}
