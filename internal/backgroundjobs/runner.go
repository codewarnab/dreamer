package backgroundjobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"
	"unicode/utf8"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
)

// SelfRepairConfig holds the fields needed for OS schedule self-repair.
// Nil Scheduler disables self-repair entirely.
type SelfRepairConfig struct {
	Scheduler      Scheduler
	ExecutablePath string
	InstallID      string
	ConfigHash     string
	ExecHash       string
}

// Executor runs a single background job on demand.
type Executor struct {
	Store       *Store
	RunStore    *RunStore
	AuditWriter *AuditWriter
	ConfigPath  string
	Logger      *logging.Logger
	NewProvider func(id config.ProviderID, cfg analyzer.ProviderConfig) (analyzer.Provider, error)
	SelfRepair  *SelfRepairConfig // nil disables self-repair
}

// RunResult captures the outcome of a job run.
type RunResult struct {
	Record Run
}

// maxPromptSnapshotRunes caps the prompt stored in a Run record.
// Prevents JSONL bloat from verbose prompts while preserving debugging context.
const maxPromptSnapshotRunes = 500

// maxOutputSummaryRunes caps the output stored in a Run record.
const maxOutputSummaryRunes = 500

// defaultSessionTimeout is the fallback when no schedule-derived timeout exists.
const defaultSessionTimeout = 10 * time.Minute

// defaultRunRetention is the maximum number of runs kept per job.
const defaultRunRetention = 100

// backgroundSystemMessage is injected into every background job session.
const backgroundSystemMessage = `You are running as a scheduled Dreamer background job.
Follow the user's prompt exactly.
Only write files allowed by the configured Dreamer permissions.
Do not ask interactive questions.
If the task cannot be completed, explain why in the final output.`

// buildBackgroundSystemMessage returns the system message augmented with
// allowed write paths when the job has selected_writes access.
func buildBackgroundSystemMessage(job *Job) string {
	msg := backgroundSystemMessage
	if job.Permissions.FileAccess == FileAccessSelectedWrites && len(job.Permissions.WritablePaths) > 0 {
		msg += "\n\nYou may write to these specific files only:\n"
		for _, p := range job.Permissions.WritablePaths {
			msg += "  - " + p + "\n"
		}
		msg += "Do not write to any other files."
	}
	return msg
}

// GenerateRunID returns a random hex run ID with "r" prefix to visually
// distinguish from job IDs (which are bare 16-hex-char).
func GenerateRunID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return "r" + hex.EncodeToString(b), nil
}

// truncateUTF8 safely truncates a string to max runes, preserving valid UTF-8.
func truncateUTF8(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}

