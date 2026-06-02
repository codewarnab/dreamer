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
	"dreamer/internal/sandbox"
)

const ID = "codex-acp"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderCodexACP, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		command := providerConfig.Command
		if len(command) == 0 {
			command = []string{"codex-acp"}
		}
		return acpcore.New(acpcore.Options{
			ID:                  ID,
			Command:             command,
			Env:                 providerConfig.Env,
			DefaultModel:        providerConfig.DefaultModel,
			Sandbox:             providerConfig.Sandbox,
			SandboxProjectWrite: providerConfig.SandboxProjectWrite,
			SandboxWritableDirs: providerConfig.SandboxWritableDirs,
			SandboxNetwork:      providerConfig.SandboxNetwork,
			SandboxSeccomp:      providerConfig.SandboxSeccomp,
			SandboxResources: sandbox.ResourceLimits{
				MemoryMB:  providerConfig.SandboxResources.MemoryMB,
				Processes: providerConfig.SandboxResources.Processes,
				FDs:       providerConfig.SandboxResources.FDs,
			},
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderCodexACP,
		DisplayName: "Codex via ACP",
		Order:       100,
		Capabilities: analyzer.ProviderCapabilities{
			RequiresNetwork: true,
		},
	})
}
