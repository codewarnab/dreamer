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
	var analyzerFlags analyzerFlagVars

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

			cfg, logger, err := loadDaemonConfig(resolvedConfigPath, overlayPath)
			if err != nil {
				return err
			}
			defer func() { _ = logger.Close() }()

			overrides := daemonOverrides{
				parallel:       analyzerFlags.parallel,
				maxConcurrency: analyzerFlags.jobs,
			}
			if cmd.Flags().Changed(flagChunkSize) {
				overrides.maxChunkBytesSet = true
				overrides.maxChunkBytes = analyzerFlags.chunkSize
			}
			if len(cfg.Projects) == 0 {
				logger.Error("daemon configuration has no projects", logging.Any("config", resolvedConfigPath))
				return fmt.Errorf("config %q has no projects configured", resolvedConfigPath)
			}

			baseCtx := commandContext(cmd)
			ctx, stop, queue, discoveryCache, events, releaseLock, err := initDaemonRuntime(baseCtx, cfg, logger)
			if err != nil {
				return err
			}
			defer stop()
			defer releaseLock()

			// Sweep leftover Phase 2 findings temp files from prior runs
			// that crashed or were killed before their defer fired. The
			// files only live one analysis run, so any that survived from
			// a prior process are stale.
			if swept := sweepStaleFindingsTempFiles(); swept > 0 {
				logger.Info("swept stale phase-2 findings temp files", logging.Any("count", swept))
			}

			workers := newWorkerPool(ctx, queue, cfg, logger, discoveryCache, events, overrides)
			workers.Start()

			var live atomic.Pointer[config.Config]
			live.Store(cfg)
			webDone := startWebIfEnabled(ctx, cfg, &live, queue, events, logger, overlayPath, stop)

			startConfigWatcher(ctx, logger, events, &live, resolvedConfigPath, overlayPath)
			enqueueMissingJobs(ctx, queue, cfg, logger)

			frequency := time.Duration(cfg.Daemon.FrequencySeconds) * time.Second
			cmd.Printf("daemon started: frequency=%s projects=%d max_concurrent=%d\n",
				frequency, len(cfg.Projects), cfg.Daemon.MaxConcurrentJobs)
			logger.Info("daemon started",
				logging.Any("config", resolvedConfigPath),
				logging.Any("frequency", frequency),
				logging.Any("projects", len(cfg.Projects)),
				logging.Any("max_concurrent", cfg.Daemon.MaxConcurrentJobs),
			)

			retDur, _ := time.ParseDuration(cfg.Daemon.JobHistoryRetention)
			runDaemonLoop(ctx, cmd, queue, cfg, frequency, retDur, logger, workers)
			<-webDone
			return nil
		},
	}

	registerAnalyzerFlags(command.Flags(), &analyzerFlags)

	return command
}

// daemonOverrides carries CLI-level overrides that apply to every project per cycle.
type daemonOverrides struct {
	parallel         bool
	maxConcurrency   int
	maxChunkBytes    int
	maxChunkBytesSet bool
}

// loadDaemonConfig resolves the config path, loads config with overlay, and
// creates the logger. Returns the config, logger, and any error.
func loadDaemonConfig(configPath, overlayPath string) (*config.Config, *logging.Logger, error) {
	cfg, err := config.LoadConfigWithOverlay(configPath, overlayPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config %q: %w", configPath, err)
	}
	logger, err := logging.New(cfg.Daemon.OutputRoot, cfg.Logging.Level, cfg.Logging.MaxSizeMB)
	if err != nil {
		return nil, nil, err
	}
	logDefaultedSinceNotices(logger, cfg)
	return cfg, logger, nil
}

// initDaemonRuntime acquires the daemon lock, sets up signal-aware context,
// creates the job queue with recovery, and initializes the discovery cache
// and event bus. Returns cleanup functions that the caller must defer.
func initDaemonRuntime(baseCtx context.Context, cfg *config.Config, logger *logging.Logger) (
	ctx context.Context, stop context.CancelFunc,
	queue *jobqueue.Queue, discoveryCache *pipeline.DiscoveryCache,
	events *pipeline.EventBus, releaseLock func(), err error,
) {
	maxDur, perr := time.ParseDuration(cfg.Daemon.MaxAnalysisDuration)
	if perr != nil {
		err = fmt.Errorf("parse max_analysis_duration %q: %w", cfg.Daemon.MaxAnalysisDuration, perr)
		return
	}

	lockPath := filepath.Join(cfg.Daemon.OutputRoot, "dreamer.daemon.lock")
	stalePID, _ := fsutil.ReadLockPID(lockPath)
	releaseLock, err = fsutil.AcquireLock(lockPath, logger)
	if err != nil {
		return
	}

	ctx, stop = signal.NotifyContext(baseCtx, daemonSignals()...)

	queue = jobqueue.New(jobqueue.Options{
		StorePath:     filepath.Join(cfg.Daemon.OutputRoot, "jobs.json"),
		MaxConcurrent: cfg.Daemon.MaxConcurrentJobs,
		MaxDuration:   maxDur,
		Logger:        logger,
	})
	if rerr := queue.Recover(); rerr != nil {
		logger.Warn("failed to recover job queue", logging.Any("err", rerr))
	}
	recoverStaleJobs(queue, stalePID, logger)

	discoveryCache = pipeline.NewDiscoveryCache()
	events = pipeline.NewEventBus()
	return
}

// startWebIfEnabled starts the embedded web server when cfg.Web.Enabled is true.
func startWebIfEnabled(ctx context.Context, cfg *config.Config, live *atomic.Pointer[config.Config], queue *jobqueue.Queue, events *pipeline.EventBus, logger *logging.Logger, overlayPath string, stop context.CancelFunc) <-chan struct{} {
	done := make(chan struct{})
	if cfg.Web.Enabled == nil || !*cfg.Web.Enabled {
		close(done)
		return done
	}

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
		ConfigPtr:   live,
		OverlayPath: overlayPath,
		Runner:      runner,
		RestartHook: restartHook,
		Activity:    activity,
	})
	if srvErr != nil {
		logger.Error("web server construct failed", logging.Any("err", srvErr))
		close(done)
		return done
	}
	if startErr := srv.Start(); startErr != nil {
		logger.Error("web server start failed", logging.Any("err", startErr))
		close(done)
		return done
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), webShutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		close(done)
	}()
	return done
}

// runDaemonLoop runs the main daemon scheduling loop until ctx is cancelled.
func runDaemonLoop(ctx context.Context, cmd *cobra.Command, queue *jobqueue.Queue, cfg *config.Config, frequency, retDur time.Duration, logger *logging.Logger, workers *workerPool) {
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
			return
		case <-ticker.C:
			queue.PruneHistory(retDur)
			enqueueMissingJobs(ctx, queue, cfg, logger)
		}
	}
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

// sweepStaleFindingsTempFiles deletes leftover dreamer-findings-*.jsonl
// files in os.TempDir(). These are Phase 2 temp files written by prior
// daemon runs that crashed or were killed before the per-run defer ran.
// Returns the count of files removed. Errors removing individual files
// are swallowed silently — the worst case is a small amount of temp-dir
// clutter on the next sweep.
func sweepStaleFindingsTempFiles() int {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "dreamer-findings-*.jsonl"))
	if err != nil {
		return 0
	}
	removed := 0
	for _, p := range matches {
		if err := os.Remove(p); err == nil {
			removed++
		}
	}
	return removed
}
