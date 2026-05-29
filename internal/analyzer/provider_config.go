package analyzer

import (
	"dreamer/internal/config"
	"dreamer/internal/sandbox"
)

// ProviderConfigFromBlock builds a ProviderConfig from a config.ProviderBlock,
// applying per-provider defaults (model, sandbox) from the config package.
// Shared by the analysis pipeline and background jobs so provider defaults,
// model fallback, env copying, and sandbox mode stay in sync.
//
// NOTE: Sandbox hardening knobs (network, seccomp, resources, project_write)
// are global-only — they come from the top-level sandbox: config block and
// apply uniformly to all providers. Per-provider overrides exist only for
// the sandbox mode (auto/on/off) via provider_block.sandbox. This is
// intentional: hardening is a deployment-level concern, not per-agent.
//
// NOTE: project_write is a no-op for ACP providers. ACP transports are
// created before the project dir is known (at provider startup, not per-
// session), so ProjectDir is "" and the sandbox Prepare function skips
// the writable-dir addition. CLI providers receive the project dir at
// session time and honor project_write correctly.
func ProviderConfigFromBlock(providerID string, block config.ProviderBlock, sandboxCfg config.SandboxConfig) ProviderConfig {
	out := ProviderConfig{
		Model:          block.Model,
		DefaultModel:   config.DefaultModelFor(providerID),
		CopilotHome:    block.CopilotHome,
		CLIURL:         block.CLIURL,
		Command:        append([]string(nil), block.Command...),
		APIKeyEnv:      block.APIKeyEnv,
		BaseURL:        block.BaseURL,
		Password:       block.Password,
		MaxInputTokens: block.MaxInputTokens,
		Sandbox:        config.DefaultSandboxFor(providerID),
		SandboxProjectWrite: sandboxCfg.ProjectWrite != nil && *sandboxCfg.ProjectWrite,
		SandboxNetwork:      sandboxCfg.Network,
		SandboxSeccomp:      sandboxCfg.Seccomp,
		SandboxResources: sandbox.ResourceLimits{
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
