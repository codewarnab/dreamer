package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
)

func newRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a project from dreamer's config.",
		Long: "remove deletes the named project from the 'projects:' list in\n" +
			"<UserConfigDir>/dreamer/config.yaml. Comments and other keys are\n" +
			"preserved via the yaml.v3 Node API.\n\n" +
			"The project name must match exactly (case-sensitive).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("project name is required")
			}
			cfgPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
				return fmt.Errorf("config file %q does not exist; run 'dreamer setup' first", cfgPath)
			} else if err != nil {
				return fmt.Errorf("stat config %q: %w", cfgPath, err)
			}
			configBytes, err := os.ReadFile(cfgPath)
			if err != nil {
				return fmt.Errorf("read config %q: %w", cfgPath, err)
			}
			updated, err := config.RemoveProjectFromYAML(configBytes, name)
			if err != nil {
				return err
			}
			// SecretPerms (0600): config.yaml may carry provider passwords.
			if err := fsutil.WriteFileAtomic(cfgPath, updated, fsutil.SecretPerms); err != nil {
				return fmt.Errorf("write config %q: %w", cfgPath, err)
			}
			cmd.Printf("removed project %q from %s\n", name, cfgPath)
			return nil
		},
	}
}
