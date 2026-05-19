package jobqueue

import (
	"sync"
	"time"
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
	maxConcurrent int
	maxDuration   time.Duration
}

// New creates a Queue backed by the given store. Call Recover after New
// to restore state from a prior run.
func New(opts Options) *Queue {
	return &Queue{
		store:         NewStore(opts.StorePath),
		maxConcurrent: opts.MaxConcurrent,
		maxDuration:   opts.MaxDuration,
	}
}

// MaxConcurrent returns the configured concurrency limit.
func (q *Queue) MaxConcurrent() int {
	return q.maxConcurrent
}

// MaxDuration returns the configured per-job timeout.
func (q *Queue) MaxDuration() time.Duration {
	return q.maxDuration
}

// StorePath returns the path to jobs.json.
func (q *Queue) StorePath() string {
	return q.store.Path()
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
	defer q.mu.Unlock()

	for _, j := range q.jobs {
		if j.Project == project && !j.Status.IsTerminal() {
			return nil // dedup
		}
	}

	job := newJob(project, cfg.ProjectPath, cfg.Provider, cfg.Since)
	q.jobs = append(q.jobs, job)
	q.persistLocked()
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

// Complete marks a job as finished successfully with pipeline metrics.
func (q *Queue) Complete(job *Job, findings, messages, sources int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job.markFinished(StatusCompleted, "", findings, messages, sources)
	q.persistLocked()
}

// Failed marks a job as failed with an error message.
func (q *Queue) Failed(job *Job, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job.markFinished(StatusFailed, err.Error(), 0, 0, 0)
	q.persistLocked()
}

// TimedOut marks a job as timed out.
func (q *Queue) TimedOut(job *Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job.markFinished(StatusTimedOut, "max analysis duration exceeded", 0, 0, 0)
	q.persistLocked()
}

// Cancel marks a job as cancelled (daemon shutdown).
func (q *Queue) Cancel(job *Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job.markFinished(StatusCancelled, "daemon shutdown", 0, 0, 0)
	q.persistLocked()
}

// Status returns a snapshot of all jobs grouped by status.
func (q *Queue) Status() QueueStatus {
	q.mu.Lock()
	defer q.mu.Unlock()

	s := QueueStatus{
		Jobs: make([]*Job, len(q.jobs)),
	}
	copy(s.Jobs, q.jobs)
	for _, j := range q.jobs {
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
	// Zero remaining slots to avoid memory leaks from the backing array.
	for i := len(filtered); i < len(q.jobs); i++ {
		q.jobs[i] = nil
	}
	q.jobs = filtered
	q.persistLocked()
}

// CancelRunning marks all running jobs as cancelled. Called during graceful
// shutdown.
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
// Errors are silently ignored — persistence is best-effort; the queue
// remains functional even if the disk write fails.
func (q *Queue) persistLocked() {
	_ = q.store.Save(q.jobs)
}
