package cmd

import (
	"context"
	"fmt"
	"strings"

	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"github.com/spf13/cobra"
)

func newAnalyzeCommand() *cobra.Command {
	var (
		configPath  string
		projectPath string
		providerID  string
		force       bool
		dryRun      bool
		permissive  bool
		outputDir   string
		since       string
	)

	command := &cobra.Command{
		Use:   "analyze",
		Short: "Run one analysis pass for a single project path.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(projectPath) == "" {
				return fmt.Errorf("--path is required")
			}
			if cmd.Flags().Changed("since") && strings.TrimSpace(since) == "" {
				return fmt.Errorf("--since must not be empty")
			}

			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			cfg, err := config.LoadConfig(resolvedConfigPath)
			if err != nil {
				return fmt.Errorf("load config %q: %w", resolvedConfigPath, err)
			}

			logRoot := outputDir
			if strings.TrimSpace(logRoot) == "" {
				logRoot = cfg.Daemon.OutputRoot
			}
			logger, err := logging.New(logRoot, cfg.Logging.Level)
			if err != nil {
				return err
			}
			defer func() { _ = logger.Close() }()

			logger.Info("analyze command started", logging.Any("config", resolvedConfigPath), logging.Any("path", projectPath), logging.Any("provider", providerID))

			result, err := pipeline.Run(commandContext(cmd), pipeline.Options{
				Config:      cfg,
				ProjectPath: projectPath,
				ProviderID:  providerID,
				Force:       force,
				DryRun:      dryRun,
				Permissive:  permissive,
				OutputDir:   outputDir,
				Since:       since,
			}, logger)
			if err != nil {
				logger.Error("analyze command failed", logging.Any("err", err))
				return err
			}

			if result.CacheHit {
				cmd.Printf("no changes (cache hit) provider=%s todos=%s\n", result.ProviderID, result.TodosPath)
				logger.Info("analyze cache hit", logging.Any("provider", result.ProviderID))
				return nil
			}

			if result.NoMistakes {
				cmd.Printf("no recurring mistakes found provider=%s todos=%s\n", result.ProviderID, result.TodosPath)
				logger.Info("analyze no mistakes", logging.Any("provider", result.ProviderID))
				return nil
			}
			if dryRun {
				cmd.Printf("dry-run complete provider=%s mistakes=%d\n", result.ProviderID, result.Mistakes)
				return nil
			}

			cmd.Printf(
				"analyze complete provider=%s sources=%d messages=%d mistakes=%d findings_added=%d warnings=%d todos=%s\n",
				result.ProviderID,
				result.SourcesAnalyzed,
				result.MessagesRead,
				result.Mistakes,
				result.Findings,
				result.Warnings,
				result.TodosPath,
			)
			logger.Info("analyze complete", logging.Any("provider", result.ProviderID), logging.Any("mistakes", result.Mistakes), logging.Any("findings", result.Findings), logging.Any("todos", result.TodosPath))
			return nil
		},
	}

	command.Flags().StringVar(&configPath, "config", "", "Path to global config file (default: <UserConfigDir>/dreamer/config.yaml)")
	command.Flags().StringVar(&projectPath, "path", "", "Absolute project directory to analyze (required)")
	command.Flags().StringVar(&providerID, "provider", "", "Override the configured provider id")
	command.Flags().BoolVar(&force, "force", false, "Skip the incremental cache and re-analyze every discovered chat")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Run phase 1 only (mistake extraction); do not synthesize guardrails or write todos.md")
	command.Flags().BoolVar(&permissive, "permissive", false, "Disable strict lint-rule allow-list; emit unrecognised rule ids tagged [unverified]")
	command.Flags().StringVar(&outputDir, "output-dir", "", "Override the per-project output directory")
	command.Flags().StringVar(&since, "since", "", "Lookback window (e.g. 30m, 1h, 1d, 1w, 1mo)")
	_ = command.MarkFlagRequired("path")

	return command
}

func commandContext(cmd *cobra.Command) context.Context {
	if cmd == nil || cmd.Context() == nil {
		return context.Background()
	}
	return cmd.Context()
}
