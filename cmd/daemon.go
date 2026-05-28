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

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/jobqueue"
	"dreamer/internal/logging"
	"dreamer/internal/mcpserver"
	"dreamer/internal/pipeline"
	"dreamer/internal/state"
	"dreamer/internal/web"
	"dreamer/internal/web/handlers"
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
				forceParallel:       analyzerFlags.parallel,
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

			stateCache := state.NewStateCache()

			var live atomic.Pointer[config.App]
			live.Store(cfg)

			workers := newWorkerPool(ctx, queue, cfg, &live, logger, discoveryCache, stateCache, events, overrides)
			workers.Start()
			webDone := startWebIfEnabled(ctx, cfg, &live, queue, events, logger, overlayPath, stop, resolvedConfigPath, stateCache)

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

			retentionDur, _ := time.ParseDuration(cfg.Daemon.JobHistoryRetention)
			runDaemonLoop(ctx, cmd, queue, cfg, frequency, retentionDur, logger, workers)
			<-webDone
			return nil
		},
	}

	registerAnalyzerFlags(command.Flags(), &analyzerFlags)

	return command
}

// daemonOverrides carries CLI-level overrides that apply to every project per cycle.
type daemonOverrides struct {
	forceParallel         bool
	maxConcurrency   int
	maxChunkBytes    int
	maxChunkBytesSet bool
}

