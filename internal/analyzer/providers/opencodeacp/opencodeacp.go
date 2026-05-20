package opencodeacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

const ID = "opencode-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderOpenCodeACP, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := cfg.Command
		if len(command) == 0 {
			command = []string{"opencode", "acp"}
		}
		return acpcore.New(acpcore.Options{
			ID:           ID,
			Command:      command,
			Env:          cfg.Env,
			DefaultModel: cfg.DefaultModel,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderOpenCodeACP,
		DisplayName: "OpenCode via ACP",
		Order:       110,
	})
}
