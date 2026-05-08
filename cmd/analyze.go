package cmd

import (
	"context"
	"fmt"

	"dreamer/internal/config"
	"dreamer/internal/logging"
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
			logger, err := logging.New(cfg.Daemon.OutputRoot, cfg.Daemon.LogLevel)
			if err != nil {
				return err
			}
			defer func() {
				_ = logger.Close()
			}()
			logger.Info("analyze command started config=%q project=%q", resolvedConfigPath, projectName)

			project, err := selectProject(cfg, projectName)
			if err != nil {
				logger.Error("select project failed project=%q error=%v", projectName, err)
				return err
			}

			runResult, err := analyzeProject(commandContext(cmd), cfg, *project, logger)
			if err != nil {
				logger.Error("analyze command failed project=%q error=%v", project.Name, err)
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
			logger.Info("analyze command complete project=%q log_path=%q", project.Name, logger.Path())
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
