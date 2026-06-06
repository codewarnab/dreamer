package config

import (
	"fmt"
	"sync"
)

// ProviderDefaults holds all per-provider default values in a single struct.
// Providers register via RegisterProviderDefaults in init(). This replaces
// the former parallel maps (DefaultModelByProvider, AllModelsByProvider,
// DefaultModelFallbacks, DefaultSandboxByProvider, providerRemediation).
//
// When adding a new provider, add ONE RegisterProviderDefaults call below.
type ProviderDefaults struct {
	DefaultModel   string   // default model string; "" means auto-select
	AllModels      []string // all known models for model picker; first must match DefaultModel
	ModelFallbacks []string // fallback models for ACP providers when primary unavailable
	DefaultSandbox string   // default sandbox mode: "auto", "true", or "false"
	Remediation    string   // operator-facing remediation hint (spec §13.2)
}

var (
	// providerDefaultsMu guards providerDefaults. All writes happen in init();
	// runtime access is read-only.
	providerDefaultsMu sync.RWMutex
	providerDefaults   = map[ProviderID]ProviderDefaults{}
)

// RegisterProviderDefaults stores defaults for a provider. Called from init()
// in this file. Idempotent — re-registration overwrites the prior entry.
func RegisterProviderDefaults(id ProviderID, d ProviderDefaults) {
	providerDefaultsMu.Lock()
	defer providerDefaultsMu.Unlock()
	providerDefaults[id] = d
}

// LookupProviderDefaults returns the defaults for id, or false if not registered.
func LookupProviderDefaults(id ProviderID) (ProviderDefaults, bool) {
	providerDefaultsMu.RLock()
	defer providerDefaultsMu.RUnlock()
	d, ok := providerDefaults[id]
	return d, ok
}

// AllProviderDefaults returns a copy of all registered defaults keyed by ID.
func AllProviderDefaults() map[ProviderID]ProviderDefaults {
	providerDefaultsMu.RLock()
	defer providerDefaultsMu.RUnlock()
	out := make(map[ProviderID]ProviderDefaults, len(providerDefaults))
	for id, d := range providerDefaults {
		out[id] = d
	}
	return out
}

// DefaultModelFor returns the default model for providerID, or "" if unknown.
func DefaultModelFor(providerID string) string {
	if d, ok := LookupProviderDefaults(ProviderID(providerID)); ok {
		return d.DefaultModel
	}
	return ""
}

// AllModelsFor returns all known models for providerID, or nil if unknown.
func AllModelsFor(providerID string) []string {
	if d, ok := LookupProviderDefaults(ProviderID(providerID)); ok {
		return d.AllModels
	}
	return nil
}

// DefaultSandboxFor returns the default sandbox mode for providerID, or "false" if unknown.
func DefaultSandboxFor(providerID string) string {
	if d, ok := LookupProviderDefaults(ProviderID(providerID)); ok {
		return d.DefaultSandbox
	}
	return "false"
}

// ModelFallbacksFor returns fallback models for providerID, or nil if none.
func ModelFallbacksFor(providerID string) []string {
	if d, ok := LookupProviderDefaults(ProviderID(providerID)); ok {
		return d.ModelFallbacks
	}
	return nil
}

// RemediationMessage returns the operator-facing remediation hint for
// providerID (spec §13.2). Unknown ids get a generic message.
func RemediationMessage(providerID string) string {
	if d, ok := LookupProviderDefaults(ProviderID(providerID)); ok && d.Remediation != "" {
		return d.Remediation
	}
	return fmt.Sprintf("Ensure the %q provider is installed and authenticated.", providerID)
}