// Run executes a background job by ID. It:
// 1. Loads job from store; returns error if not found or deleted.
// 2. Validates: enabled, schedule valid, provider background-safe.
// 3. Rejects selected_writes and full_workspace (only read_only is supported).
// 4. Resolves provider config from current config + overlay.
// 5. Creates a run record with status "running"; writes job.run.claim audit.
// 6. Acquires per-job lock, starts provider, runs prompt with timeout.
// 7. Marks run completed/failed/timed_out.
// 8. Updates job last_run_at, next_run_at, health.
// 9. Writes job.run.finish audit event.
func (e *Executor) Run(ctx context.Context, jobID string) (RunResult, error) {
	// Step 1: Load job.
	state, err := e.Store.Load()
	if err != nil {
		return RunResult{}, fmt.Errorf("load job store: %w", err)
	}
	job := state.Jobs[jobID]
	if job == nil {
		return RunResult{}, fmt.Errorf("job %q not found", jobID)
	}

	// Step 2: Validate.
	skipped, skipResult, err := e.validateJob(job, jobID)
	if err != nil {
		return RunResult{}, err
	}
	if skipped {
		return skipResult, nil
	}

	// Step 3: Resolve provider config (before self-repair to avoid re-installing for broken configs).
	providerCfg, err := e.resolveProvider(job)
	if err != nil {
		return RunResult{}, err
	}

	// Step 3.5: Self-repair (bounded — single job only).
	if e.SelfRepair != nil && e.SelfRepair.Scheduler != nil {
		reconciler := &Reconciler{
			Scheduler:  e.SelfRepair.Scheduler,
			Store:      e.Store,
			Logger:     e.Logger,
			ConfigHash: e.SelfRepair.ConfigHash,
		}
		if repairErr := reconciler.SelfRepair(ctx, jobID); repairErr != nil {
			e.Logger.Warn("self-repair failed (non-fatal)", logging.String("job_id", jobID), logging.Any("error", repairErr))
		}
	}

	// Step 5: Create run record and execute.
	runID, err := GenerateRunID()
	if err != nil {
		return RunResult{}, err
	}
	startedAt := time.Now().UTC()

	run := Run{
		ID:             runID,
		JobID:          jobID,
		ScheduledFor:   startedAt,
		Status:         RunStatusRunning,
		StartedAt:      startedAt,
		ProviderID:     job.ProviderID,
		Model:          providerCfg.Model,
		PromptSnapshot: truncateUTF8(job.Prompt, maxPromptSnapshotRunes),
	}

	// Step 6: Acquire per-job lock first, then write audit claim.
	lockPath := filepath.Join(e.Store.Dir(), "locks", jobID+".lock")
	release, lockErr := fsutil.AcquireLock(lockPath, e.Logger)
	if lockErr != nil {
		run.Status = RunStatusFailed
		run.Error = fmt.Sprintf("acquire lock: %v", lockErr)
		now := time.Now().UTC()
		run.FinishedAt = &now
		run.DurationMillis = now.Sub(startedAt).Milliseconds()
		if appendErr := e.RunStore.Append(run); appendErr != nil {
			e.Logger.Warn("run record append failed (lock error path)", logging.Any("err", appendErr))
		}
		return RunResult{Record: run}, nil
	}

	// Defer release to prevent lock leak on panic.
	defer release()

	// Write job.run.claim audit event after lock acquired (best-effort, non-fatal).
	if auditErr := e.AuditWriter.Write(AuditEvent{
		Event: "job.run.claim",
		JobID: jobID,
		Details: map[string]any{
			"run_id": runID,
		},
	}); auditErr != nil {
		e.Logger.Warn("audit write failed (claim)", logging.Any("err", auditErr))
	}

	output, runErr := e.executeJob(ctx, job, providerCfg, runID)

	// Step 7: Record result.
	now := time.Now().UTC()
	run.FinishedAt = &now
	run.DurationMillis = now.Sub(startedAt).Milliseconds()
	run.OutputSummary = truncateUTF8(output, maxOutputSummaryRunes)

	if runErr != nil {
		if ctx.Err() != nil {
			run.Status = RunStatusCancelled
			run.Error = ctx.Err().Error()
		} else {
			run.Status = RunStatusFailed
			run.Error = runErr.Error()
		}
	} else {
		run.Status = RunStatusCompleted
	}

	if appendErr := e.RunStore.Append(run); appendErr != nil {
		e.Logger.Warn("failed to record run", logging.Any("err", appendErr))
	}

	// Enforce run retention (best-effort, non-fatal).
	if _, pruneErr := e.RunStore.Prune(jobID, defaultRunRetention); pruneErr != nil {
		e.Logger.Warn("run retention prune failed", logging.String("job_id", jobID), logging.Any("err", pruneErr))
	}

	// Step 8: Update job.
	e.updateJobAfterRun(ctx, jobID, job.Schedule, run, now)

	// Step 9: Write job.run.finish audit event (best-effort, non-fatal).
	if auditErr := e.AuditWriter.Write(AuditEvent{
		Event: "job.run.finish",
		JobID: jobID,
		Details: map[string]any{
			"run_id": runID,
			"status": run.Status,
		},
	}); auditErr != nil {
		e.Logger.Warn("audit write failed (finish)", logging.Any("err", auditErr))
	}

	return RunResult{Record: run}, nil
}

// executeJob starts a provider session and runs the job prompt.
// Returns the response text and any error.
func (e *Executor) executeJob(ctx context.Context, job *Job, providerCfg analyzer.ProviderConfig, runID string) (string, error) {
	provider, err := e.NewProvider(config.ProviderID(job.ProviderID), providerCfg)
	if err != nil {
		return "", fmt.Errorf("create provider: %w", err)
	}
	defer provider.Close()

	if err := provider.Start(ctx); err != nil {
		return "", fmt.Errorf("start provider: %w", err)
	}

	sessionCfg := analyzer.SessionConfig{
		WorkingDirectory: job.ProjectPath,
		Model:            providerCfg.Model,
		ReadOnly:         job.Permissions.FileAccess == FileAccessReadOnly,
		SystemMessage:    buildBackgroundSystemMessage(job),
		RunID:            runID,
	}

	session, err := provider.NewSession(ctx, sessionCfg)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer session.Close()

	// Derive session timeout from context deadline when available.
	sessionTimeout := defaultSessionTimeout
	if deadline, ok := ctx.Deadline(); ok {
		sessionTimeout = time.Until(deadline)
		if sessionTimeout <= 0 {
			sessionTimeout = defaultSessionTimeout
		}
	}
	output, err := session.Run(ctx, job.Prompt, sessionTimeout)
	if err != nil {
		return output, fmt.Errorf("session run: %w", err)
	}
	return output, nil
}

