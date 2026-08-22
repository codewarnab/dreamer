package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// OverlayFileName is the conventional name of the ui-overrides file.
const OverlayFileName = "ui-overrides.yaml"

// GlobalOverlayPath returns the conventional overlay path under the user
// config dir. It mirrors GlobalConfigPath's resolution.
func GlobalOverlayPath() (string, error) {
	base, err := UserConfigRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, OverlayFileName), nil
}

// LoadConfigWithOverlay loads the base config.yaml then merges
// ui-overrides.yaml on top. A missing overlay is a
// silent no-op (no error, OverlayApplied stays false). An overlay that
// fails to parse is captured in Notices.OverlayParseError and the base
// config is returned unmodified so the daemon keeps running on malformed
// overlay.
func LoadConfigWithOverlay(basePath, overlayPath string) (*App, error) {
	baseConfig, err := LoadConfig(basePath)
	if err != nil {
		return nil, err
	}
	overlayBytes, err := os.ReadFile(overlayPath)
	if err != nil {
		if os.IsNotExist(err) {
			return baseConfig, nil
		}
		return nil, fmt.Errorf("read overlay %q: %w", overlayPath, err)
	}
	if len(overlayBytes) == 0 {
		return baseConfig, nil
	}
	var overlay App
	if err := yaml.Unmarshal(overlayBytes, &overlay); err != nil {
		baseConfig.Notices.OverlayParseError = fmt.Sprintf("overlay %q parse error: %v", overlayPath, err)
		return baseConfig, nil
	}
	mergeOverlay(baseConfig, &overlay)
	baseConfig.Notices.OverlayApplied = true
	if err := applyDefaults(baseConfig); err != nil {
		// Post-merge defaults failed: roll back by re-loading base.
		base, loadErr := LoadConfig(basePath)
		if loadErr != nil {
			return nil, loadErr
		}
		base.Notices.OverlayParseError = fmt.Sprintf("overlay %q failed defaults: %v", overlayPath, err)
		return base, nil
	}
	if err := validateConfig(baseConfig); err != nil {
		base, loadErr := LoadConfig(basePath)
		if loadErr != nil {
			return nil, loadErr
		}
		base.Notices.OverlayParseError = fmt.Sprintf("overlay %q failed validation: %v", overlayPath, err)
		return base, nil
	}
	return baseConfig, nil
}

// mergeOverlay applies overlay onto base:
//   - Scalars: overlay value wins when present (non-zero).
//   - Maps (providers, analyzer.rules): per-key, overlay value wins.
//   - Lists (projects, redaction.patterns): overlay replaces entire list
//     when non-empty.
func mergeOverlay(base, overlay *App) {
	if overlay.DefaultProvider != "" {
		base.DefaultProvider = overlay.DefaultProvider
	}
	if overlay.Daemon.FrequencySeconds != 0 {
		base.Daemon.FrequencySeconds = overlay.Daemon.FrequencySeconds
	}
	if overlay.Daemon.OutputRoot != "" {
		base.Daemon.OutputRoot = overlay.Daemon.OutputRoot
	}
	if overlay.Daemon.MaxConcurrentJobs != 0 {
		base.Daemon.MaxConcurrentJobs = overlay.Daemon.MaxConcurrentJobs
	}
	if overlay.Daemon.MaxAnalysisDuration != "" {
		base.Daemon.MaxAnalysisDuration = overlay.Daemon.MaxAnalysisDuration
	}
	if overlay.Daemon.JobHistoryRetention != "" {
		base.Daemon.JobHistoryRetention = overlay.Daemon.JobHistoryRetention
	}
	if overlay.Logging.Level != "" {
		base.Logging.Level = overlay.Logging.Level
	}
	if overlay.Logging.File != "" {
		base.Logging.File = overlay.Logging.File
	}
	if overlay.Logging.MaxSizeMB != 0 {
		base.Logging.MaxSizeMB = overlay.Logging.MaxSizeMB
	}
	if len(overlay.Redaction.Patterns) > 0 {
		base.Redaction.Patterns = append([]string(nil), overlay.Redaction.Patterns...)
	}
	if len(overlay.Projects) > 0 {
		base.Projects = append([]ProjectConfig(nil), overlay.Projects...)
	}
	if len(overlay.Providers) > 0 {
		if base.Providers == nil {
			base.Providers = map[string]ProviderBlock{}
		}
		for providerID, block := range overlay.Providers {
			existing := base.Providers[providerID]
			base.Providers[providerID] = MergeProviderBlock(existing, block)
		}
	}
	mergeAnalyzer(&base.Analyzer, &overlay.Analyzer)
	mergeWeb(&base.Web, &overlay.Web)
	mergeSandbox(&base.Sandbox, &overlay.Sandbox)
}

