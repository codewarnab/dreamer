package copilotsdk

import "dreamer/internal/analyzer"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderCopilotSDK, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(Options{
			CopilotHome:        providerConfig.CopilotHome,
			UseLoggedInUser:    providerConfig.UseLoggedInUser,
			UseLoggedInUserSet: providerConfig.UseLoggedInUserSet,
			CLIURL:             providerConfig.CLIURL,
			Model:              providerConfig.Model,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderCopilotSDK,
		DisplayName: "GitHub Copilot SDK",
		Order:       20,
		Capabilities: analyzer.ProviderCapabilities{
			RequiresNetwork: true,
		},
	})
}
