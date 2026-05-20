package claudecli

import "dreamer/internal/analyzer"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderClaudeCLI, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(Options{
			Command:      cfg.Command,
			Env:          cfg.Env,
			Model:        cfg.Model,
			DefaultModel: cfg.DefaultModel,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderClaudeCLI,
		DisplayName: "Anthropic Claude (stream-json)",
		Order:       40,
	})
}
