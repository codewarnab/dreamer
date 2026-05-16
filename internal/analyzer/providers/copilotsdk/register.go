package copilotsdk

import "dreamer/internal/analyzer"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderCopilotSDK, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(Options{
			CopilotHome:        cfg.CopilotHome,
			UseLoggedInUser:    cfg.UseLoggedInUser,
			UseLoggedInUserSet: cfg.UseLoggedInUserSet,
			CLIURL:             cfg.CLIURL,
			AutoStart:          cfg.AutoStart,
			Model:              cfg.Model,
		})
	})
}
