package kiroacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

const ID = "kiro-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderKiroACP, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := providerConfig.Command
		if len(command) == 0 {
			command = []string{"kiro-cli", "acp"}
		}
		return acpcore.New(acpcore.Options{
			ID:           ID,
			Command:      command,
			Env:          providerConfig.Env,
			DefaultModel: providerConfig.DefaultModel,
			Sandbox:      providerConfig.Sandbox,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderKiroACP,
		DisplayName: "Kiro via ACP",
		Order:       80,
	})
}
