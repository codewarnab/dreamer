package config

// ProviderID identifies a known provider implementation. Values match the
// strings used in the configuration system (§3, §4.1 of docs/spec.md).
// This is the canonical definition; analyzer re-exports it for backward
// compatibility with provider packages.
type ProviderID string

const (
	ProviderCopilotSDK     ProviderID = "copilot-sdk"
	ProviderCopilotACP     ProviderID = "copilot-acp"
	ProviderClaudeCLI      ProviderID = "claude-cli"
	ProviderClaudeACP      ProviderID = "claude-acp"
	ProviderGeminiCLI      ProviderID = "gemini-cli"
	ProviderGeminiACP      ProviderID = "gemini-acp"
	ProviderKiroACP        ProviderID = "kiro-acp"
	ProviderCodexCLI       ProviderID = "codex-cli"
	ProviderCodexACP       ProviderID = "codex-acp"
	ProviderOpenClaudeCLI  ProviderID = "openclaude-cli"
	ProviderOpenCodeACP    ProviderID = "opencode-acp"
	ProviderOpenCodeServer ProviderID = "opencode-server"
	ProviderCodebuffSDK    ProviderID = "codebuff-sdk"
)
