package backgroundjobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dreamer/internal/logging"
)

// ReconcileAction describes a single action the reconciler needs to take.
type ReconcileAction struct {
	JobID  string
	Reason string
	Kind   ReconcileActionKind
}

// ReconcileActionKind identifies the type of reconciliation action.
type ReconcileActionKind int

const (
	ReconcileInstall      ReconcileActionKind = iota // install or reinstall schedule
	ReconcileRemove                                   // remove orphaned OS schedule
	ReconcileRemoveDisabled                           // remove OS schedule for a disabled job
)

// ReconcileResult captures the outcome of a ReconcileSchedules call.
type ReconcileResult struct {
	Installed int
	Removed   int
	Disabled  int
	Errors    []ReconcileError
}

// ReconcileError is a per-job error during reconciliation.
type ReconcileError struct {
	JobID string
	Err   error
}

// Reconciler performs schedule reconciliation.
type Reconciler struct {
	Scheduler  Scheduler
	Store      *Store
	Logger     *logging.Logger
	ConfigHash string // current config file hash; empty disables config-drift detection
}

// ReconcileSchedules computes the set of actions required to bring the OS
// scheduler in sync with the in-memory job store, then applies them.
func (r *Reconciler) ReconcileSchedules(ctx context.Context, dryRun bool) (ReconcileResult, error) {
	state, err := r.Store.Load()
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("load state: %w", err)
	}

	actions, err := r.computeReconcileActions(ctx, state)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("compute actions: %w", err)
	}

	if dryRun {
		return r.dryRunResult(actions), nil
	}

	return r.applyReconcileActions(ctx, actions, state)
}

// computeReconcileActions is a pure function that determines what actions
// the reconciler needs to take. Separated from I/O for testability.
func (r *Reconciler) computeReconcileActions(ctx context.Context, state *State) ([]ReconcileAction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var actions []ReconcileAction

	// Scan all jobs in the store.
	for jobID, job := range state.Jobs {
		if job.Enabled {
			needsInstall, reason := r.checkInstallNeeded(job)
			if needsInstall {
				actions = append(actions, ReconcileAction{
					JobID:  jobID,
					Reason: reason,
					Kind:   ReconcileInstall,
				})
			}
			// In-sync enabled jobs require no action.
		} else {
			// Disabled job — remove OS schedule if one exists.
			if job.OSSchedule.ScheduleID != "" {
				actions = append(actions, ReconcileAction{
					JobID:  jobID,
					Reason: "job disabled",
					Kind:   ReconcileRemoveDisabled,
				})
			}
		}
	}

	// Check for orphaned OS schedules (exist in OS but not in store).
	ownIDs, err := r.Scheduler.ListOwn(ctx)
	if err != nil {
		// Non-fatal: log and continue.
		r.Logger.Warn("list own schedules", logging.Any("error", err))
		return actions, nil
	}

	storeIDs := make(map[string]bool)
	for jobID := range state.Jobs {
		storeIDs[jobID] = true
	}

	for _, osID := range ownIDs {
		if !storeIDs[osID] {
			actions = append(actions, ReconcileAction{
				JobID:  osID,
				Reason: "orphaned OS schedule",
				Kind:   ReconcileRemove,
			})
		}
	}

	return actions, nil
}

// checkInstallNeeded determines if a job's OS schedule needs to be installed
// or reinstalled. Returns true and the reason if installation is needed.
func (r *Reconciler) checkInstallNeeded(job *Job) (bool, string) {
	if job.OSSchedule.ScheduleID == "" {
		return true, "no OS schedule exists"
	}

	if job.OSSchedule.InstallID == "" {
		return true, "missing install ID"
	}

	// Config path hash mismatch — config file moved or changed.
	if r.ConfigHash != "" && job.OSSchedule.ConfigPathHash != "" {
		if job.OSSchedule.ConfigPathHash != r.ConfigHash {
			return true, "config path hash changed"
		}
	}

	// Spec hash mismatch — schedule changed.
	if job.OSSchedule.SpecHash != "" {
		currentHash, err := HashScheduleSpec(job.Schedule)
		if err == nil && currentHash != job.OSSchedule.SpecHash {
			return true, "schedule spec changed"
		}
	}

	return false, ""
}

