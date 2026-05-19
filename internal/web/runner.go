package web

import (
	"context"
	"fmt"
	"sync"
	"time"

	"dreamer/internal/logging"
)

// Runner serializes per-project on-demand pipeline runs. One worker
// per project (max one concurrent run per project). Cross-project
// runs proceed in parallel.
type Runner struct {
	mu       sync.Mutex
	inflight map[string]string // projectName -> runID
	invoke   RunFunc
	logger   *logging.Logger
}

// RunFunc is the function the daemon supplies to actually execute a
// run. It receives the project name and a context bound to the
// daemon's lifecycle.
type RunFunc func(ctx context.Context, projectName string) error

// NewRunner constructs a Runner. The daemon wires `invoke` to a
// closure that builds pipeline.Options for the named project and
// calls pipeline.Run.
func NewRunner(invoke RunFunc, logger *logging.Logger) *Runner {
	return &Runner{
		inflight: map[string]string{},
		invoke:   invoke,
		logger:   logger,
	}
}

// Enqueue starts a run for the named project if none is in flight.
// Returns (runID, true, nil) on enqueue; ("", false, nil) if a run is
// already in flight for that project. The runID is a nanosecond
// timestamp string scoped to this daemon process.
func (r *Runner) Enqueue(projectName string) (string, bool, error) {
	r.mu.Lock()
	if _, exists := r.inflight[projectName]; exists {
		r.mu.Unlock()
		return "", false, nil
	}
	runID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	r.inflight[projectName] = runID
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.inflight, projectName)
			r.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := r.invoke(ctx, projectName); err != nil && r.logger != nil {
			r.logger.Error("on-demand run failed", logging.String("project", projectName), logging.Any("err", err))
		}
	}()
	return runID, true, nil
}

// EnqueueFunc returns a closure suitable for handlers.Deps.EnqueueRun.
func (r *Runner) EnqueueFunc() func(string) (string, bool, error) {
	return r.Enqueue
}
