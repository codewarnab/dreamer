package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/jobqueue"
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
	var afv analyzerFlagVars

	command := &cobra.Command{
		Use:   "daemon",
		Short: "Run periodic analysis for all configured projects.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Lazy-init note: subsystem initialization is gated by command,
			// not deferred via sync.Once. The web server, fsnotify watchers,
			// and job queue only start in daemon mode. The analyze command
			// only touches config, logging, and pipeline.Run. SQLite readers
			// (opencode, kiro) open per-source on demand. We considered
			// sync.Once-based lazy init but the command-level separation is
			// simpler and avoids first-use latency spikes in the hot path.
			// If a future command needs cross-cutting shared state, revisit.

			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			overlayPath, _ := config.GlobalOverlayPath()

			cfg, err := config.LoadConfigWithOverlay(resolvedConfigPath, overlayPath)
			if err != nil {
				return fmt.Errorf("load config %q: %w", resolvedConfigPath, err)
			}
			// live holds the hot-swappable config snapshot. Watcher CASs in a new
			// pointer on successful overlay reload; web + pipeline read through it.
			var live atomic.Pointer[config.Config]
			live.Store(cfg)
			logger, err := logging.New(cfg.Daemon.OutputRoot, cfg.Logging.Level, cfg.Logging.MaxSizeMB)
			if err != nil {
				return err
			}
			defer func() {
				_ = logger.Close()
			}()
			logDefaultedSinceNotices(logger, cfg)

			overrides := daemonOverrides{
				parallel:       afv.parallel,
				maxConcurrency: afv.jobs,
			}
			if cmd.Flags().Changed(flagChunkSize) {
				overrides.maxChunkBytesSet = true
				overrides.maxChunkBytes = afv.chunkSize
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
				Logger:        logger,
			})
			if err := queue.Recover(); err != nil {
				logger.Warn("failed to recover job queue", logging.Any("err", err))
			}
			recoverStaleJobs(queue, stalePID, logger)

			discoveryCache := pipeline.NewDiscoveryCache()
			events := pipeline.NewEventBus()
			frequency := time.Duration(cfg.Daemon.FrequencySeconds) * time.Second

			workers := newWorkerPool(ctx, queue, cfg, logger, discoveryCache, events, overrides)
			workers.Start()

			if cfg.Web.Enabled != nil && *cfg.Web.Enabled {
				activity := web.NewActivityRing(activityRingSize)
				go activity.Bind(ctx, events)

				restartHook := func() error {
					stop()
					return nil
				}

				runner := web.NewRunner(func(projectName string) (string, bool) {
					curCfg := live.Load()
					var proj config.ProjectConfig
					found := false
					for _, p := range curCfg.Projects {
						if p.Name == projectName {
							proj = p
							found = true
							break
						}
					}
					if !found {
						return "", false
					}
					job := queue.Enqueue(proj.Name, jobqueue.EnqueueConfig{
						ProjectPath: proj.Path,
						Provider:    resolveProvider(curCfg, proj),
						Since:       proj.Since,
					})
					if job == nil {
						return "", false
					}
					return job.ID, true
				}, logger)

				srv, srvErr := web.NewServer(web.Options{
					Config:      cfg,
					Logger:      logger,
					Events:      events,
					ConfigPtr:   &live,
					OverlayPath: overlayPath,
					Runner:      runner,
					RestartHook: restartHook,
					Activity:    activity,
				})
				if srvErr != nil {
					logger.Error("web server construct failed", logging.Any("err", srvErr))
				} else if startErr := srv.Start(); startErr != nil {
					logger.Error("web server start failed", logging.Any("err", startErr))
				} else {
					defer func() {
						shutdownCtx, cancel := context.WithTimeout(context.Background(), webShutdownTimeout)
						defer cancel()
						_ = srv.Shutdown(shutdownCtx)
					}()
				}
			}

			startConfigWatcher(ctx, logger, events, &live, resolvedConfigPath, overlayPath)

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
					// Don't call CancelRunning here: that would race with the
					// worker's own terminal write when pipeline.Run returns
					// from the cancelled ctx. Workers detect ctx.Canceled and
					// call Cancel(job) themselves; the IsTerminal guard on
					// queue mutators makes either order safe.
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

	registerAnalyzerFlags(command.Flags(), &afv)

	return command
}

// daemonOverrides carries CLI-level overrides that apply to every project per cycle.
type daemonOverrides struct {
	parallel         bool
	maxConcurrency   int
	maxChunkBytes    int
	maxChunkBytesSet bool
}

// startConfigWatcher spawns a goroutine bound to ctx that watches the parent
// directories of configPath and overlayPath for write events. On a successful
// reload it CAS-swaps the live config pointer and publishes a config.reloaded
// event via the bus. Reload failures leave the prior pointer value active and
// are logged at info level; they never crash the daemon.
func startConfigWatcher(ctx context.Context, logger *logging.Logger, events *pipeline.EventBus, live *atomic.Pointer[config.Config], configPath, overlayPath string) {
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
		// Debounce coalesces editor save bursts (vim/VS Code emit several
		// WRITE/CREATE events per save via temp+rename) into a single
		// reload + config.reloaded publish.
		const debounce = 200 * time.Millisecond
		var pending *time.Timer
		var pendingC <-chan time.Time
		reload := func() {
			newCfg, loadErr := config.LoadConfigWithOverlay(configPath, overlayPath)
			if loadErr != nil {
				logger.Info("config reload failed", logging.Any("err", loadErr))
				return
			}
			if live != nil {
				live.Store(newCfg)
			}
			events.Publish(pipeline.Event{
				Type: pipeline.EventConfigReload,
				Payload: map[string]any{
					"overlay":          newCfg.Notices.OverlayApplied,
					"restart_required": newCfg.Notices.RestartRequired,
				},
			})
		}
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
				if pending != nil {
					pending.Stop()
				}
				pending = time.NewTimer(debounce)
				pendingC = pending.C
			case <-pendingC:
				pendingC = nil
				reload()
			case werr, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Info("config watcher error", logging.Any("err", werr))
			}
		}
	}()
}