// dryRunResult computes a ReconcileResult from actions without applying them.
func (r *Reconciler) dryRunResult(actions []ReconcileAction) ReconcileResult {
	var result ReconcileResult
	for _, action := range actions {
		switch action.Kind {
		case ReconcileInstall:
			result.Installed++
		case ReconcileRemove:
			result.Removed++
		case ReconcileRemoveDisabled:
			result.Disabled++
		}
	}
	return result
}

// applyReconcileActions executes the computed reconciliation actions.
func (r *Reconciler) applyReconcileActions(ctx context.Context, actions []ReconcileAction, state *State) (ReconcileResult, error) {
	var result ReconcileResult

	for _, action := range actions {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		var err error
		switch action.Kind {
		case ReconcileInstall:
			err = r.applyInstall(ctx, action, state)
			if err == nil {
				result.Installed++
			}
		case ReconcileRemove:
			err = r.Scheduler.Remove(ctx, action.JobID)
			if err == nil {
				result.Removed++
			}
		case ReconcileRemoveDisabled:
			err = r.Scheduler.Remove(ctx, action.JobID)
			if err == nil {
				result.Disabled++
			}
		}

		if err != nil {
			result.Errors = append(result.Errors, ReconcileError{
				JobID: action.JobID,
				Err:   err,
			})
		}
	}

	return result, nil
}

// applyInstall installs or reinstalls a schedule for a job.
func (r *Reconciler) applyInstall(ctx context.Context, action ReconcileAction, state *State) error {
	job, ok := state.Jobs[action.JobID]
	if !ok {
		return fmt.Errorf("job %q not found in state", action.JobID)
	}

	params := ScheduleParams{
		JobID:    job.ID,
		Schedule: job.Schedule,
		Name:     SanitizeScheduleName(job.Name),
		Enabled:  job.Enabled,
	}

	osState, err := r.Scheduler.Install(ctx, params)
	if err != nil {
		return fmt.Errorf("install schedule: %w", err)
	}

	// Update the job's OS schedule state.
	return r.Store.Update(ctx, func(s *State) error {
		j, ok := s.Jobs[action.JobID]
		if !ok {
			return fmt.Errorf("job %q disappeared", action.JobID)
		}
		j.OSSchedule = osState
		j.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// SelfRepair performs a bounded self-repair of the current job's schedule
// before execution. Only repairs the single job — no broad reconciliation.
// Non-blocking: errors are logged but don't prevent execution.
func (r *Reconciler) SelfRepair(ctx context.Context, jobID string) error {
	state, err := r.Store.Load()
	if err != nil {
		return fmt.Errorf("load state for self-repair: %w", err)
	}

	job, ok := state.Jobs[jobID]
	if !ok {
		return fmt.Errorf("job %q not found", jobID)
	}

	if !job.Enabled {
		return nil // nothing to repair
	}

	needsInstall, reason := r.checkInstallNeeded(job)
	if !needsInstall {
		return nil
	}

	r.Logger.Info("self-repairing schedule", logging.String("job_id", jobID), logging.String("reason", reason))

	params := ScheduleParams{
		JobID:    job.ID,
		Schedule: job.Schedule,
		Name:     SanitizeScheduleName(job.Name),
		Enabled:  true,
	}

	osState, err := r.Scheduler.Install(ctx, params)
	if err != nil {
		return fmt.Errorf("self-repair install: %w", err)
	}

	return r.Store.Update(ctx, func(s *State) error {
		j, ok := s.Jobs[jobID]
		if !ok {
			return fmt.Errorf("job %q disappeared", jobID)
		}
		j.OSSchedule = osState
		j.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// SanitizeScheduleName removes characters that are problematic in OS scheduler names.
// Strips all control characters (including \t, \n, \r) by replacing them with spaces.
func SanitizeScheduleName(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return ' '
		}
		return r
	}, name)
}
