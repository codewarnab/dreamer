package cmd

import (
	"context"
	"fmt"

	"dreamer/internal/config"
	"github.com/spf13/cobra"
)

func newAnalyzeCommand() *cobra.Command {
	var configPath string
	var projectName string

	command := &cobra.Command{
		Use:   "analyze",
		Short: "Run one analysis pass for a configured project.",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			cfg, err := config.LoadConfig(resolvedConfigPath)
			if err != nil {
				return fmt.Errorf("load config %q: %w", resolvedConfigPath, err)
			}

			project, err := selectProject(cfg, projectName)
			if err != nil {
				return err
			}

			runResult, err := analyzeProject(commandContext(cmd), cfg, *project)
			if err != nil {
				return err
			}

			cmd.Printf(
				"analysis complete for %q: sources=%d messages=%d findings=%d todos_added=%d todos_path=%s\n",
				project.Name,
				runResult.SourcesAnalyzed,
				runResult.MessagesRead,
				runResult.FindingsFound,
				runResult.TodosAdded,
				runResult.TodosPath,
			)
			return nil
		},
	}

	command.Flags().StringVar(&configPath, "config", "", "Path to config file (default: ~/.dreamer/config.yaml)")
	command.Flags().StringVar(&projectName, "project", "", "Project name from config")
	_ = command.MarkFlagRequired("project")

	return command
}

func commandContext(cmd *cobra.Command) context.Context {
	if cmd == nil || cmd.Context() == nil {
		return context.Background()
	}
	return cmd.Context()
}
