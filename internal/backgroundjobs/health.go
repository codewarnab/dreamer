package backgroundjobs

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"dreamer/internal/logging"
	"dreamer/internal/sandbox"
)

// HealthIssue is a single diagnostic issue with a severity.
type HealthIssue struct {
	JobID    string         `json:"job_id"`
	Severity HealthSeverity `json:"severity"`
	Message  string         `json:"message"`
}

// HealthSeverity is the severity of a health issue.
type HealthSeverity string

const (
	HealthSeverityError   HealthSeverity = "error"
	HealthSeverityWarning HealthSeverity = "warning"
	HealthSeverityInfo    HealthSeverity = "info"
)

// SystemHealth is the aggregated health result from CheckHealth.
type SystemHealth struct {
	SystemHealthy bool          `json:"system_healthy"`
	TotalJobs     int           `json:"total_jobs"`
	EnabledJobs   int           `json:"enabled_jobs"`
	ScheduledJobs int           `json:"scheduled_jobs"`
	Issues        []HealthIssue `json:"issues"`
	JobHealth     []JobHealth   `json:"jobs"`
}

// JobHealth is the health status of a single job.
type JobHealth struct {
	JobID         string        `json:"job_id"`
	Installed     bool          `json:"installed"`
	Enabled       bool          `json:"enabled"`
	HasOSSchedule bool          `json:"has_os_schedule"` // has active OS schedule artifact
	Healthy       bool          `json:"healthy"`
	Issues        []HealthIssue `json:"issues"`
}

// HealthChecker runs health diagnostics.
type HealthChecker struct {
	Scheduler Scheduler
	Store     *Store
	RunStore  *RunStore // optional; nil disables run-status checks
	Logger    *logging.Logger
}

// CheckHealth runs all health checks and returns aggregated results.
func (h *HealthChecker) CheckHealth(ctx context.Context) (SystemHealth, error) {
	state, err := h.Store.Load()
	if err != nil {
		return SystemHealth{}, fmt.Errorf("load state: %w", err)
	}

	health := SystemHealth{
		TotalJobs: len(state.Jobs),
		Issues:    []HealthIssue{},
		JobHealth: []JobHealth{},
	}

	// System-level check: sandbox availability.
	if !sandbox.Available() {
		health.Issues = append(health.Issues, HealthIssue{
			Severity: HealthSeverityWarning,
			Message: fmt.Sprintf(
				"OS sandbox unavailable (%s/%s); background jobs run fully permissive with no file access restrictions",
				runtime.GOOS, runtime.GOARCH),
		})
	}

	// Check each job.
	for jobID, job := range state.Jobs {
		if job.Enabled {
			health.EnabledJobs++
		}

		jh := h.checkJob(ctx, jobID, job)
		if jh.HasOSSchedule {
			health.ScheduledJobs++
		}
		if !jh.Healthy {
			health.Issues = append(health.Issues, jh.Issues...)
		}
		health.JobHealth = append(health.JobHealth, jh)
	}

	// Check for orphaned schedules.
	orphaned, err := h.checkOrphans(ctx, state)
	if err != nil {
		h.Logger.Warn("check orphans", logging.Any("error", err))
	} else {
		health.Issues = append(health.Issues, orphaned...)
	}

	health.SystemHealthy = len(health.Issues) == 0
	return health, nil
}

// checkJob runs health checks for a single job.
func (h *HealthChecker) checkJob(ctx context.Context, jobID string, job *Job) JobHealth {
	jh := JobHealth{
		JobID:   jobID,
		Enabled: job.Enabled,
	}

	// Check OS schedule.
	if job.OSSchedule.ScheduleID != "" {
		jh.HasOSSchedule = true
		scheduleHealth, err := h.Scheduler.Inspect(ctx, jobID)
		if err != nil {
			jh.Issues = append(jh.Issues, HealthIssue{
				JobID:    jobID,
				Severity: HealthSeverityWarning,
				Message:  fmt.Sprintf("inspect schedule: %v", err),
			})
		} else {
			jh.Installed = scheduleHealth.Installed
			if !scheduleHealth.Installed {
				jh.Issues = append(jh.Issues, HealthIssue{
					JobID:    jobID,
					Severity: HealthSeverityError,
					Message:  "OS schedule missing despite metadata existing",
				})
			}
		}
	} else if job.Enabled {
		// Enabled job with no OS schedule.
		jh.Issues = append(jh.Issues, HealthIssue{
			JobID:    jobID,
			Severity: HealthSeverityWarning,
			Message:  "enabled job has no OS schedule — run 'dreamer jobs reconcile'",
		})
	}

	// Check for stale last run.
	if job.LastRunAt != nil {
		staleDuration := time.Since(*job.LastRunAt)
		if staleDuration > 7*24*time.Hour {
			jh.Issues = append(jh.Issues, HealthIssue{
				JobID:    jobID,
				Severity: HealthSeverityInfo,
				Message:  fmt.Sprintf("last run was %s ago", staleDuration.Truncate(time.Hour)),
			})
		}
	}

	// Check for overdue NextRunAt — daemon may be down or schedule needs reconcile.
	const nextRunGrace = 10 * time.Minute
	if job.Enabled && job.NextRunAt != nil && job.NextRunAt.Before(time.Now().UTC().Add(-nextRunGrace)) {
		overdue := time.Since(*job.NextRunAt).Truncate(time.Minute)
		jh.Issues = append(jh.Issues, HealthIssue{
			JobID:    jobID,
			Severity: HealthSeverityWarning,
			Message:  fmt.Sprintf("next run is overdue by %s — daemon may be down or schedule needs reconcile", overdue),
		})
	}

	// Check for repeated timeouts (distinct from failures).
	if h.RunStore != nil {
		if latest, err := h.RunStore.Latest(jobID); err == nil && latest != nil {
			if latest.Status == RunStatusTimedOut {
				jh.Issues = append(jh.Issues, HealthIssue{
					JobID:    jobID,
					Severity: HealthSeverityWarning,
					Message:  "last run timed out — consider increasing the job timeout",
				})
			}
		}
	}

	jh.Healthy = len(jh.Issues) == 0

	// Collect issues into system-level list.
	// (Already done by the caller via jh.Issues.)

	return jh
}

// checkOrphans checks for OS schedules that don't have matching jobs in the store.
func (h *HealthChecker) checkOrphans(ctx context.Context, state *State) ([]HealthIssue, error) {
	ownIDs, err := h.Scheduler.ListOwn(ctx)
	if err != nil {
		return nil, err
	}

	var issues []HealthIssue
	for _, osID := range ownIDs {
		if _, exists := state.Jobs[osID]; !exists {
			issues = append(issues, HealthIssue{
				JobID:    osID,
				Severity: HealthSeverityWarning,
				Message:  "orphaned OS schedule — run 'dreamer jobs reconcile' to clean up",
			})
		}
	}
	return issues, nil
}
