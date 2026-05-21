package config

import "fmt"

// providerRemediation maps provider ids to operator-facing remediation hints
// (spec §13.2). When adding a new provider, add an entry here.
var providerRemediation = map[ProviderID]string{
	ProviderCopilotSDK:     "Install GitHub Copilot CLI (`npm install -g @github/copilot`) and run `copilot` then `/login` to authenticate.",
	ProviderCopilotACP:     "Ensure `copilot --acp` starts and emits an ACP initialize response.",
	ProviderClaudeCLI:      "Install Claude Code (`npm i -g @anthropic-ai/claude-code`) and run `claude` to authenticate (use `claude setup-token` for headless environments).",
	ProviderClaudeACP:      "Ensure `claude --acp` starts and emits an ACP initialize response.",
	ProviderGeminiCLI:      "Install Gemini CLI (`npm i -g @google/gemini-cli` or `brew install gemini-cli`) and run `gemini` to authenticate (browser OAuth on first launch).",
	ProviderGeminiACP:      "Ensure `gemini --acp` starts and emits an ACP initialize response.",
	ProviderKiroACP:        "Install the Kiro CLI and ensure `kiro-cli acp` starts cleanly.",
	ProviderCodexCLI:       "Install the OpenAI Codex CLI (`npm i -g @openai/codex` or platform installer) and run `codex login`.",
	ProviderCodexACP:       "Install an ACP bridge for Codex (e.g. set `providers.codex-acp.command` to your bridge binary) and verify it speaks JSON-RPC 2.0 over stdio.",
	ProviderOpenClaudeCLI:  "Install OpenClaude (`npm i -g @gitlawb/openclaude`) and run `openclaude`, then `/provider` for guided provider setup.",
	ProviderOpenCodeACP:    "Install OpenCode (see https://opencode.ai) and ensure `opencode acp` starts cleanly.",
	ProviderOpenCodeServer: "Start OpenCode server (`opencode serve`) or configure `providers.opencode-server.base_url` to point at a running instance.",
	ProviderCodebuffSDK:    "Set `providers.codebuff-sdk.api_key_env` to the environment variable holding your Codebuff API key (or set `password`).",
}

// RemediationMessage returns the operator-facing remediation hint for a
// provider id (spec §13.2). Unknown ids get a generic ACP-style message.
func RemediationMessage(providerID string) string {
	if msg, ok := providerRemediation[ProviderID(providerID)]; ok {
		return msg
	}
	return fmt.Sprintf("Ensure the %q provider is installed and authenticated.", providerID)
}
