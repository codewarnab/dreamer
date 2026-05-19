package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"dreamer/internal/web"
	"github.com/fsnotify/fsnotify"
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
			overlayPath, _ := config.GlobalOverlayPath()

			cfg, err := config.LoadConfigWithOverlay(resolvedConfigPath, overlayPath)
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
			if cfg.Daemon.FrequencySeconds <= 0 {
				logger.Error("daemon frequency_seconds must be greater than zero", logging.Any("value", cfg.Daemon.FrequencySeconds))
				return fmt.Errorf("daemon frequency_seconds must be greater than zero")
			}

			lockPath := filepath.Join(cfg.Daemon.OutputRoot, "dreamer.daemon.lock")
			releaseLock, err := fsutil.AcquireLock(lockPath, logger)
			if err != nil {
				return err
			}
			defer releaseLock()

			frequency := time.Duration(cfg.Daemon.FrequencySeconds) * time.Second
			baseCtx := commandContext(cmd)
			ctx, stop := signal.NotifyContext(baseCtx, daemonSignals()...)
			defer stop()

			discoveryCache := pipeline.NewDiscoveryCache()
			events := pipeline.NewEventBus()

			if cfg.Web.Enabled != nil && *cfg.Web.Enabled {
				srv, srvErr := web.NewServer(web.Options{Config: cfg, Logger: logger, Events: events})
				if srvErr != nil {
					logger.Error("web server construct failed", logging.Any("err", srvErr))
				} else if startErr := srv.Start(); startErr != nil {
					logger.Error("web server start failed", logging.Any("err", startErr))
				} else {
					defer func() {
						shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						_ = srv.Shutdown(shutdownCtx)
					}()
				}
			}

			startConfigWatcher(ctx, logger, events, resolvedConfigPath, overlayPath)

			cmd.Printf("daemon started: frequency=%s projects=%d\n", frequency, len(cfg.Projects))
			logger.Info("daemon started", logging.Any("config", resolvedConfigPath), logging.Any("frequency", frequency), logging.Any("projects", len(cfg.Projects)))
			if err := runDaemonCycle(ctx, cfg, cmd, logger, overrides, discoveryCache, events); err != nil {
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
					if err := runDaemonCycle(ctx, cfg, cmd, logger, overrides, discoveryCache, events); err != nil {
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

func runDaemonCycle(ctx context.Context, cfg *config.Config, cmd *cobra.Command, logger *logging.Logger, overrides daemonOverrides, discoveryCache *pipeline.DiscoveryCache, events *pipeline.EventBus) error {
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
			DiscoveryCache:         discoveryCache,
			Events:                 events,
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

// startConfigWatcher spawns a goroutine bound to ctx that watches the parent
// directories of configPath and overlayPath for write events. On a successful
// reload it publishes a config.reloaded event via the bus. Reload failures are
// logged at warn level and never crash the daemon. The in-memory cfg is NOT
// swapped here; that lands in a later commit.
func startConfigWatcher(ctx context.Context, logger *logging.Logger, events *pipeline.EventBus, configPath, overlayPath string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Error("config watcher init failed", logging.Any("err", err))
		return
	}

	watched := map[string]struct{}{}
	addWatch := func(p string) {
		if p == "" {
			return
		}
		dir := filepath.Dir(p)
		if _, ok := watched[dir]; ok {
			return
		}
		if err := watcher.Add(dir); err != nil {
			logger.Error("config watcher add failed", logging.Any("dir", dir), logging.Any("err", err))
			return
		}
		watched[dir] = struct{}{}
	}
	addWatch(configPath)
	addWatch(overlayPath)

	if len(watched) == 0 {
		_ = watcher.Close()
		return
	}

	go func() {
		defer func() { _ = watcher.Close() }()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if ev.Op&fsnotify.Write == 0 && ev.Op&fsnotify.Create == 0 {
					continue
				}
				clean := filepath.Clean(ev.Name)
				if clean != filepath.Clean(configPath) && clean != filepath.Clean(overlayPath) {
					continue
				}
				newCfg, loadErr := config.LoadConfigWithOverlay(configPath, overlayPath)
				if loadErr != nil {
					logger.Info("config reload failed", logging.Any("err", loadErr))
					continue
				}
				events.Publish(pipeline.Event{
					Type: pipeline.EventConfigReload,
					Payload: map[string]any{
						"overlay":          newCfg.Notices.OverlayApplied,
						"restart_required": newCfg.Notices.RestartRequired,
					},
				})
			case werr, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Info("config watcher error", logging.Any("err", werr))
			}
		}
	}()
}
