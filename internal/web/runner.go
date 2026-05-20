package web

import (
	"dreamer/internal/logging"
)

// Runner delegates on-demand pipeline runs to the job queue. It is a
// thin adapter — deduplication is handled by the queue's own
// Enqueue (one active job per project). The Runner exists so the web
// server's dependency injection layer has a clean interface.
type Runner struct {
	invoke RunFunc
	logger *logging.Logger
}

// RunFunc enqueues a job for the named project. Returns (jobID, true)
// on success; ("", false) when a job is already active for the
// project (queue-level dedup).
type RunFunc func(projectName string) (jobID string, accepted bool)

// NewRunner constructs a Runner. The daemon wires invoke to a closure
// that resolves project config and calls queue.Enqueue.
func NewRunner(invoke RunFunc, logger *logging.Logger) *Runner {
	return &Runner{
		invoke: invoke,
		logger: logger,
	}
}

// Enqueue starts a run for the named project if none is active.
// Returns (runID, true, nil) on enqueue; ("", false, nil) if a run
// is already active for that project.
func (r *Runner) Enqueue(projectName string) (string, bool, error) {
	jobID, accepted := r.invoke(projectName)
	return jobID, accepted, nil
}

// EnqueueFunc returns a closure suitable for handlers.Deps.EnqueueRun.
func (r *Runner) EnqueueFunc() func(string) (string, bool, error) {
	return r.Enqueue
}
