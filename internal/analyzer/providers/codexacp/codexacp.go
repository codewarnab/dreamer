// Package codexacp wires Codex into dreamer via the stdio ACP transport.
//
// OpenAI's `codex` binary does not currently ship a native ACP server. Until
// it does, this provider talks to an ACP-compatible bridge supplied by the
// operator (e.g. an external `codex-acp` proxy). The bridge command must be
// specified in the per-provider `command` field of the config.
package codexacp

import (
	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

const ID = "codex-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderCodexACP, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := cfg.Command
		if len(command) == 0 {
			command = []string{"codex-acp"}
		}
		return acpcore.New(acpcore.Options{
			ID:           ID,
			Command:      command,
			Env:          cfg.Env,
			DefaultModel: cfg.DefaultModel,
		})
	})
}
