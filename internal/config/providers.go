package config

import "fmt"

// RemediationMessage returns the operator-facing remediation hint for a
// provider id (spec §13.2). Unknown ids get a generic ACP-style message.
func RemediationMessage(providerID string) string {
	switch providerID {
	case "copilot-sdk":
		return "Run `copilot auth login` (GitHub Copilot CLI must be installed)."
	case "copilot-acp":
		return "Ensure `copilot --acp` starts and emits an ACP initialize response."
	case "claude-cli":
		return "Run `claude auth login`."
	case "claude-acp":
		return "Ensure `claude --acp` starts and emits an ACP initialize response."
	case "gemini-sdk":
		return "Export `GEMINI_API_KEY` in the environment."
	case "gemini-cli":
		return "Install Gemini CLI (`npm i -g @anthropic-ai/gemini-cli` or `brew install gemini-cli`) and run `gemini auth login`."
	case "gemini-acp":
		return "Ensure `gemini --acp` starts and emits an ACP initialize response."
	case "kiro-acp":
		return "Install the Kiro CLI and ensure `kiro --acp` starts cleanly."
	case "codex-cli":
		return "Install the OpenAI Codex CLI (`npm i -g @openai/codex` or platform installer) and run `codex login`."
	case "codex-acp":
		return "Install an ACP bridge for Codex (e.g. set `providers.codex-acp.command` to your bridge binary) and verify it speaks JSON-RPC 2.0 over stdio."
	case "openclaude-cli":
		return "Install OpenClude (`npm i -g @gitlawb/openclaude`) and run `openclaude auth login`."
	default:
		return fmt.Sprintf("Ensure the %q provider is installed and authenticated.", providerID)
	}
}
