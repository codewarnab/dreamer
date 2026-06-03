package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
)

func newAddCommand() *cobra.Command {
	var (
		name  string
		since string
	)
	cmd := &cobra.Command{
		Use:   "add [path]",
		Short: "Add a project to dreamer's config (path defaults to current directory).",
		Long: "add appends an entry to the 'projects:' list in <UserConfigDir>/dreamer/config.yaml.\n\n" +
			"path defaults to '.' (current working directory). The path is resolved to an absolute\n" +
			"path; --name defaults to the basename; --since defaults to '24h'. Comments and other\n" +
			"keys in config.yaml are preserved via the yaml.v3 Node API.\n\n" +
			"If config.yaml does not yet exist, run 'dreamer setup' first.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pathArg := "."
			if len(args) == 1 {
				pathArg = args[0]
			}
			absPath, err := resolveAddPath(pathArg)
			if err != nil {
				return err
			}
			if name == "" {
				name = filepath.Base(absPath)
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
			// Append the project details to the config YAML. We call the centralized config.AppendProjectToYAML
			// function which is shared with the Web server handlers to ensure unified validation and duplicate checks.
			updated, err := config.AppendProjectToYAML(configBytes, name, absPath, since)
			if err != nil {
				return err
			}
			// SecretPerms (0600): config.yaml may carry provider passwords;
			// keep it consistent with `dreamer remove` and the web writers.
			if err := fsutil.WriteFileAtomic(cfgPath, updated, fsutil.SecretPerms); err != nil {
				return fmt.Errorf("write config %q: %w", cfgPath, err)
			}
			cmd.Printf("added project %q (path=%s since=%s) to %s\n", name, absPath, since, cfgPath)
			return nil
		},
	}
	cmd.Flags().StringVarP(&name, "name", "n", "", "Project name (default: basename of path).")
	cmd.Flags().StringVarP(&since, "since", "s", config.DefaultSince, "Lookback window for chat history (e.g. 30m, 1h, 1d, 1w, 1mo, lifetime)")
	return cmd
}

// resolveAddPath turns the CLI arg into an absolute, existing directory path.
func resolveAddPath(pathArg string) (string, error) {
	pathArg = strings.TrimSpace(pathArg)
	if pathArg == "" {
		pathArg = "."
	}
	expanded, err := config.ExpandUserHome(pathArg)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", pathArg, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("path %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", abs)
	}
	return abs, nil
}
