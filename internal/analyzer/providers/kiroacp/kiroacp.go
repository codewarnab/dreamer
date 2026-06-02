package kiroacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
	"dreamer/internal/sandbox"
)

const ID = "kiro-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderKiroACP, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := providerConfig.Command
		if len(command) == 0 {
			command = []string{"kiro-cli", "acp"}
		}
		return acpcore.New(acpcore.Options{
			ID:                  ID,
			Command:             command,
			Env:                 providerConfig.Env,
			DefaultModel:        providerConfig.DefaultModel,
			Sandbox:             providerConfig.Sandbox,
			SandboxProjectWrite: providerConfig.SandboxProjectWrite,
			SandboxWritableDirs: providerConfig.SandboxWritableDirs,
			SandboxNetwork:      providerConfig.SandboxNetwork,
			SandboxSeccomp:      providerConfig.SandboxSeccomp,
			SandboxResources: sandbox.ResourceLimits{
				MemoryMB:  providerConfig.SandboxResources.MemoryMB,
				Processes: providerConfig.SandboxResources.Processes,
				FDs:       providerConfig.SandboxResources.FDs,
			},
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderKiroACP,
		DisplayName: "Kiro via ACP",
		Order:       80,
		Capabilities: analyzer.ProviderCapabilities{
			RequiresNetwork: true,
		},
	})
}
