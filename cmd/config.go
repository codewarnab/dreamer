package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const defaultConfigTemplate = `projects: []
analyzer:
  model: gpt-5
  use_logged_in_user: true
  auto_start: false
  copilot_home: ""
  cli_url: ""
  rules: {}
daemon:
  frequency_seconds: 300
  log_level: info
  output_root: ~/.dreamer
`

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
		Short: "Create a default config at ~/.dreamer/config.yaml.",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, err := resolveConfigPath("")
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

	command.Flags().BoolVar(&force, "force", false, "Overwrite existing config file")
	return command
}
