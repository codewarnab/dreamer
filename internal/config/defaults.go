package config

// DefaultModelByProvider maps each provider id to its default model string.
// Empty string means "let the provider/agent auto-select".
// Users override via providers.<id>.model in config YAML.
var DefaultModelByProvider = map[string]string{
	"copilot-sdk":     "auto",
	"copilot-acp":     "auto",
	"claude-cli":      "claude-haiku-4-5-20251001",
	"claude-acp":      "claude-haiku-4-5-20251001",
	"gemini-cli":      "gemini-3-flash-preview",
	"gemini-acp":      "gemini-3-flash-preview",
	"kiro-acp":        "claude-sonnet-4-5-20250929",
	"codex-cli":       "gpt-5.4-mini",
	"codex-acp":       "gpt-5.4-mini",
	"openclaude-cli":  "mimo-v2.5-pro",
	"opencode-acp":    "deepseek-v4-flash",
	"opencode-server": "deepseek-v4-flash",
	"codebuff-sdk":    "claude-opus-4-7",
}

// DefaultModelFallbacks lists alternative models for providers that support
// automatic fallback when the primary model is unavailable.
// Used by ACP providers where the agent may not advertise the preferred model.
var DefaultModelFallbacks = map[string][]string{
	"gemini-acp": {"gemini-2.5-flash"},
}