// validateJob checks that a job is eligible to run. If the job is disabled,
// it records a skipped run and returns (true, RunResult, nil). Otherwise
// returns (false, zero, nil) on success or (false, zero, err) on validation failure.
func (e *Executor) validateJob(job *Job, jobID string) (skipped bool, result RunResult, err error) {
	if !job.Enabled {
		now := time.Now().UTC()
		run := Run{
			ID:            mustGenerateRunID(),
			JobID:         jobID,
			ScheduledFor:  now,
			Status:        RunStatusSkipped,
			StartedAt:     now,
			ProviderID:    job.ProviderID,
			SkippedReason: "job is disabled",
		}
		if appendErr := e.RunStore.Append(run); appendErr != nil {
			e.Logger.Warn("failed to record skipped run", logging.Any("err", appendErr))
		}
		// Emit audit event so skipped runs are visible in the audit log.
		if auditErr := e.AuditWriter.Write(AuditEvent{
			Event: "job.run.skipped",
			JobID: jobID,
			Details: map[string]any{
				"run_id": run.ID,
				"reason": "job is disabled",
			},
		}); auditErr != nil {
			e.Logger.Warn("audit write failed (skipped)", logging.Any("err", auditErr))
		}
		return true, RunResult{Record: run}, nil
	}

	if err := ValidateSchedule(job.Schedule); err != nil {
		return false, RunResult{}, fmt.Errorf("invalid schedule: %w", err)
	}

	caps := analyzer.LookupProviderCapabilities(config.ProviderID(job.ProviderID))
	if !caps.BackgroundSafe {
		return false, RunResult{}, fmt.Errorf("provider %q is not safe for background execution", job.ProviderID)
	}

	// Validate file access mode.
	switch job.Permissions.FileAccess {
	case FileAccessReadOnly:
		// ok
	case FileAccessSelectedWrites:
		if len(job.Permissions.WritablePaths) == 0 {
			return false, RunResult{}, fmt.Errorf("selected_writes requires at least one writable path")
		}
		if err := ValidateWritablePaths(job.ProjectPath, job.Permissions.WritablePaths); err != nil {
			return false, RunResult{}, fmt.Errorf("invalid writable paths: %w", err)
		}
	default:
		return false, RunResult{}, fmt.Errorf("file access %q is not supported; only read_only and selected_writes are allowed", job.Permissions.FileAccess)
	}

	return false, RunResult{}, nil
}

// resolveProvider loads the provider config for a job, applying the job's model override.
func (e *Executor) resolveProvider(job *Job) (analyzer.ProviderConfig, error) {
	cfg, err := config.LoadConfigWithOverlay(e.ConfigPath, globalOverlayPath())
	if err != nil {
		return analyzer.ProviderConfig{}, fmt.Errorf("load config: %w", err)
	}
	_, block := cfg.ResolveProviderConfig(nil, string(job.ProviderID))
	providerCfg := analyzer.ProviderConfigFromBlock(string(job.ProviderID), block)
	if job.Model != "" {
		providerCfg.Model = job.Model
	}
	return providerCfg, nil
}

// updateJobAfterRun updates the job's LastRunAt, NextRunAt, Health.RunState, and UpdatedAt.
// Uses j.Schedule (current value under lock) to compute NextRun, not a stale snapshot.
func (e *Executor) updateJobAfterRun(ctx context.Context, jobID string, _ ScheduleSpec, run Run, now time.Time) {
	if updateErr := e.Store.Update(ctx, func(s *State) error {
		j := s.Jobs[jobID]
		if j == nil {
			e.Logger.Warn("job deleted during run, skipping update", logging.Any("job_id", jobID))
			return nil
		}
		j.LastRunAt = &now
		// B20: Recompute NextRun from the current schedule, not the captured snapshot.
		nextRunAt, _ := NextRun(j.Schedule, now)
		j.NextRunAt = &nextRunAt
		j.Health.RunState = run.Status
		j.UpdatedAt = now
		return nil
	}); updateErr != nil {
		e.Logger.Warn("job timestamp update failed", logging.Any("err", updateErr))
	}
}

// mustGenerateRunID wraps GenerateRunID, panicking on error (crypto/rand failure).
func mustGenerateRunID() string {
	id, err := GenerateRunID()
	if err != nil {
		panic(err)
	}
	return id
}

// globalOverlayPath returns the global overlay path, or empty string on error.
// Empty is valid — the overlay is optional.
func globalOverlayPath() string {
	path, err := config.GlobalOverlayPath()
	if err != nil {
		return ""
	}
	return path
}
