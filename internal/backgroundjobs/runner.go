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

// Executor runs a single background job on demand.
type Executor struct {
	Store       *Store
	RunStore    *RunStore
	AuditWriter *AuditWriter
	ConfigPath  string
	Logger      *logging.Logger
	NewProvider func(id config.ProviderID, cfg analyzer.ProviderConfig) (analyzer.Provider, error)
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

// backgroundSystemMessage is injected into every background job session.
const backgroundSystemMessage = `You are running as a scheduled Dreamer background job.
Follow the user's prompt exactly.
Only write files allowed by the configured Dreamer permissions.
Do not ask interactive questions.
If the task cannot be completed, explain why in the final output.`

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
// 3. Rejects selected_writes and full_workspace (Phase 2).
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

	// Step 3: Resolve provider config.
	providerCfg, err := e.resolveProvider(job)
	if err != nil {
		return RunResult{}, err
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

	// Write job.run.claim audit event (best-effort, non-fatal).
	if auditErr := e.AuditWriter.Write(AuditEvent{
		Event: "job.run.claim",
		JobID: jobID,
		Details: map[string]any{
			"run_id": runID,
		},
	}); auditErr != nil {
		e.Logger.Warn("audit write failed (claim)", logging.Any("err", auditErr))
	}

	// Step 6: Acquire per-job lock, start provider, run prompt.
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
		ReadOnly:         true,
		SystemMessage:    backgroundSystemMessage,
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
		return true, RunResult{Record: run}, nil
	}

	if err := ValidateSchedule(job.Schedule); err != nil {
		return false, RunResult{}, fmt.Errorf("invalid schedule: %w", err)
	}

	caps := analyzer.LookupProviderCapabilities(config.ProviderID(job.ProviderID))
	if !caps.BackgroundSafe {
		return false, RunResult{}, fmt.Errorf("provider %q is not safe for background execution", job.ProviderID)
	}

	// Reject write modes until Phase 2.
	if job.Permissions.FileAccess != FileAccessReadOnly {
		return false, RunResult{}, fmt.Errorf("file access %q is not supported in Phase 1; only read_only is allowed", job.Permissions.FileAccess)
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
func (e *Executor) updateJobAfterRun(ctx context.Context, jobID string, jobSchedule ScheduleSpec, run Run, now time.Time) {
	nextRunAt, _ := NextRun(jobSchedule, now)
	if updateErr := e.Store.Update(ctx, func(s *State) error {
		j := s.Jobs[jobID]
		if j == nil {
			e.Logger.Warn("job deleted during run, skipping update", logging.Any("job_id", jobID))
			return nil
		}
		j.LastRunAt = &now
		j.NextRunAt = &nextRunAt
		j.Health.RunState = string(run.Status)
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
