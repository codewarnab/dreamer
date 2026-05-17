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
	"dreamer/internal/pipeline"
	"github.com/spf13/cobra"
)

func newDaemonCommand() *cobra.Command {
	var (
		configPath     string
		parallel       bool
		maxConcurrency int
		maxChunkBytes  int
	)

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
			logger, err := logging.New(cfg.Daemon.OutputRoot, cfg.Logging.Level)
			if err != nil {
				return err
			}
			defer func() {
				_ = logger.Close()
			}()
			logDefaultedSinceNotices(logger, cfg)

			overrides := daemonOverrides{
				parallel:       parallel,
				maxConcurrency: maxConcurrency,
			}
			if cmd.Flags().Changed("max-chunk-bytes") {
				overrides.maxChunkBytesSet = true
				overrides.maxChunkBytes = maxChunkBytes
			}
			if len(cfg.Projects) == 0 {
				logger.Error("daemon configuration has no projects", logging.Any("config", resolvedConfigPath))
				return fmt.Errorf("config %q has no projects configured", resolvedConfigPath)
			}
			if cfg.Daemon.FrequencySeconds <= 0 {
				logger.Error("daemon frequency_seconds must be greater than zero", logging.Any("value", cfg.Daemon.FrequencySeconds))
				return fmt.Errorf("daemon frequency_seconds must be greater than zero")
			}

			frequency := time.Duration(cfg.Daemon.FrequencySeconds) * time.Second
			baseCtx := commandContext(cmd)
			ctx, stop := signal.NotifyContext(baseCtx, os.Interrupt)
			defer stop()

			cmd.Printf("daemon started: frequency=%s projects=%d\n", frequency, len(cfg.Projects))
			logger.Info("daemon started", logging.Any("config", resolvedConfigPath), logging.Any("frequency", frequency), logging.Any("projects", len(cfg.Projects)))
			if err := runDaemonCycle(ctx, cfg, cmd, logger, overrides); err != nil {
				logger.Error("daemon cycle failed", logging.Any("err", err))
				cmd.Printf("daemon cycle failed: %v\n", err)
			}

			ticker := time.NewTicker(frequency)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					cmd.Printf("daemon stopped: %v\n", context.Cause(ctx))
					logger.Info("daemon stopped", logging.Any("cause", context.Cause(ctx)))
					return nil
				case <-ticker.C:
					if err := runDaemonCycle(ctx, cfg, cmd, logger, overrides); err != nil {
						logger.Error("daemon cycle failed", logging.Any("err", err))
						cmd.Printf("daemon cycle failed: %v\n", err)
					}
				}
			}
		},
	}

	command.Flags().StringVar(&configPath, "config", "", "Path to config file (default: <UserConfigDir>/dreamer/config.yaml)")
	command.Flags().BoolVar(&parallel, "parallel", false, "Force analyzer.execution.mode=parallel (provider must implement ParallelCapable; fallback logged)")
	command.Flags().IntVar(&maxConcurrency, "max-concurrency", 0, "Cap parallel session count. 0 = len(chunks). Ignored when sequential.")
	command.Flags().IntVar(&maxChunkBytes, "max-chunk-bytes", 0, "Override analyzer.chunking.max_chunk_bytes for every cycle. 0 disables chunking.")

	return command
}

// daemonOverrides carries CLI-level overrides that apply to every project per cycle.
type daemonOverrides struct {
	parallel         bool
	maxConcurrency   int
	maxChunkBytes    int
	maxChunkBytesSet bool
}

func runDaemonCycle(ctx context.Context, cfg *config.Config, cmd *cobra.Command, logger *logging.Logger, overrides daemonOverrides) error {
	logger.Info("daemon cycle started", logging.Any("projects", len(cfg.Projects)))
	var cycleErrors []error
	for _, project := range cfg.Projects {
		select {
		case <-ctx.Done():
			logger.Info("daemon cycle cancelled")
			return nil
		default:
		}

		opts := pipeline.Options{
			Config:                 cfg,
			ProjectPath:            project.Path,
			ProjectName:            project.Name,
			Since:                  project.Since,
			ParallelOverride:       overrides.parallel,
			MaxConcurrencyOverride: overrides.maxConcurrency,
		}
		if overrides.maxChunkBytesSet {
			opts.MaxChunkBytesOverride = overrides.maxChunkBytes
			opts.MaxChunkBytesOverrideSet = true
		}
		result, err := pipeline.Run(ctx, opts, logger)
		if err != nil {
			logger.Error("daemon project failed",
				append(logging.ErrAttr(err), logging.Any("project", project.Name))...)
			cmd.Printf("daemon cycle failed for %q: %v\n", project.Name, err)
			cycleErrors = append(cycleErrors, fmt.Errorf("analyze project %q: %w", project.Name, err))
			continue
		}
		if result.CacheHit {
			cmd.Printf("daemon cycle no-op for %q (cache hit)\n", project.Name)
			logger.Info("daemon project cache hit", logging.Any("project", project.Name))
			continue
		}
		cmd.Printf(
			"daemon cycle complete for %q: sources=%d messages=%d mistakes=%d findings_added=%d\n",
			project.Name,
			result.SourcesAnalyzed,
			result.MessagesRead,
			result.Mistakes,
			result.Findings,
		)
		logger.Info("daemon project complete", logging.Any("project", project.Name), logging.Any("sources", result.SourcesAnalyzed), logging.Any("messages", result.MessagesRead), logging.Any("mistakes", result.Mistakes), logging.Any("findings", result.Findings))
	}

	logger.Info("daemon cycle complete")
	return errors.Join(cycleErrors...)
}
