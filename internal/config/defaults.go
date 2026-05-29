package config

// DefaultModelByProvider maps each provider id to its default model string.
// Empty string means "let the provider/agent auto-select".
// Users override via providers.<id>.model in config YAML.
// Keys use ProviderID constants from this package to stay in sync.
var DefaultModelByProvider = map[ProviderID]string{
	ProviderCopilotSDK:     "auto",
	ProviderCopilotACP:     "auto",
	ProviderClaudeCLI:      "claude-haiku-4-5-20251001",
	ProviderClaudeACP:      "claude-haiku-4-5-20251001",
	ProviderGeminiCLI:      "gemini-3-flash-preview",
	ProviderGeminiACP:      "gemini-3-flash-preview",
	ProviderKiroACP:        "claude-sonnet-4-5-20250929",
	ProviderCodexCLI:       "gpt-5.4-mini",
	ProviderCodexACP:       "gpt-5.4-mini",
	ProviderOpenClaudeCLI:  "mimo-v2.5-pro",
	ProviderOpenCodeACP:    "deepseek-v4-flash",
	ProviderOpenCodeServer: "deepseek-v4-flash",
	ProviderCodebuffSDK:    "claude-opus-4-7",
}

// AllModelsByProvider lists every known model for each provider, ordered
// with the primary (default) model first. Used by the setup and job
// wizards to populate model picker lists. The first entry must match
// DefaultModelByProvider for the same provider.
var AllModelsByProvider = map[ProviderID][]string{
	ProviderCopilotSDK:     {"auto"},
	ProviderCopilotACP:     {"auto"},
	ProviderClaudeCLI:      {"claude-haiku-4-5-20251001", "claude-sonnet-4-5-20250929", "claude-opus-4-7"},
	ProviderClaudeACP:      {"claude-haiku-4-5-20251001", "claude-sonnet-4-5-20250929"},
	ProviderGeminiCLI:      {"gemini-3-flash-preview", "gemini-2.5-flash", "gemini-2.5-pro"},
	ProviderGeminiACP:      {"gemini-3-flash-preview", "gemini-2.5-flash"},
	ProviderKiroACP:        {"claude-sonnet-4-5-20250929"},
	ProviderCodexCLI:       {"gpt-5.4-mini", "gpt-5.3-codex"},
	ProviderCodexACP:       {"gpt-5.4-mini"},
	ProviderOpenClaudeCLI:  {"mimo-v2.5-pro"},
	ProviderOpenCodeACP:    {"deepseek-v4-flash"},
	ProviderOpenCodeServer: {"deepseek-v4-flash"},
	ProviderCodebuffSDK:    {"claude-opus-4-7"},
}

// DefaultModelFallbacks lists alternative models for providers that support
// automatic fallback when the primary model is unavailable.
// Used by ACP providers where the agent may not advertise the preferred model.
var DefaultModelFallbacks = map[ProviderID][]string{
	ProviderGeminiACP: {"gemini-2.5-flash"},
}

// DefaultSandboxByProvider maps each provider id to its default sandbox mode.
// Providers that shell out to child processes with policy-only flags default
// to "auto" (use OS sandbox if available). Providers that don't spawn child
// processes (copilot-sdk, codebuff-sdk, opencode-server) default to "false".
// ACP providers use "auto" since they also spawn child processes.
var DefaultSandboxByProvider = map[ProviderID]string{
	ProviderCopilotSDK:     "false",
	ProviderCopilotACP:     "auto",
	ProviderClaudeCLI:      "auto",
	ProviderClaudeACP:      "auto",
	ProviderGeminiCLI:      "auto",
	ProviderGeminiACP:      "auto",
	ProviderKiroACP:        "auto",
	ProviderCodexCLI:       "auto",
	ProviderCodexACP:       "auto",
	ProviderOpenClaudeCLI:  "auto",
	ProviderOpenCodeACP:    "auto",
	ProviderOpenCodeServer: "false",
	ProviderCodebuffSDK:    "false",
}
