package pipeline

import (
	"fmt"
	"strings"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
	"dreamer/internal/mcpserver"
)

func anyEnabled(packs []analyzer.RulePack) bool {
	for _, p := range packs {
		if p.Enabled {
			return true
		}
	}
	return false
}

// BuildRulePacks assembles the effective rule packs for a project: embedded
// defaults, extra project-defined packs, global + project toggles, and the
// configured rule timeout. Exported for replay tooling so CLI and web
// replays decode responses with exactly the packs a live run would use.
func BuildRulePacks(cfg *config.App, projectPath string) ([]analyzer.RulePack, error) {
	projectFile, err := config.LoadProjectFileConfig(projectPath)
	if err != nil {
		return nil, fmt.Errorf("load project config: %w", err)
	}
	return mergeRulePacks(cfg, projectFile, projectPath)
}

func mergeRulePacks(cfg *config.App, project *config.ProjectFileConfig, projectPath string) ([]analyzer.RulePack, error) {
	packs, err := analyzer.LoadDefaultRulePacks()
	if err != nil {
		return nil, fmt.Errorf("load default rule packs: %w", err)
	}

	// Load user-defined packs from <projectPath>/.dreamer/rules/ and append
	// any that introduce a new (non-built-in) category.
	if projectPath != "" {
		rulesDir := config.ProjectRulesDir(projectPath)
		extraPacks, err := analyzer.LoadProjectRulePacks(rulesDir)
		if err != nil {
			return nil, fmt.Errorf("load project rule packs: %w", err)
		}
		for _, p := range extraPacks {
			// Register the new category so MCP finding recording accepts it.
			mcpserver.RegisterCategory(string(p.Category))
		}
		packs = append(packs, extraPacks...)
	}

	applyRuleToggles(packs, cfg.Analyzer.Rules)
	if project != nil {
		applyRuleToggles(packs, project.Rules)
	}
	if cfg.Analyzer.RuleTimeoutSeconds > 0 {
		for i := range packs {
			packs[i].TimeoutSeconds = cfg.Analyzer.RuleTimeoutSeconds
		}
	}
	return packs, nil
}

func applyRuleToggles(packs []analyzer.RulePack, overrides map[string]config.RuleConfig) {
	if len(overrides) == 0 {
		return
	}
	for i := range packs {
		key := strings.ToLower(string(packs[i].Category))
		override, ok := overrides[key]
		if !ok {
			override, ok = overrides[string(packs[i].Category)]
		}
		if !ok {
			continue
		}
		if override.Enabled != nil {
			packs[i].Enabled = *override.Enabled
		}
		if override.MistakePromptTemplate != "" {
			packs[i].MistakePromptTemplate = override.MistakePromptTemplate
		}
		if override.GuardrailPromptTemplate != "" {
			packs[i].GuardrailPromptTemplate = override.GuardrailPromptTemplate
		}
		if override.Phase1Preamble != "" {
			packs[i].Phase1Preamble = override.Phase1Preamble
		}
		if override.Phase1CategoryDescription != "" {
			packs[i].Phase1CategoryDescription = override.Phase1CategoryDescription
		}
		if override.Phase1ResponseSchema != "" {
			packs[i].Phase1ResponseSchema = override.Phase1ResponseSchema
		}
		if override.Phase2Preamble != "" {
			packs[i].Phase2Preamble = override.Phase2Preamble
		}
		if override.Phase2ResponseSchema != "" {
			packs[i].Phase2ResponseSchema = override.Phase2ResponseSchema
		}
		if override.ToolUseInstructions != "" {
			packs[i].ToolUseInstructions = override.ToolUseInstructions
		}
		if override.Phase2RecordingInstructions != "" {
			packs[i].Phase2RecordingInstructions = override.Phase2RecordingInstructions
		}
	}
}

func buildRedactor(cfg *config.App, project *config.ProjectFileConfig) (*analyzer.Redactor, error) {
	patterns := append([]string{}, cfg.Redaction.Patterns...)
	if project != nil {
		patterns = append(patterns, project.Redaction.Patterns...)
	}
	return analyzer.NewRedactor(patterns)
}
