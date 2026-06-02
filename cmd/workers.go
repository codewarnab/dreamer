package cmd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/jobqueue"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"dreamer/internal/state"
)

// workerIdleInterval is the worst-case latency between Enqueue and a worker
// noticing a new job in the absence of the wake signal. Kept short so a
// missed wake (e.g. coalesced with a concurrent signal already drained by
// another worker) still recovers quickly.
const workerIdleInterval = 1 * time.Second

// workerPool manages a set of goroutines that dequeue and run analysis jobs.
type workerPool struct {
	ctx        context.Context
	queue      *jobqueue.Queue
	cfg        *config.App
	live       *atomic.Pointer[config.App]
	logger     *logging.Logger
	cache      *pipeline.DiscoveryCache
	stateCache *state.StateCache
	events     *pipeline.EventBus
	overrides  daemonOverrides
	wg         sync.WaitGroup
}

// workerPoolConfig holds the configuration for creating a new workerPool.
type workerPoolConfig struct {
	queue      *jobqueue.Queue
	cfg        *config.App
	live       *atomic.Pointer[config.App]
	logger     *logging.Logger
	cache      *pipeline.DiscoveryCache
	stateCache *state.StateCache
	events     *pipeline.EventBus
	overrides  daemonOverrides
}

// newWorkerPool creates a pool that will spawn MaxConcurrent workers.
func newWorkerPool(ctx context.Context, wpc workerPoolConfig) *workerPool {
	return &workerPool{
		ctx:        ctx,
		queue:      wpc.queue,
		cfg:        wpc.cfg,
		live:       wpc.live,
		logger:     wpc.logger,
		cache:      wpc.cache,
		stateCache: wpc.stateCache,
		events:     wpc.events,
		overrides:  wpc.overrides,
	}
}

// Start launches worker goroutines. Each worker polls the queue for pending
// jobs and runs them. Call Stop to wait for all workers to finish.
func (wp *workerPool) Start() {
	for i := 0; i < wp.queue.MaxConcurrent(); i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
}

// Stop waits for workers to drain their current job, but no longer than
// workerShutdownGrace. The caller cancels the run context before calling
// Stop, so workers are already winding down; the grace period is an upper
// bound that prevents a stuck provider subprocess from blocking shutdown for
// the full max_analysis_duration. On timeout we return and let process exit
// reap any abandoned worker goroutines.
func (wp *workerPool) Stop() {
	wp.stop(workerShutdownGrace)
}

// stop is the grace-parameterized core of Stop, split out so tests can drive
// both the clean-drain and timeout branches without waiting the full
// production grace period.
func (wp *workerPool) stop(grace time.Duration) {
	done := make(chan struct{})
	go func() {
		// NOTE: If the grace period expires, this spawned goroutine waiting on wg.Wait()
		// will remain blocked. In production, this is harmless as it only occurs immediately
		// before daemon process exit, at which point the OS reclaims all resources.
		// Furthermore, stuck worker subprocesses are guaranteed to be terminated via SIGKILL
		// because pipeline.Run executes commands using the canceled command context.
		wp.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(grace):
		wp.logger.Warn("worker shutdown grace exceeded; abandoning in-flight jobs",
			logging.Any("grace", grace))
	}
}

