package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"dreamer/internal/config"
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
			if len(cfg.Projects) == 0 {
				return fmt.Errorf("config %q has no projects configured", resolvedConfigPath)
			}
			if cfg.Daemon.FrequencySeconds <= 0 {
				return fmt.Errorf("daemon frequency_seconds must be greater than zero")
			}

			frequency := time.Duration(cfg.Daemon.FrequencySeconds) * time.Second
			baseCtx := commandContext(cmd)
			ctx, stop := signal.NotifyContext(baseCtx, os.Interrupt)
			defer stop()

			cmd.Printf("daemon started: frequency=%s projects=%d\n", frequency, len(cfg.Projects))
			if err := runDaemonCycle(ctx, cfg, cmd); err != nil {
				return err
			}

			ticker := time.NewTicker(frequency)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					cmd.Printf("daemon stopped: %v\n", context.Cause(ctx))
					return nil
				case <-ticker.C:
					if err := runDaemonCycle(ctx, cfg, cmd); err != nil {
						return err
					}
				}
			}
		},
	}

	command.Flags().StringVar(&configPath, "config", "", "Path to config file (default: ~/.dreamer/config.yaml)")

	return command
}

func runDaemonCycle(ctx context.Context, cfg *config.Config, cmd *cobra.Command) error {
	for _, project := range cfg.Projects {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		runResult, err := analyzeProject(ctx, cfg, project)
		if err != nil {
			if errors.Is(err, errNoNewChatSources) || errors.Is(err, errNoChatSources) {
				cmd.Printf("daemon cycle skipped for %q: %v\n", project.Name, err)
				continue
			}
			return fmt.Errorf("analyze project %q: %w", project.Name, err)
		}
		cmd.Printf(
			"daemon cycle complete for %q: sources=%d messages=%d findings=%d todos_added=%d\n",
			project.Name,
			runResult.SourcesAnalyzed,
			runResult.MessagesRead,
			runResult.FindingsFound,
			runResult.TodosAdded,
		)
	}

	return nil
}