// loadDaemonConfig resolves the config path, loads config with overlay, and
// creates the logger. Returns the config, logger, and any error.
func loadDaemonConfig(configPath, overlayPath string) (*config.App, *logging.Logger, error) {
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
func initDaemonRuntime(baseCtx context.Context, cfg *config.App, logger *logging.Logger) (
	ctx context.Context, stop context.CancelFunc,
	queue *jobqueue.Queue, discoveryCache *pipeline.DiscoveryCache,
	events *pipeline.EventBus, releaseLock func(), err error,
) {
	maxDur, parseErr := time.ParseDuration(cfg.Daemon.MaxAnalysisDuration)
	if parseErr != nil {
		err = fmt.Errorf("parse max_analysis_duration %q: %w", cfg.Daemon.MaxAnalysisDuration, parseErr)
		return
	}

	lockPath := filepath.Join(cfg.Daemon.OutputRoot, "dreamer.daemon.lock")
	stalePID, readErr := fsutil.ReadLockPID(lockPath)
	if readErr != nil {
		logger.Warn("read lock PID failed; stale-job recovery will reap unconditionally",
			logging.Any("lock_path", lockPath),
			logging.Any("err", readErr),
		)
	}
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
	if recoverErr := queue.Recover(); recoverErr != nil {
		logger.Warn("failed to recover job queue", logging.Any("err", recoverErr))
	}
	recoverStaleJobs(queue, stalePID, logger)

	discoveryCache = pipeline.NewDiscoveryCache()
	events = pipeline.NewEventBus()
	return
}

// startWebIfEnabled starts the embedded web server when cfg.Web.Enabled is true.
func startWebIfEnabled(ctx context.Context, cfg *config.App, live *atomic.Pointer[config.App], queue *jobqueue.Queue, events *pipeline.EventBus, logger *logging.Logger, overlayPath string, stop context.CancelFunc, configPath string, stateCache *state.StateCache) <-chan struct{} {
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

	enqueueRun := func(projectName string) (string, bool, error) {
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
			return "", false, nil
		}
		job := queue.Enqueue(proj.Name, jobqueue.EnqueueConfig{
			ProjectPath: proj.Path,
			Provider:    resolveProvider(curCfg, proj),
			Since:       proj.Since,
		})
		if job == nil {
			return "", false, nil
		}
		return job.ID, true, nil
	}

	// Wire background job dependencies for the web UI.
	outputRoot := cfg.Daemon.OutputRoot
	store := backgroundjobs.NewStore(outputRoot, logger)
	runStore := backgroundjobs.NewRunStore(store.Dir(), logger)
	auditWriter := backgroundjobs.NewAuditWriter(store.Dir())
	installID, _ := backgroundjobs.ResolveInstallID(store.Dir())

	var scheduler backgroundjobs.Scheduler
	if s, schedErr := buildScheduler(outputRoot, configPath); schedErr == nil {
		scheduler = s
	} else {
		logger.Warn("background job scheduler unavailable", logging.Any("err", schedErr))
	}

	var selfRepair *backgroundjobs.SelfRepairConfig
	if scheduler != nil {
		execPath, _ := os.Executable()
		execPath, _ = filepath.EvalSymlinks(execPath)
		selfRepair = &backgroundjobs.SelfRepairConfig{
			Scheduler:      scheduler,
			ExecutablePath: execPath,
			InstallID:      installID,
			ConfigHash:     backgroundjobs.HashConfigPath(configPath),
			ExecHash:       backgroundjobs.HashExecutablePath(execPath),
		}
	}

	executor := &backgroundjobs.Executor{
		Store:       store,
		RunStore:    runStore,
		AuditWriter: auditWriter,
		ConfigPath:  configPath,
		Logger:      logger,
		NewProvider: defaultProviderFactory,
		SelfRepair:  selfRepair,
	}

	jobsDeps := handlers.JobDeps{
		Store:     store,
		Runs:      runStore,
		Audit:     auditWriter,
		Scheduler: scheduler,
		Executor:  executor,
		InstallID: installID,
		Events: &handlers.EventSink{
			PublishFunc: func(evt string, payload map[string]any) {
				if events != nil {
					events.Publish(pipeline.Event{Type: evt, Payload: payload})
				}
			},
		},
	}

	// Startup reconciliation: repair missing/stale OS schedules.
	if scheduler != nil {
		go func() {
			reconciler := &backgroundjobs.Reconciler{
				Scheduler:  scheduler,
				Store:      store,
				Logger:     logger,
				ConfigHash: backgroundjobs.HashConfigPath(configPath),
			}
			result, err := reconciler.ReconcileSchedules(ctx, false)
			if err != nil {
				logger.Warn("startup reconciliation failed", logging.Any("err", err))
			} else if result.Installed+result.Removed+result.Disabled > 0 {
				logger.Info("startup reconciliation complete",
					logging.Any("installed", result.Installed),
					logging.Any("removed", result.Removed),
					logging.Any("disabled", result.Disabled))
			}
		}()
	}

	srv, srvErr := web.NewServer(web.Options{
		Config:      cfg,
		Logger:      logger,
		Events:      events,
		ShutdownCtx: ctx,
		ConfigPtr:   live,
		OverlayPath: overlayPath,
		EnqueueRun:  enqueueRun,
		RestartHook: restartHook,
		Activity:    activity,
		StateCache:  stateCache,
		Jobs:        jobsDeps,
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
func runDaemonLoop(ctx context.Context, cmd *cobra.Command, queue *jobqueue.Queue, cfg *config.App, frequency, retention time.Duration, logger *logging.Logger, workers *workerPool) {
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
			queue.PruneHistory(retention)
			enqueueMissingJobs(ctx, queue, cfg, logger)
		}
	}
}

// startConfigWatcher spawns a goroutine bound to ctx that watches the parent
// directories of configPath and overlayPath for write events. On a successful
// reload it CAS-swaps the live config pointer and publishes a config.reloaded
// event via the bus. Reload failures leave the prior pointer value active and
// are logged at info level; they never crash the daemon.
func startConfigWatcher(ctx context.Context, logger *logging.Logger, events *pipeline.EventBus, live *atomic.Pointer[config.App], configPath, overlayPath string) {
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
		var debounceTimer *time.Timer
		var debounceCh <-chan time.Time
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
				// Re-add watch if the watched directory's content was
				// renamed or removed (editor temp-and-rename can
				// invalidate the fsnotify watch). Run in a goroutine
				// so the sleep doesn't block reading other events.
				if ev.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
					dir := ev.Name
					if _, isWatched := watched[dir]; isWatched {
						go func(d string) {
							time.Sleep(100 * time.Millisecond)
							_ = watcher.Remove(d)
							if err := watcher.Add(d); err != nil {
								logger.Warn("config watcher re-add failed", logging.Any("dir", d), logging.Any("err", err))
							}
						}(dir)
					}
				}
				if ev.Op&fsnotify.Write == 0 && ev.Op&fsnotify.Create == 0 {
					continue
				}
				clean := filepath.Clean(ev.Name)
				if clean != filepath.Clean(configPath) && clean != filepath.Clean(overlayPath) {
					continue
				}
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.NewTimer(debounce)
				debounceCh = debounceTimer.C
			case <-debounceCh:
				debounceCh = nil
				reload()
			case watchErr, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Info("config watcher error", logging.Any("err", watchErr))
			}
		}
	}()
}

// sweepStaleFindingsTempFiles deletes leftover temp files from prior daemon
// runs that crashed or were killed before the per-run defer ran. Sweeps
// both Phase 2 findings temp files (dreamer-findings-*.jsonl) and MCP
// config temp files (dreamer-mcp-config-*.json).
// staleAge is the minimum age before a temp file is considered abandoned.
// Files younger than this may still be in use by a concurrent
// `dreamer analyze` run (which doesn't hold the daemon lock).
const staleAge = 10 * time.Minute

// Returns the count of files removed. Errors removing individual files
// are swallowed silently — the worst case is a small amount of temp-dir
// clutter on the next sweep.
func sweepStaleFindingsTempFiles() int {
	patterns := []string{
		filepath.Join(os.TempDir(), pipeline.FindingsTempFilePattern),
		filepath.Join(os.TempDir(), mcpserver.MCPTempFilePattern),
	}
	cutoff := time.Now().Add(-staleAge)
	removed := 0
	for _, glob := range patterns {
		matches, err := filepath.Glob(glob)
		if err != nil {
			continue
		}
		for _, p := range matches {
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			if info.ModTime().After(cutoff) {
				continue // still potentially in use
			}
			if err := os.Remove(p); err == nil {
				removed++
			}
		}
	}
	return removed
}
