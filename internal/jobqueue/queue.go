package jobqueue

import (
	"sync"
	"time"

	"dreamer/internal/logging"
)

// EnqueueConfig holds the per-project parameters captured at enqueue time.
type EnqueueConfig struct {
	ProjectPath string
	Provider    string
	Since       string
}

// Options configures the Queue at construction time.
type Options struct {
	StorePath     string
	MaxConcurrent int
	MaxDuration   time.Duration
	Logger        *logging.Logger
}

// QueueStatus is a snapshot of the queue for status display.
type QueueStatus struct {
	Jobs      []*Job
	Running   int
	Pending   int
	Completed int
	Failed    int
	TimedOut  int
	Cancelled int
}

// Queue manages the lifecycle of analysis jobs. It enforces one active
// (pending or running) job per project and coordinates with a worker pool.
type Queue struct {
	mu            sync.Mutex
	jobs          []*Job
	store         *Store
	logger        *logging.Logger
	maxConcurrent int
	maxDuration   time.Duration
	// wake is buffered (cap=1) and signalled on every Enqueue so idle workers
	// can react without polling. Workers select on this channel; non-blocking
	// send avoids back-pressure when no worker is waiting.
	wake chan struct{}
}

// New creates a Queue backed by the given store. Call Recover after New
// to restore state from a prior run.
func New(opts Options) *Queue {
	return &Queue{
		store:         NewStore(opts.StorePath),
		logger:        opts.Logger,
		maxConcurrent: opts.MaxConcurrent,
		maxDuration:   opts.MaxDuration,
		wake:          make(chan struct{}, 1),
	}
}

// MaxConcurrent returns the configured concurrency limit.
func (q *Queue) MaxConcurrent() int {
	return q.maxConcurrent
}


// Wake returns the channel signalled on Enqueue. Workers can select on it
// to pre-empt their idle wait without polling on a long ticker.
func (q *Queue) Wake() <-chan struct{} {
	return q.wake
}

// Recover loads persisted jobs and handles stale running jobs. Pending jobs
// are preserved for the worker pool. Call once after New, before any
// enqueue/dequeue operations.
func (q *Queue) Recover() error {
	jobs, err := q.store.Load()
	if err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = jobs
	return nil
}

// HasActiveJob reports whether a pending or running job exists for the
// given project name. Used for dedup before enqueue.
func (q *Queue) HasActiveJob(project string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		if j.Project == project && !j.Status.IsTerminal() {
			return true
		}
	}
	return false
}

// Enqueue adds a job for the given project. If a pending or running job
// already exists for that project, the call is a no-op and returns nil.
// The queue is persisted after enqueue.
func (q *Queue) Enqueue(project string, cfg EnqueueConfig) *Job {
	q.mu.Lock()
	for _, j := range q.jobs {
		if j.Project == project && !j.Status.IsTerminal() {
			q.mu.Unlock()
			return nil // dedup
		}
	}

	job := newJob(project, cfg.ProjectPath, cfg.Provider, cfg.Since)
	q.jobs = append(q.jobs, job)
	q.persistLocked()
	q.mu.Unlock()

	// Signal idle workers without blocking. Buffered cap=1: if a wake is
	// already pending, we coalesce — a single worker will drain it and try
	// Dequeue, which in turn handles whichever pending job is at the head.
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return job
}

// Dequeue returns the next pending job if the concurrency limit has not been
// reached. Returns nil when no jobs are available or all worker slots are
// occupied. Caller must call Complete/Failed/TimedOut/Cancel when done.
func (q *Queue) Dequeue() *Job {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.runningCountLocked() >= q.maxConcurrent {
		return nil
	}
	for _, j := range q.jobs {
		if j.Status == StatusPending {
			j.markRunning()
			q.persistLocked()
			return j
		}
	}
	return nil
}

// RunningCount returns the number of currently running jobs.
func (q *Queue) RunningCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.runningCountLocked()
}

// Complete marks a job as finished successfully with pipeline metrics. The
// transition is a no-op if the job already has a terminal status — this
// prevents a CancelRunning-style admin write from racing with the worker's
// own terminal write on the same job.
func (q *Queue) Complete(job *Job, findings, messages, sources int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job.Status.IsTerminal() {
		return
	}
	job.markFinished(StatusCompleted, "", findings, messages, sources)
	q.persistLocked()
}

