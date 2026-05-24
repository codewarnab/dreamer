package openclaudecli

import "dreamer/internal/analyzer"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderOpenClaudeCLI, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(Options{
			Command:      providerConfig.Command,
			Env:          providerConfig.Env,
			Model:        providerConfig.Model,
			DefaultModel: providerConfig.DefaultModel,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderOpenClaudeCLI,
		DisplayName: "OpenClaude CLI (recommended)",
		Order:       10,
		Phase2Mode:  analyzer.Phase2ModeMCP,
	})
}
