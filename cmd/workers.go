package cmd

import (
	"context"
	"errors"
	"sync"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/jobqueue"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
)

// workerPool manages a set of goroutines that dequeue and run analysis jobs.
type workerPool struct {
	ctx       context.Context
	queue     *jobqueue.Queue
	cfg       *config.Config
	logger    *logging.Logger
	cache     *pipeline.DiscoveryCache
	overrides daemonOverrides
	wg        sync.WaitGroup
}

// newWorkerPool creates a pool that will spawn MaxConcurrent workers.
func newWorkerPool(ctx context.Context, queue *jobqueue.Queue, cfg *config.Config, logger *logging.Logger, cache *pipeline.DiscoveryCache, overrides daemonOverrides) *workerPool {
	return &workerPool{
		ctx:       ctx,
		queue:     queue,
		cfg:       cfg,
		logger:    logger,
		cache:     cache,
		overrides: overrides,
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

// Stop signals workers to exit and waits for them to finish their current job.
func (wp *workerPool) Stop() {
	wp.wg.Wait()
}

// worker polls the queue until the context is cancelled.
func (wp *workerPool) worker(id int) {
	defer wp.wg.Done()
	for {
		job := wp.queue.Dequeue()
		if job == nil {
			select {
			case <-wp.ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
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

	maxDur := resolveMaxDuration(job, wp.cfg)
	jobCtx, cancel := context.WithTimeout(wp.ctx, maxDur)
	defer cancel()

	opts := pipeline.Options{
		Config:           wp.cfg,
		ProjectPath:      job.ProjectPath,
		ProjectName:      job.Project,
		ProviderID:       job.Provider,
		Force:            false,
		Since:            job.Since,
		DiscoveryCache:   wp.cache,
		ParallelOverride: wp.overrides.parallel,
		MaxConcurrencyOverride: wp.overrides.maxConcurrency,
	}
	if wp.overrides.maxChunkBytesSet {
		opts.MaxChunkBytesOverride = wp.overrides.maxChunkBytes
		opts.MaxChunkBytesOverrideSet = true
	}

	result, err := pipeline.Run(jobCtx, opts, wp.logger)

	if errors.Is(jobCtx.Err(), context.DeadlineExceeded) {
		wp.queue.TimedOut(job)
		wp.logger.Warn("job timed out", logging.Any("job", job.ID), logging.Any("duration", maxDur))
	} else if errors.Is(jobCtx.Err(), context.Canceled) {
		wp.queue.Cancel(job)
		wp.logger.Info("job cancelled", logging.Any("job", job.ID))
	} else if err != nil {
		wp.queue.Failed(job, err)
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

// resolveMaxDuration returns the per-project or global max analysis duration.
func resolveMaxDuration(job *jobqueue.Job, cfg *config.Config) time.Duration {
	dur, err := cfg.ResolveMaxDuration(job.Project)
	if err != nil {
		return 8 * time.Hour
	}
	return dur
}

// recoverStaleJobs marks running jobs from a prior daemon run as failed
// if the old process (identified by stalePID) is no longer alive.
// Pass stalePID=0 when the lock file didn't exist or was unreadable.
func recoverStaleJobs(queue *jobqueue.Queue, stalePID int, logger *logging.Logger) {
	if stalePID == 0 {
		return
	}
	if fsutil.IsProcessAlive(stalePID) {
		return
	}
	status := queue.Status()
	for _, j := range status.Jobs {
		if j.Status == jobqueue.StatusRunning {
			queue.Failed(j, errors.New("daemon restarted during analysis"))
			logger.Warn("marked stale job as failed", logging.Any("job", j.ID), logging.Any("project", j.Project))
		}
	}
}
