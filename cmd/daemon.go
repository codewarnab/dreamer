package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/jobqueue"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"github.com/spf13/cobra"
)

// daemonSignals returns the OS-specific signals that trigger graceful shutdown.
// On Windows only os.Interrupt (Ctrl+C) is available. On Unix we also catch
// SIGTERM so that systemd and other process managers can stop the daemon cleanly.
func daemonSignals() []os.Signal {
	if runtime.GOOS == "windows" {
		return []os.Signal{os.Interrupt}
	}
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

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
			logger, err := logging.New(cfg.Daemon.OutputRoot, cfg.Logging.Level, cfg.Logging.MaxSizeMB)
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

			lockPath := filepath.Join(cfg.Daemon.OutputRoot, "dreamer.daemon.lock")
			// Read the stale PID before AcquireLock overwrites it with ours.
			stalePID, _ := fsutil.ReadLockPID(lockPath)
			releaseLock, err := fsutil.AcquireLock(lockPath, logger)
			if err != nil {
				return err
			}
			defer releaseLock()

			baseCtx := commandContext(cmd)
			ctx, stop := signal.NotifyContext(baseCtx, daemonSignals()...)
			defer stop()

			maxDur, err := time.ParseDuration(cfg.Daemon.MaxAnalysisDuration)
			if err != nil {
				return fmt.Errorf("parse max_analysis_duration %q: %w", cfg.Daemon.MaxAnalysisDuration, err)
			}
			retDur, err := time.ParseDuration(cfg.Daemon.JobHistoryRetention)
			if err != nil {
				return fmt.Errorf("parse job_history_retention %q: %w", cfg.Daemon.JobHistoryRetention, err)
			}

			queue := jobqueue.New(jobqueue.Options{
				StorePath:     filepath.Join(cfg.Daemon.OutputRoot, "jobs.json"),
				MaxConcurrent: cfg.Daemon.MaxConcurrentJobs,
				MaxDuration:   maxDur,
			})
			if err := queue.Recover(); err != nil {
				logger.Warn("failed to recover job queue", logging.Any("err", err))
			}
			recoverStaleJobs(queue, stalePID, logger)

			discoveryCache := pipeline.NewDiscoveryCache()
			frequency := time.Duration(cfg.Daemon.FrequencySeconds) * time.Second

			workers := newWorkerPool(ctx, queue, cfg, logger, discoveryCache, overrides)
			workers.Start()

			enqueueMissingJobs(ctx, queue, cfg, logger)

			cmd.Printf("daemon started: frequency=%s projects=%d max_concurrent=%d\n",
				frequency, len(cfg.Projects), cfg.Daemon.MaxConcurrentJobs)
			logger.Info("daemon started",
				logging.Any("config", resolvedConfigPath),
				logging.Any("frequency", frequency),
				logging.Any("projects", len(cfg.Projects)),
				logging.Any("max_concurrent", cfg.Daemon.MaxConcurrentJobs),
			)

			ticker := time.NewTicker(frequency)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					cmd.Printf("daemon stopping: %v\n", context.Cause(ctx))
					logger.Info("daemon stopping", logging.Any("cause", context.Cause(ctx)))
					queue.CancelRunning()
					workers.Stop()
					cmd.Printf("daemon stopped\n")
					return nil
				case <-ticker.C:
					queue.PruneHistory(retDur)
					enqueueMissingJobs(ctx, queue, cfg, logger)
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

