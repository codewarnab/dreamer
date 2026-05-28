package opencodeacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

const ID = "opencode-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderOpenCodeACP, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := providerConfig.Command
		if len(command) == 0 {
			command = []string{"opencode", "acp"}
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
		ID:          analyzer.ProviderOpenCodeACP,
		DisplayName: "OpenCode via ACP",
		Order:       110,
		Capabilities: analyzer.ProviderCapabilities{
			RequiresNetwork: true,
		},
	})
}
