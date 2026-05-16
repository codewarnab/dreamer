package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"dreamer/internal/config"
	"github.com/spf13/cobra"
)

var defaultConfigTemplate = fmt.Sprintf(`# dreamer global config (v1). See doc/spec.md for full schema.

# Provider used when --provider is not passed and no per-project config sets one.
# Options: copilot-sdk | copilot-acp | claude-cli | claude-acp |
#          gemini-cli | gemini-acp | kiro-acp |
#          codex-cli  | codex-acp
default_provider: copilot-sdk

# Projects iterated by the daemon. Each entry: {name, path, since}.
#   name:  output subdir under <output_root>
#   path:  absolute project directory (symlinks resolved)
#   since: optional lookback window (e.g. "30m", "1h", "1d", "1w", "1mo")
projects: []
# Example:
#   - name: dreamer
#     path: /home/me/dev/dreamer
#     since: 7d

daemon:
  # Sleep between daemon cycles. Integer seconds. Default: 3600 (1h).
  frequency_seconds: 3600
  # Output root override. Empty = <UserConfigDir>/dreamer.
  # output_root: ""

logging:
  # Verbosity. Options: error | warn | info | debug
  level: info
  # Log file path. Empty = <output_root>/dreamer.log.
  file: ""

redaction:
  # Extra regex patterns appended to the built-in secret allow-list.
  # Each match becomes [REDACTED:custom]. See spec §6 for built-ins.
  patterns: []
  # Example:
  #   - "ACME_INTERNAL_[A-Z0-9]{32}"

providers:
  copilot-sdk:
    # GitHub Copilot SDK (native Go SDK). See spec §18.
    model: %[1]s        # options: %[1]s | gpt-4.1 | gpt-5 | "" (SDK auto)
    use_logged_in_user: true    # options: true | false. Use keychain auth. Mutually exclusive with cli_url.
    auto_start: false           # options: true | false. Spawn CLI eagerly vs. on first session.
    # copilot_home: ""          # override $COPILOT_HOME. Optional.
    # cli_url: ""               # connect to headless CLI server (e.g. "localhost:4321"). Disables use_logged_in_user.

  copilot-acp:
    # Copilot via Agent Client Protocol stdio transport (spec §4.4).
    command: ["copilot", "--acp"]
    # env: {}                   # extra environment variables for the subprocess

  claude-cli:
    # Claude CLI via stream-json. Flags validated upstream; do not strip --output-format.
    command: ["claude", "-p", "--verbose", "--output-format=stream-json", "--permission-mode", "plan"]
    # env: {}

  claude-acp:
    command: ["claude", "--acp"]
    # env: {}

  gemini-cli:
    command: ["gemini", "--headless"]
    # env: {}

  gemini-acp:
    command: ["gemini", "--acp"]
    # env: {}

  kiro-acp:
    # Kiro CLI via ACP. Uses Quorinex/Kiro-Goacp internally.
    command: ["kiro", "--acp"]
    # env: {}

  codex-cli:
    # OpenAI Codex CLI via 'codex exec --json --sandbox read-only' (spec v1.1).
    # Override command to add flags like --model, --image, or to point at a wrapper.
    command: ["codex", "exec", "--json", "--sandbox", "read-only"]
    # model: ""                 # optional --model override; empty = codex default
    # env: {}                   # extra environment for the subprocess

  codex-acp:
    # Codex via an ACP bridge supplied by the operator.
    # OpenAI's 'codex' binary does not ship a native ACP server yet, so the
    # 'command' field must point at a third-party bridge that speaks
    # JSON-RPC 2.0 over stdio.
    command: ["codex-acp"]
    # env: {}

# Analyzer settings.
# rule_timeout_seconds: global timeout for every provider call (phase-1 + phase-2).
#                       Applied to all rule packs; overrides per-pack defaults.
#                       Set higher for slow providers / large transcripts.
# rules:                per-category enable/disable overrides.
# Categories: lint-rule | test | ci-check | doc | config | refactor-boundary
analyzer:
  rule_timeout_seconds: 120
  # rules:
  #   lint-rule:
  #     enabled: true
  #   refactor-boundary:
  #     enabled: false
`, config.DefaultModel)

func newConfigCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Manage Dreamer configuration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	command.AddCommand(newConfigInitCommand())
	return command
}

func newConfigInitCommand() *cobra.Command {
	var force bool

	command := &cobra.Command{
		Use:   "init",
		Short: "Create a default config at <UserConfigDir>/dreamer/config.yaml.",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, err := config.GlobalConfigPath()
			if err != nil {
				return err
			}

			if _, err := os.Stat(configPath); err == nil && !force {
				return fmt.Errorf("config file already exists at %q (use --force to overwrite)", configPath)
			}

			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				return fmt.Errorf("create config directory %q: %w", filepath.Dir(configPath), err)
			}
			if err := os.WriteFile(configPath, []byte(defaultConfigTemplate), 0o644); err != nil {
				return fmt.Errorf("write default config %q: %w", configPath, err)
			}

			cmd.Printf("initialized config at %s\n", configPath)
			return nil
		},
	}

	command.Flags().BoolVarP(&force, "force", "f", false, "Overwrite existing config file")
	return command
}