// worker polls the queue until the context is cancelled. The ctx check sits
// at the top of every iteration so a shutdown is honoured even when the
// queue is non-empty — otherwise a backlog would block daemon stop for the
// full max_analysis_duration of each remaining job.
func (wp *workerPool) worker(id int) {
	defer wp.wg.Done()
	for {
		if wp.ctx.Err() != nil {
			return
		}
		job := wp.queue.Dequeue()
		if job == nil {
			timer := time.NewTimer(workerIdleInterval)
			select {
			case <-wp.ctx.Done():
				timer.Stop()
				return
			case <-wp.queue.Wake():
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
		wp.runJob(id, job)
	}
}

// runJob executes the analysis pipeline for a single job.
func (wp *workerPool) runJob(workerID int, job *jobqueue.Job) {
	wp.logger.Info("job started",
		logging.Any("job", job.ID),
		logging.Any("project", job.Project),
		logging.Any("worker", workerID),
	)

	maxDur, durErr := wp.cfg.ResolveMaxDuration(job.Project)
	if durErr != nil {
		// Should never happen — config validation rejects bad durations at
		// load time. Log and fail the job rather than silently fall back so
		// a regression in validation is visible.
		wp.queue.Failed(job, durErr)
		pipeline.PublishRunError(wp.events, job.Project, durErr)
		wp.logger.Error("resolve max duration failed", append([]logging.Attr{logging.Any("job", job.ID)}, logging.ErrAttr(durErr)...)...)
		return
	}
	jobCtx, cancel := context.WithTimeout(wp.ctx, maxDur)
	defer cancel()

	opts := pipeline.Options{
		Config:                 wp.cfg,
		LiveConfig:             wp.live,
		ProjectPath:            job.ProjectPath,
		ProjectName:            job.Project,
		ProviderID:             job.Provider,
		Force:                  false,
		Since:                  job.Since,
		DiscoveryCache:         wp.cache,
		StateCache:             wp.stateCache,
		Events:                 wp.events,
		ParallelOverride:       wp.overrides.forceParallel,
		MaxConcurrencyOverride: wp.overrides.maxConcurrency,
	}
	if wp.overrides.maxChunkBytesSet {
		opts.MaxChunkBytesOverride = wp.overrides.maxChunkBytes
		opts.MaxChunkBytesOverrideSet = true
	}

	result, err := pipeline.Run(jobCtx, opts, wp.logger)

	if errors.Is(jobCtx.Err(), context.DeadlineExceeded) {
		wp.queue.TimedOut(job)
		pipeline.PublishRunError(wp.events, job.Project, fmt.Errorf("max analysis duration exceeded (%s)", maxDur))
		wp.logger.Warn("job timed out", logging.Any("job", job.ID), logging.Any("duration", maxDur))
	} else if errors.Is(jobCtx.Err(), context.Canceled) {
		wp.queue.Cancel(job)
		pipeline.PublishRunError(wp.events, job.Project, fmt.Errorf("job cancelled: daemon shutdown"))
		wp.logger.Info("job cancelled", logging.Any("job", job.ID))
	} else if err != nil {
		wp.queue.Failed(job, err)
		pipeline.PublishRunError(wp.events, job.Project, err)
		wp.logger.Error("job failed", append([]logging.Attr{logging.Any("job", job.ID)}, logging.ErrAttr(err)...)...)
	} else {
		wp.queue.Complete(job, result.Findings, result.MessagesRead, result.SourcesAnalyzed)
		wp.logger.Info("job completed",
			logging.Any("job", job.ID),
			logging.Any("findings", result.Findings),
			logging.Any("sources", result.SourcesAnalyzed),
		)
	}
}

// recoverStaleJobs cleans up running jobs left over from a prior daemon run.
// We hold the daemon lock by the time this is called, so by definition no
// other process is mutating jobs.json. Any row still in Running state is a
// zombie — either the old process died before writing its terminal status
// or the lockfile was lost. Reap unconditionally rather than gating on
// stalePID, which can be 0 when the lockfile didn't exist.
func recoverStaleJobs(queue *jobqueue.Queue, stalePID int, staleExec string, logger *logging.Logger) {
	// If we can prove the old process is still alive and is our executable,
	// leave its jobs alone — AcquireLock would normally have failed in that
	// case, but check defensively. Compare against the live process image,
	// not our own binary path.
	if stalePID != 0 && fsutil.IsProcessAlive(stalePID) {
		// Verify the PID still belongs to our executable to guard against
		// PID reuse after a crash.
		if staleExec != "" {
			if liveExec, ok := fsutil.ProcessExecutable(stalePID); ok && !fsutil.ExecPathsMatch(staleExec, liveExec) {
				// PID reused — fall through to reap.
				goto reap
			}
		}
		return
	}
reap:
	n := queue.ReapRunning("daemon restarted during analysis")
	if n > 0 {
		logger.Warn("reaped stale running jobs", logging.Any("count", n))
	}
}
