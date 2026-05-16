package pipeline

import (
	"strings"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
)

func anyEnabled(packs []analyzer.RulePack) bool {
	for _, p := range packs {
		if p.Enabled {
			return true
		}
	}
	return false
}

func mergeRulePacks(cfg *config.Config, project *config.ProjectFileConfig) []analyzer.RulePack {
	packs, err := analyzer.LoadDefaultRulePacks()
	if err != nil {
		return nil
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
	return packs
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
		packs[i].Enabled = override.Enabled
	}
}

func buildRedactor(cfg *config.Config, project *config.ProjectFileConfig) (*analyzer.Redactor, error) {
	patterns := append([]string{}, cfg.Redaction.Patterns...)
	if project != nil {
		patterns = append(patterns, project.Redaction.Patterns...)
	}
	return analyzer.NewRedactor(patterns)
}

func buildProviderConfig(block config.ProviderBlock) analyzer.ProviderConfig {
	out := analyzer.ProviderConfig{
		Model:          block.Model,
		CopilotHome:    block.CopilotHome,
		CLIURL:         block.CLIURL,
		Command:        append([]string(nil), block.Command...),
		APIKeyEnv:      block.APIKeyEnv,
		MaxInputTokens: block.MaxInputTokens,
	}
	if block.UseLoggedInUser != nil {
		out.UseLoggedInUser = *block.UseLoggedInUser
		out.UseLoggedInUserSet = true
	}
	if block.AutoStart != nil {
		out.AutoStart = *block.AutoStart
	}
	if len(block.Env) > 0 {
		out.Env = map[string]string{}
		for k, v := range block.Env {
			out.Env[k] = v
		}
	}
	return out
}
