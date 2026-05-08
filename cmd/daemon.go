package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/logging"
	"github.com/spf13/cobra"
)

func newDaemonCommand() *cobra.Command {
	var configPath string

	command := &cobra.Command{
		Use:   "daemon",
		Short: "Run periodic analysis for all configured projects.",
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
			if len(cfg.Projects) == 0 {
				logger.Error("daemon configuration has no projects config=%q", resolvedConfigPath)
				return fmt.Errorf("config %q has no projects configured", resolvedConfigPath)
			}
			if cfg.Daemon.FrequencySeconds <= 0 {
				logger.Error("daemon frequency_seconds must be greater than zero value=%d", cfg.Daemon.FrequencySeconds)
				return fmt.Errorf("daemon frequency_seconds must be greater than zero")
			}

			frequency := time.Duration(cfg.Daemon.FrequencySeconds) * time.Second
			baseCtx := commandContext(cmd)
			ctx, stop := signal.NotifyContext(baseCtx, os.Interrupt)
			defer stop()

			cmd.Printf("daemon started: frequency=%s projects=%d\n", frequency, len(cfg.Projects))
			logger.Info("daemon started config=%q frequency=%s projects=%d", resolvedConfigPath, frequency, len(cfg.Projects))
			if err := runDaemonCycle(ctx, cfg, cmd, logger); err != nil {
				logger.Error("daemon cycle failed error=%v", err)
				cmd.Printf("daemon cycle failed: %v\n", err)
			}

			ticker := time.NewTicker(frequency)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					cmd.Printf("daemon stopped: %v\n", context.Cause(ctx))
					logger.Info("daemon stopped cause=%v", context.Cause(ctx))
					return nil
				case <-ticker.C:
					if err := runDaemonCycle(ctx, cfg, cmd, logger); err != nil {
						logger.Error("daemon cycle failed error=%v", err)
						cmd.Printf("daemon cycle failed: %v\n", err)
					}
				}
			}
		},
	}

	command.Flags().StringVar(&configPath, "config", "", "Path to config file (default: ~/.dreamer/config.yaml)")

	return command
}

func runDaemonCycle(ctx context.Context, cfg *config.Config, cmd *cobra.Command, logger *logging.Logger) error {
	logger.Info("daemon cycle started projects=%d", len(cfg.Projects))
	var cycleErrors []error
	for _, project := range cfg.Projects {
		select {
		case <-ctx.Done():
			logger.Info("daemon cycle cancelled")
			return nil
		default:
		}

		runResult, err := analyzeProject(ctx, cfg, project, logger, analyzeOptions{})
		if err != nil {
			if errors.Is(err, errNoNewChatSources) || errors.Is(err, errNoChatSources) || errors.Is(err, errNoLookbackChatSources) {
				cmd.Printf("daemon cycle skipped for %q: %v\n", project.Name, err)
				logger.Warn("daemon cycle skipped project=%q reason=%v", project.Name, err)
				continue
			}
			logger.Error("daemon project failed project=%q error=%v", project.Name, err)
			cycleErrors = append(cycleErrors, fmt.Errorf("analyze project %q: %w", project.Name, err))
			continue
		}
		cmd.Printf(
			"daemon cycle complete for %q: sources=%d messages=%d findings=%d todos_added=%d\n",
			project.Name,
			runResult.SourcesAnalyzed,
			runResult.MessagesRead,
			runResult.FindingsFound,
			runResult.TodosAdded,
		)
		logger.Info("daemon project complete project=%q sources=%d messages=%d findings=%d todos_added=%d", project.Name, runResult.SourcesAnalyzed, runResult.MessagesRead, runResult.FindingsFound, runResult.TodosAdded)
	}

	logger.Info("daemon cycle complete")
	return errors.Join(cycleErrors...)
}
