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

// DefaultModelFallbacks lists alternative models for providers that support
// automatic fallback when the primary model is unavailable.
// Used by ACP providers where the agent may not advertise the preferred model.
var DefaultModelFallbacks = map[ProviderID][]string{
	ProviderGeminiACP: {"gemini-2.5-flash"},
}
