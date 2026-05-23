package claudecli

import "dreamer/internal/analyzer"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderClaudeCLI, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(Options{
			Command:      providerConfig.Command,
			Env:          providerConfig.Env,
			Model:        providerConfig.Model,
			DefaultModel: providerConfig.DefaultModel,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderClaudeCLI,
		DisplayName: "Anthropic Claude (stream-json)",
		Order:       40,
		Phase2Mode:  "mcp",
	})
}
