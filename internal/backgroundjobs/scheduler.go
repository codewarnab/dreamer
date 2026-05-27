package backgroundjobs

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"dreamer/internal/logging"
)

// Scheduler manages per-job OS schedules.
type Scheduler interface {
	// Install creates a new OS schedule for the job.
	// If a schedule already exists for this job, replaces it idempotently.
	Install(ctx context.Context, params ScheduleParams) (OSScheduleState, error)

	// Update modifies an existing OS schedule for the job.
	Update(ctx context.Context, params ScheduleParams) (OSScheduleState, error)

	// Remove deletes the OS schedule for the job.
	Remove(ctx context.Context, jobID string) error

	// Inspect returns the current OS-level health for a job's schedule.
	Inspect(ctx context.Context, jobID string) (ScheduleHealth, error)

	// ListOwn returns job IDs of schedules owned by this Dreamer installation.
	// Used by reconciliation to find orphaned schedules.
	ListOwn(ctx context.Context) ([]string, error)
}

// ScheduleParams bundles caller-specific fields for Install/Update.
// Derivable metadata (InstallID, hashes) is computed by the scheduler
// from SchedulerConfig injected at construction time.
type ScheduleParams struct {
	JobID    string
	Schedule ScheduleSpec
	Name     string // human-readable, sanitized before use in OS artifacts
	Enabled  bool
}

// SchedulerConfig holds stable configuration shared across all schedule operations.
// Injected once at construction via NewScheduler.
type SchedulerConfig struct {
	StoreDir       string
	ExecutablePath string
	ConfigPath     string
	InstallID      string
	ConfigHash     string
	ExecHash       string
}

// NewScheduler returns a platform-specific Scheduler.
// Uses runtime.GOOS to select: windows, linux, darwin, or noop.
func NewScheduler(cfg SchedulerConfig, logger *logging.Logger) Scheduler {
	return newPlatformScheduler(cfg, logger)
}

// ScheduleHealth is the OS-level health for a single job schedule.
type ScheduleHealth struct {
	Installed   bool       `json:"installed"`
	Enabled     bool       `json:"enabled"`
	NextRunTime *time.Time `json:"next_run_time,omitempty"` // OS-reported next run
	LastRunTime *time.Time `json:"last_run_time,omitempty"` // OS-reported last run
	Detail      string     `json:"detail,omitempty"`        // human-readable diagnostic
}

// runExternalCommand executes an external command and returns its combined output.
// Shared by all platform scheduler implementations.
func runExternalCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}
