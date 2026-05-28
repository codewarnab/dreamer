package claudeacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

const ID = "claude-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderClaudeACP, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := providerConfig.Command
		if len(command) == 0 {
			command = []string{"npx", "-y", "@zed-industries/claude-code-acp"}
		}
		// Strip CLAUDECODE so claude-code-acp's nested-session guard doesn't
		// reject us when dreamer itself was launched from a Claude Code session.
		env := map[string]string{"CLAUDECODE": ""}
		for k, v := range providerConfig.Env {
			env[k] = v
		}
		return acpcore.New(acpcore.Options{
			ID:               ID,
			Command:          command,
			Env:              env,
			DefaultModel:     providerConfig.DefaultModel,
			Sandbox:          providerConfig.Sandbox,
			SandboxNetwork:   providerConfig.SandboxNetwork,
			SandboxSeccomp:   providerConfig.SandboxSeccomp,
			SandboxResources: acpcore.SandboxResourceLimits{
				MemoryMB:  providerConfig.SandboxResources.MemoryMB,
				Processes: providerConfig.SandboxResources.Processes,
				FDs:       providerConfig.SandboxResources.FDs,
			},
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderClaudeACP,
		DisplayName: "Claude via ACP",
		Order:       50,
		Capabilities: analyzer.ProviderCapabilities{
			RequiresNetwork: true,
		},
	})
}
