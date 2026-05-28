package copilotacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

const ID = "copilot-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderCopilotACP, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := providerConfig.Command
		if len(command) == 0 {
			command = []string{"copilot", "--acp"}
		}
		return acpcore.New(acpcore.Options{
			ID:               ID,
			Command:          command,
			Env:              providerConfig.Env,
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
		ID:          analyzer.ProviderCopilotACP,
		DisplayName: "Copilot via ACP",
		Order:       30,
		Capabilities: analyzer.ProviderCapabilities{
			RequiresNetwork: true,
		},
	})
}