// Failed marks a job as failed with an error message. No-op if already terminal.
func (q *Queue) Failed(job *Job, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job.Status.IsTerminal() {
		return
	}
	job.markFinished(StatusFailed, err.Error(), 0, 0, 0)
	q.persistLocked()
}

// TimedOut marks a job as timed out. No-op if already terminal.
func (q *Queue) TimedOut(job *Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job.Status.IsTerminal() {
		return
	}
	job.markFinished(StatusTimedOut, "max analysis duration exceeded", 0, 0, 0)
	q.persistLocked()
}

// Cancel marks a job as cancelled (daemon shutdown). No-op if already terminal.
func (q *Queue) Cancel(job *Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job.Status.IsTerminal() {
		return
	}
	job.markFinished(StatusCancelled, "daemon shutdown", 0, 0, 0)
	q.persistLocked()
}

// Status returns a snapshot of all jobs grouped by status. Jobs are
// shallow-copied (value fields are independent; pointer fields like
// StartedAt/FinishedAt share targets, but workers reassign rather than
// mutate them, so this is safe for concurrent reads).
func (q *Queue) Status() QueueStatus {
	q.mu.Lock()
	defer q.mu.Unlock()

	s := QueueStatus{
		Jobs: make([]*Job, 0, len(q.jobs)),
	}
	for _, j := range q.jobs {
		cp := *j
		s.Jobs = append(s.Jobs, &cp)
		switch j.Status {
		case StatusRunning:
			s.Running++
		case StatusPending:
			s.Pending++
		case StatusCompleted:
			s.Completed++
		case StatusFailed:
			s.Failed++
		case StatusTimedOut:
			s.TimedOut++
		case StatusCancelled:
			s.Cancelled++
		}
	}
	return s
}

// PruneHistory removes terminal jobs older than maxAge.
func (q *Queue) PruneHistory(maxAge time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := time.Now().UTC().Add(-maxAge)
	filtered := q.jobs[:0]
	for _, j := range q.jobs {
		if j.Status.IsTerminal() && j.FinishedAt != nil && j.FinishedAt.Before(cutoff) {
			continue
		}
		filtered = append(filtered, j)
	}
	// Nil out dangling slots to release *Job pointers for GC.
	for i := len(filtered); i < len(q.jobs); i++ {
		q.jobs[i] = nil
	}
	q.jobs = filtered
	q.persistLocked()
}

// CancelRunning marks all running jobs as cancelled. Retained for callers
// that want a synchronous "everyone stop" semantic (e.g. tests). The
// recommended shutdown path is to cancel the worker context and let workers
// transition their own jobs via Cancel(job); the IsTerminal guard prevents
// double writes.
func (q *Queue) CancelRunning() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		if j.Status == StatusRunning {
			j.markFinished(StatusCancelled, "daemon shutdown", 0, 0, 0)
		}
	}
	q.persistLocked()
}

// ReapRunning marks every running job as failed with the given reason. Used
// on daemon startup to clean up zombie rows left by a SIGKILL or crash before
// the worker could write its own terminal status. Safe to call unconditionally
// — we hold the daemon lock, so by definition no other writer exists.
func (q *Queue) ReapRunning(reason string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, j := range q.jobs {
		if j.Status == StatusRunning {
			j.markFinished(StatusFailed, reason, 0, 0, 0)
			n++
		}
	}
	if n > 0 {
		q.persistLocked()
	}
	return n
}

// runningCountLocked returns the count of running jobs. Caller must hold q.mu.
func (q *Queue) runningCountLocked() int {
	count := 0
	for _, j := range q.jobs {
		if j.Status == StatusRunning {
			count++
		}
	}
	return count
}

// persistLocked writes the current state to disk. Caller must hold q.mu.
// Errors are surfaced via the injected logger; the queue stays functional
// even if persistence fails so in-memory state remains authoritative.
func (q *Queue) persistLocked() {
	if err := q.store.Save(q.jobs); err != nil && q.logger != nil {
		q.logger.Warn("persist job queue failed", logging.Any("path", q.store.Path()), logging.Any("err", err))
	}
}
