package openclaudecli

import "dreamer/internal/analyzer"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderOpenClaudeCLI, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(Options{
			Command:      cfg.Command,
			Env:          cfg.Env,
			Model:        cfg.Model,
			DefaultModel: cfg.DefaultModel,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderOpenClaudeCLI,
		DisplayName: "OpenClaude CLI (recommended)",
		Order:       10,
	})
}
