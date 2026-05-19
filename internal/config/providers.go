package config

import "fmt"

// RemediationMessage returns the operator-facing remediation hint for a
// provider id (spec §13.2). Unknown ids get a generic ACP-style message.
func RemediationMessage(providerID string) string {
	switch providerID {
	case "copilot-sdk":
		return "Install GitHub Copilot CLI (`npm install -g @github/copilot`) and run `copilot` then `/login` to authenticate."
	case "copilot-acp":
		return "Ensure `copilot --acp` starts and emits an ACP initialize response."
	case "claude-cli":
		return "Install Claude Code (`npm i -g @anthropic-ai/claude-code`) and run `claude` to authenticate (use `claude setup-token` for headless environments)."
	case "claude-acp":
		return "Ensure `claude --acp` starts and emits an ACP initialize response."
	case "gemini-sdk":
		return "Export `GEMINI_API_KEY` in the environment."
	case "gemini-cli":
		return "Install Gemini CLI (`npm i -g @google/gemini-cli` or `brew install gemini-cli`) and run `gemini` to authenticate (browser OAuth on first launch)."
	case "gemini-acp":
		return "Ensure `gemini --acp` starts and emits an ACP initialize response."
	case "kiro-acp":
		return "Install the Kiro CLI and ensure `kiro-cli acp` starts cleanly."
	case "codex-cli":
		return "Install the OpenAI Codex CLI (`npm i -g @openai/codex` or platform installer) and run `codex login`."
	case "codex-acp":
		return "Install an ACP bridge for Codex (e.g. set `providers.codex-acp.command` to your bridge binary) and verify it speaks JSON-RPC 2.0 over stdio."
	case "openclaude-cli":
		return "Install OpenClaude (`npm i -g @gitlawb/openclaude`) and run `openclaude`, then `/provider` for guided provider setup."
	case "opencode-acp":
		return "Install OpenCode (see https://opencode.ai) and ensure `opencode acp` starts cleanly."
	case "opencode-server":
		return "Start OpenCode server (`opencode serve`) or configure `providers.opencode-server.base_url` to point at a running instance."
	case "codebuff-sdk":
		return "Set `providers.codebuff-sdk.api_key_env` to the environment variable holding your Codebuff API key (or set `password`)."
	default:
		return fmt.Sprintf("Ensure the %q provider is installed and authenticated.", providerID)
	}
}