func mergeAnalyzer(base, overlay *AnalyzerConfig) {
	if overlay.RuleTimeoutSeconds != 0 {
		base.RuleTimeoutSeconds = overlay.RuleTimeoutSeconds
	}
	// Bool field: zero-value guard means overlay can set false→true but
	// not true→false. This matches the additive overlay convention for
	// non-pointer fields. To override true→false, edit config.yaml.
	if overlay.IncludeSubagentTranscripts != nil {
		base.IncludeSubagentTranscripts = overlay.IncludeSubagentTranscripts
	}
	if overlay.Execution.Mode != "" {
		base.Execution.Mode = overlay.Execution.Mode
	}
	if overlay.Execution.MaxConcurrency != 0 {
		base.Execution.MaxConcurrency = overlay.Execution.MaxConcurrency
	}
	if overlay.Chunking.MaxChunkBytes != 0 {
		base.Chunking.MaxChunkBytes = overlay.Chunking.MaxChunkBytes
	}
	if overlay.Chunking.ProviderBoundaryHeadroom != nil {
		base.Chunking.ProviderBoundaryHeadroom = overlay.Chunking.ProviderBoundaryHeadroom
	}
	if overlay.Capture.Enabled != nil {
		base.Capture.Enabled = overlay.Capture.Enabled
	}
	if overlay.Capture.RetainRuns != 0 {
		base.Capture.RetainRuns = overlay.Capture.RetainRuns
	}
	if overlay.Capture.MaxRecordKB != 0 {
		base.Capture.MaxRecordKB = overlay.Capture.MaxRecordKB
	}
	if len(overlay.Rules) > 0 {
		if base.Rules == nil {
			base.Rules = map[string]RuleConfig{}
		}
		for k, v := range overlay.Rules {
			existing := base.Rules[k]
			mergeRuleConfig(&existing, &v)
			base.Rules[k] = existing
		}
	}
}

func mergeWeb(base, overlay *WebConfig) {
	if overlay.Enabled != nil {
		base.Enabled = overlay.Enabled
	}
	if overlay.Port != 0 {
		base.Port = overlay.Port
	}
	if overlay.Host != "" {
		base.Host = overlay.Host
	}
	if overlay.LogTailKB != 0 {
		base.LogTailKB = overlay.LogTailKB
	}
}

func mergeSandbox(base, overlay *SandboxConfig) {
	if overlay.ProjectWrite != nil {
		base.ProjectWrite = overlay.ProjectWrite
	}
	if overlay.Network != "" {
		base.Network = overlay.Network
	}
	if overlay.Seccomp != "" {
		base.Seccomp = overlay.Seccomp
	}
	if overlay.SIDExpiryDays != 0 {
		base.SIDExpiryDays = overlay.SIDExpiryDays
	}
	if overlay.Resources.MemoryMB != 0 {
		base.Resources.MemoryMB = overlay.Resources.MemoryMB
	}
	if overlay.Resources.Processes != 0 {
		base.Resources.Processes = overlay.Resources.Processes
	}
	if overlay.Resources.FDs != 0 {
		base.Resources.FDs = overlay.Resources.FDs
	}
}

func mergeRuleConfig(base, overlay *RuleConfig) {
	if overlay.Enabled != nil {
		base.Enabled = overlay.Enabled
	}
	if overlay.Severity != "" {
		base.Severity = overlay.Severity
	}
	if overlay.MistakePromptTemplate != "" {
		base.MistakePromptTemplate = overlay.MistakePromptTemplate
	}
	if overlay.GuardrailPromptTemplate != "" {
		base.GuardrailPromptTemplate = overlay.GuardrailPromptTemplate
	}
	if overlay.Phase1Preamble != "" {
		base.Phase1Preamble = overlay.Phase1Preamble
	}
	if overlay.Phase1CategoryDescription != "" {
		base.Phase1CategoryDescription = overlay.Phase1CategoryDescription
	}
	if overlay.Phase1ResponseSchema != "" {
		base.Phase1ResponseSchema = overlay.Phase1ResponseSchema
	}
	if overlay.Phase2Preamble != "" {
		base.Phase2Preamble = overlay.Phase2Preamble
	}
	if overlay.Phase2ResponseSchema != "" {
		base.Phase2ResponseSchema = overlay.Phase2ResponseSchema
	}
	if overlay.ToolUseInstructions != "" {
		base.ToolUseInstructions = overlay.ToolUseInstructions
	}
	if overlay.Phase2RecordingInstructions != "" {
		base.Phase2RecordingInstructions = overlay.Phase2RecordingInstructions
	}
}
