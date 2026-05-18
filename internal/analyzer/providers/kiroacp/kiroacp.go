package kiroacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

const ID = "kiro-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderKiroACP, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := cfg.Command
		if len(command) == 0 {
			command = []string{"kiro-cli", "acp"}
		}
		return acpcore.New(acpcore.Options{
			ID:           ID,
			Command:      command,
			Env:          cfg.Env,
			DefaultModel: cfg.DefaultModel,
		})
	})
}
