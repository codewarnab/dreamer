package geminiacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
	"dreamer/internal/config"
)

const ID = "gemini-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderGeminiACP, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := providerConfig.Command
		if len(command) == 0 {
			command = []string{"gemini", "--acp"}
		}
		return acpcore.New(acpcore.Options{
			ID:             ID,
			Command:        command,
			Env:            providerConfig.Env,
			DefaultModel:   providerConfig.DefaultModel,
			ModelFallbacks: config.DefaultModelFallbacks[config.ProviderGeminiACP],
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderGeminiACP,
		DisplayName: "Gemini via ACP",
		Order:       70,
	})
}
