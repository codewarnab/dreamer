package copilotacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

const ID = "copilot-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderCopilotACP, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := cfg.Command
		if len(command) == 0 {
			command = []string{"copilot", "--acp"}
		}
		return acpcore.New(acpcore.Options{
			ID:           ID,
			Command:      command,
			Env:          cfg.Env,
			DefaultModel: cfg.DefaultModel,
		})
	})
}
