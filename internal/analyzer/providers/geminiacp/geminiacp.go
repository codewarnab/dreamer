package geminiacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

const ID = "gemini-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderGeminiACP, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := cfg.Command
		if len(command) == 0 {
			command = []string{"gemini", "--acp"}
		}
		return acpcore.New(acpcore.Options{
			ID:      ID,
			Command: command,
			Env:     cfg.Env,
		})
	})
}