// init registers defaults for every known provider. When adding a new
// provider, add a single RegisterProviderDefaults call here.
func init() {
	RegisterProviderDefaults(ProviderCopilotSDK, ProviderDefaults{
		DefaultModel:   "auto",
		AllModels:      []string{"auto"},
		DefaultSandbox: "false",
		Remediation:    "Install GitHub Copilot CLI (`npm install -g @github/copilot`) and run `copilot` then `/login` to authenticate.",
	})
	RegisterProviderDefaults(ProviderCopilotACP, ProviderDefaults{
		DefaultModel:   "auto",
		AllModels:      []string{"auto"},
		DefaultSandbox: "auto",
		Remediation:    "Ensure `copilot --acp` starts and emits an ACP initialize response.",
	})
	RegisterProviderDefaults(ProviderClaudeCLI, ProviderDefaults{
		DefaultModel:   "claude-haiku-4-5-20251001",
		AllModels:      []string{"claude-haiku-4-5-20251001", "claude-sonnet-4-5-20250929", "claude-sonnet-4-6", "claude-opus-4-6"},
		DefaultSandbox: "auto",
		Remediation:    "Install Claude Code (`npm i -g @anthropic-ai/claude-code`) and run `claude` to authenticate (use `claude setup-token` for headless environments).",
	})
	RegisterProviderDefaults(ProviderClaudeACP, ProviderDefaults{
		DefaultModel:   "claude-haiku-4-5-20251001",
		AllModels:      []string{"claude-haiku-4-5-20251001", "claude-sonnet-4-5-20250929", "claude-sonnet-4-6"},
		DefaultSandbox: "auto",
		Remediation:    "Ensure `claude --acp` starts and emits an ACP initialize response.",
	})
	RegisterProviderDefaults(ProviderGeminiCLI, ProviderDefaults{
		DefaultModel:   "gemini-3-flash-preview",
		AllModels:      []string{"gemini-3-flash-preview", "gemini-3-pro-preview", "gemini-3.1-pro-preview", "gemini-3.1-flash-lite", "gemini-3.5-flash", "gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.5-pro"},
		DefaultSandbox: "auto",
		Remediation:    "Install Gemini CLI (`npm i -g @google/gemini-cli` or `brew install gemini-cli`) and run `gemini` to authenticate (browser OAuth on first launch).",
	})
	RegisterProviderDefaults(ProviderGeminiACP, ProviderDefaults{
		DefaultModel:   "gemini-3-flash-preview",
		AllModels:      []string{"gemini-3-flash-preview", "gemini-3-pro-preview", "gemini-2.5-flash"},
		ModelFallbacks: []string{"gemini-2.5-flash"},
		DefaultSandbox: "auto",
		Remediation:    "Ensure `gemini --acp` starts and emits an ACP initialize response.",
	})
	RegisterProviderDefaults(ProviderKiroACP, ProviderDefaults{
		DefaultModel:   "claude-sonnet-4.5",
		AllModels:      []string{"claude-sonnet-4.5", "claude-sonnet-4"},
		ModelFallbacks: []string{"claude-sonnet-4-5-20250929"}, // legacy date-versioned slug
		DefaultSandbox: "auto",
		Remediation:    "Install the Kiro CLI and ensure `kiro-cli acp` starts cleanly.",
	})
	RegisterProviderDefaults(ProviderCodexCLI, ProviderDefaults{
		DefaultModel:   "gpt-5.4-mini",
		AllModels:      []string{"gpt-5.4-mini", "gpt-5.3-codex"},
		DefaultSandbox: "auto",
		Remediation:    "Install the OpenAI Codex CLI (`npm i -g @openai/codex` or platform installer) and run `codex login`.",
	})
	RegisterProviderDefaults(ProviderCodexACP, ProviderDefaults{
		DefaultModel:   "gpt-5.4-mini",
		AllModels:      []string{"gpt-5.4-mini"},
		DefaultSandbox: "auto",
		Remediation:    "Install an ACP bridge for Codex (e.g. set `providers.codex-acp.command` to your bridge binary) and verify it speaks JSON-RPC 2.0 over stdio.",
	})
	RegisterProviderDefaults(ProviderOpenClaudeCLI, ProviderDefaults{
		DefaultModel:   "mimo-v2.5-pro",
		AllModels:      []string{"mimo-v2.5-pro"},
		DefaultSandbox: "auto",
		Remediation:    "Install OpenClaude (`npm i -g @gitlawb/openclaude`) and run `openclaude`, then `/provider` for guided provider setup.",
	})
	RegisterProviderDefaults(ProviderOpenCodeACP, ProviderDefaults{
		DefaultModel:   "deepseek-v4-flash",
		AllModels:      []string{"deepseek-v4-flash"},
		DefaultSandbox: "auto",
		Remediation:    "Install OpenCode (see https://opencode.ai) and ensure `opencode acp` starts cleanly.",
	})
	RegisterProviderDefaults(ProviderOpenCodeServer, ProviderDefaults{
		DefaultModel:   "deepseek-v4-flash",
		AllModels:      []string{"deepseek-v4-flash"},
		DefaultSandbox: "false",
		Remediation:    "Start OpenCode server (`opencode serve`) or configure `providers.opencode-server.base_url` to point at a running instance.",
	})
	RegisterProviderDefaults(ProviderCodebuffSDK, ProviderDefaults{
		DefaultModel:   "claude-opus-4-6",
		AllModels:      []string{"claude-opus-4-6"},
		DefaultSandbox: "false",
		Remediation:    "Set `providers.codebuff-sdk.api_key_env` to the environment variable holding your Codebuff API key (or set `password`).",
	})
}
