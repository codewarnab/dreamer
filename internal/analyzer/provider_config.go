package analyzer

import "dreamer/internal/config"

// ProviderConfigFromBlock builds a ProviderConfig from a config.ProviderBlock,
// applying per-provider defaults (model, sandbox) from the config package.
// Shared by the analysis pipeline and background jobs so provider defaults,
// model fallback, env copying, and sandbox mode stay in sync.
func ProviderConfigFromBlock(providerID string, block config.ProviderBlock, sandboxCfg config.SandboxConfig) ProviderConfig {
	out := ProviderConfig{
		Model:          block.Model,
		DefaultModel:   config.DefaultModelByProvider[config.ProviderID(providerID)],
		CopilotHome:    block.CopilotHome,
		CLIURL:         block.CLIURL,
		Command:        append([]string(nil), block.Command...),
		APIKeyEnv:      block.APIKeyEnv,
		BaseURL:        block.BaseURL,
		Password:       block.Password,
		MaxInputTokens: block.MaxInputTokens,
		Sandbox:        config.DefaultSandboxByProvider[config.ProviderID(providerID)],
		SandboxProjectWrite: sandboxCfg.ProjectWrite != nil && *sandboxCfg.ProjectWrite,
		SandboxNetwork:      sandboxCfg.Network,
		SandboxSeccomp:      sandboxCfg.Seccomp,
		SandboxResources: SandboxResourceLimits{
			MemoryMB:  sandboxCfg.Resources.MemoryMB,
			Processes: sandboxCfg.Resources.Processes,
			FDs:       sandboxCfg.Resources.FDs,
		},
	}
	if block.Sandbox != nil {
		out.Sandbox = *block.Sandbox
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
