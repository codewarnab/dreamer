package backgroundjobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
	"dreamer/internal/sandbox"
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

// MaxPromptSnapshotRunes caps the prompt stored in a Run record.
// Prevents JSONL bloat from verbose prompts while preserving debugging context.
const MaxPromptSnapshotRunes = 500

// MaxOutputSummaryRunes caps the output stored in a Run record.
// Exported so CLI commands (e.g. jobs logs) can reference the same limit.
const MaxOutputSummaryRunes = 500

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
	switch job.Permissions.FileAccess {
	case FileAccessSelectedWrites:
		if len(job.Permissions.WritablePaths) > 0 {
			msg += "\n\nYou may write to these specific files only:\n"
			for _, p := range job.Permissions.WritablePaths {
				msg += "  - " + p + "\n"
			}
			msg += "Do not write to any other files."
		}
	case FileAccessFullWorkspace:
		msg += "\n\nYou may write to any file within the project directory."
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
// 3. Validates file access mode (read_only, selected_writes, full_workspace).
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
		e.recordEarlyFailure(jobID, "", fmt.Errorf("load job store: %w", err))
		return RunResult{}, fmt.Errorf("load job store: %w", err)
	}
	job := state.Jobs[jobID]
	if job == nil {
		e.recordEarlyFailure(jobID, "", fmt.Errorf("job %q not found", jobID))
		return RunResult{}, fmt.Errorf("job %q not found", jobID)
	}

	// Step 2: Validate.
	skipped, skipResult, err := e.validateJob(job, jobID)
	if err != nil {
		e.recordEarlyFailure(jobID, job.ProviderID, err)
		return RunResult{}, err
	}
	if skipped {
		return skipResult, nil
	}

	// Step 3: Resolve provider config (before self-repair to avoid re-installing for broken configs).
	providerCfg, err := e.resolveProvider(job)
	if err != nil {
		e.recordEarlyFailure(jobID, job.ProviderID, err)
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
		PromptSnapshot: truncateUTF8(job.Prompt, MaxPromptSnapshotRunes),
	}

	// Warn when the OS sandbox is unavailable — the provider runs
	// fully permissive with no kernel-enforced file access control.
	if !sandbox.Available() && isCLIProvider(job.ProviderID) {
		warn := fmt.Sprintf(
			"OS sandbox unavailable on this platform (%s/%s); "+
				"background job runs fully permissive with no file access restrictions",
			runtime.GOOS, runtime.GOARCH)
		run.Warnings = append(run.Warnings, warn)
		e.Logger.Warn("sandbox unavailable for background job",
			logging.String("job_id", jobID),
			logging.String("provider", job.ProviderID),
			logging.String("platform", runtime.GOOS+"/"+runtime.GOARCH),
		)
	}

	// Build sandbox status snapshot for audit.
	sbStatus := SandboxStatus{
		Available: sandbox.Available(),
		// Intentionally hardcoded to true: Windows cannot isolate network for
		// Job Object children; on Linux/macOS the sandbox may isolate, but we
		// conservatively report open since the provider itself always has network.
		NetworkOpen: true,
		FileAccess:  string(job.Permissions.FileAccess),
	}
	if !sbStatus.Available {
		sbStatus.Warnings = append(sbStatus.Warnings, run.Warnings...)
	}
	// Warn about network being open on Windows where isolation is unavailable.
	if sbStatus.Available && runtime.GOOS == "windows" {
		run.Warnings = append(run.Warnings, "Network is open — external connections are not restricted")
		sbStatus.Warnings = append(sbStatus.Warnings, "Network is open — external connections are not restricted")
	}
	// Warn about full workspace write access.
	if job.Permissions.FileAccess == FileAccessFullWorkspace {
		run.Warnings = append(run.Warnings, "Full workspace access — provider can write to project files")
		sbStatus.Warnings = append(sbStatus.Warnings, "Full workspace access — provider can write to project files")
	}
	run.SandboxStatus = sbStatus

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
	e.Logger.Info("job run claimed",
		logging.String("job_id", jobID),
		logging.String("run_id", runID),
	)
	if auditErr := e.AuditWriter.Write(AuditEvent{
		Event: "job.run.claim",
		JobID: jobID,
		Details: map[string]any{
			"run_id": runID,
		},
	}); auditErr != nil {
		e.Logger.Warn("audit write failed (claim)", logging.Any("err", auditErr))
	}

	// Defer run-record persistence so the record is written even on panic.
	// The deferred closure captures run by pointer and finalises it.
	defer func() {
		if r := recover(); r != nil {
			now := time.Now().UTC()
			run.FinishedAt = &now
			run.DurationMillis = now.Sub(startedAt).Milliseconds()
			run.Status = RunStatusFailed
			run.Error = fmt.Sprintf("panic: %v", r)
			e.Logger.Error("job execution panicked",
				logging.String("job_id", jobID),
				logging.Any("panic", r),
			)
		}

		if appendErr := e.RunStore.Append(run); appendErr != nil {
			e.Logger.Warn("failed to record run", logging.Any("err", appendErr))
		}

		// Enforce run retention (best-effort, non-fatal).
		if _, pruneErr := e.RunStore.Prune(jobID, defaultRunRetention); pruneErr != nil {
			e.Logger.Warn("run retention prune failed", logging.String("job_id", jobID), logging.Any("err", pruneErr))
		}
	}()

	e.Logger.Info("job run starting",
		logging.String("job_id", jobID),
		logging.String("run_id", runID),
		logging.String("provider", job.ProviderID),
	)
	output, runErr := e.executeJob(ctx, job, providerCfg, runID, &run)

	// Record result.
	now := time.Now().UTC()
	run.FinishedAt = &now
	run.DurationMillis = now.Sub(startedAt).Milliseconds()
	run.OutputSummary = truncateUTF8(output, MaxOutputSummaryRunes)

	// Persist full output to a per-run log file so `jobs logs` can retrieve it.
	if logPath, writeErr := e.writeRunLog(jobID, runID, output); writeErr != nil {
		e.Logger.Warn("write run log failed", logging.Any("err", writeErr))
	} else {
		run.LogPath = logPath
	}

	if runErr != nil {
		// Classify off the returned error, not the parent context.
		// session.Run may surface a session-level timeout as a non-nil
		// runErr while ctx.Err() is still nil.
		if errors.Is(runErr, context.DeadlineExceeded) {
			run.Status = RunStatusTimedOut
			run.Error = runErr.Error()
		} else if errors.Is(runErr, context.Canceled) {
			run.Status = RunStatusCancelled
			run.Error = runErr.Error()
		} else {
			run.Status = RunStatusFailed
			run.Error = runErr.Error()
		}
	} else {
		run.Status = RunStatusCompleted
	}

	// Log completion.
	if runErr != nil {
		e.Logger.Error("job run failed",
			logging.String("job_id", jobID),
			logging.String("run_id", runID),
			logging.String("status", string(run.Status)),
			logging.Any("duration_ms", run.DurationMillis),
			logging.Any("error", runErr),
		)
	} else {
		e.Logger.Info("job run finished",
			logging.String("job_id", jobID),
			logging.String("run_id", runID),
			logging.String("status", string(run.Status)),
			logging.Any("duration_ms", run.DurationMillis),
		)
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
// Returns the response text and any error. The run parameter is used to
// store activity monitoring results (process/network events).
func (e *Executor) executeJob(ctx context.Context, job *Job, providerCfg analyzer.ProviderConfig, runID string, run *Run) (string, error) {
	// Map file access mode to sandbox write posture.
	// - full_workspace: grant project-dir writes via SandboxProjectWrite.
	// - selected_writes: grant per-path writes via SandboxWritableDirs.
	//   ValidateWritablePaths (called in validateJob) already ensures every
	//   path resolves inside the project root and avoids protected dirs.
	if job.Permissions.FileAccess == FileAccessFullWorkspace {
		providerCfg.SandboxProjectWrite = true
	} else if job.Permissions.FileAccess == FileAccessSelectedWrites && len(job.Permissions.WritablePaths) > 0 {
		providerCfg.SandboxWritableDirs = append([]string(nil), job.Permissions.WritablePaths...)
		if strings.HasSuffix(job.ProviderID, "-acp") {
			// ACP providers spawn once before the project dir is known, so
			// SandboxWritableDirs can't be wired into the OS sandbox at
			// session time. The per-path restriction is passed via system
			// prompt only — a model that ignores the prompt can write
			// anywhere. CLI providers honor it via sandbox.Config.WritableDirs.
			// See providers.go SandboxWritableDirs for the architectural note.
			e.Logger.Warn("ACP provider selected_writes: per-path sandbox stored but enforced only via system prompt, not OS-level",
				logging.String("provider", job.ProviderID))
		}
	}

	provider, err := e.NewProvider(config.ProviderID(job.ProviderID), providerCfg)
	if err != nil {
		return "", fmt.Errorf("create provider: %w", err)
	}
	defer provider.Close()

	if err := provider.Start(ctx); err != nil {
		return "", fmt.Errorf("start provider: %w", err)
	}

	// Set up activity monitoring. The PostStartHook is called by the
	// harness after the child process starts and the Job Object is
	// created. We create the store here and the monitor inside the hook
	// (since we need the job handle from the OS). The hook MUST be set
	// before NewSession: the harness copies it out of the config at
	// session creation, so a later assignment is silently ignored.
	var actMonitor *ActivityMonitor
	actStore := NewActivityStore(e.RunStore.Dir(), job.ID, runID)
	defer actStore.Close()
	run.ActivityLogPath = ActivityLogPath(e.RunStore.Dir(), job.ID, runID)

	sessionCfg := analyzer.SessionConfig{
		WorkingDirectory: job.ProjectPath,
		Model:            providerCfg.Model,
		ReadOnly:         job.Permissions.FileAccess == FileAccessReadOnly,
		SystemMessage:    buildBackgroundSystemMessage(job),
		RunID:            runID,
		Sandbox:          providerCfg.Sandbox,
		PostStartHook: func(jobHandle uintptr) {
			actMonitor = NewActivityMonitor(jobHandle, 0, actStore, e.Logger)
			if startErr := actMonitor.Start(ctx); startErr != nil {
				e.Logger.Warn("activity monitor start failed", logging.Any("err", startErr))
				actMonitor = nil
			}
		},
	}

	session, err := provider.NewSession(ctx, sessionCfg)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer session.Close()

	// Derive session timeout from context deadline when available,
	// falling back to a schedule-derived default.
	sessionTimeout := DefaultTimeoutFor(job.Schedule)
	if deadline, ok := ctx.Deadline(); ok {
		sessionTimeout = time.Until(deadline)
		if sessionTimeout <= 0 {
			sessionTimeout = DefaultTimeoutFor(job.Schedule)
		}
	}

	output, err := session.Run(ctx, job.Prompt, sessionTimeout)

	// Finalize activity monitoring.
	if actMonitor != nil {
		summary := actMonitor.Stop()
		run.ActivitySummary = &summary
	}

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
	case FileAccessFullWorkspace:
		// ok — no WritablePaths required; sandbox posture resolved at execution time.
	default:
		return false, RunResult{}, fmt.Errorf("file access %q is not supported; only read_only, selected_writes, and full_workspace are allowed", job.Permissions.FileAccess)
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
	providerCfg := analyzer.ProviderConfigFromBlock(string(job.ProviderID), block, cfg.Sandbox)
	if job.Model != "" {
		providerCfg.Model = job.Model
	}
	providerCfg.Background = true
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

// recordEarlyFailure appends a failed Run record when Executor.Run exits before
// a run record exists (load failure, validation error, provider resolution failure).
// This ensures all job invocations produce a visible trace in the run history.
func (e *Executor) recordEarlyFailure(jobID, providerID string, runErr error) {
	now := time.Now().UTC()
	runID, genErr := GenerateRunID()
	if genErr != nil {
		runID = fmt.Sprintf("error-%d", now.UnixMilli())
		e.Logger.Warn("failed to generate run ID for early failure", logging.Any("err", genErr))
	}
	run := Run{
		ID:             runID,
		JobID:          jobID,
		ScheduledFor:   now,
		Status:         RunStatusFailed,
		StartedAt:      now,
		FinishedAt:     &now,
		DurationMillis: 0,
		ProviderID:     providerID,
		Error:          runErr.Error(),
	}
	if appendErr := e.RunStore.Append(run); appendErr != nil {
		e.Logger.Warn("failed to record early-exit run", logging.Any("err", appendErr))
	}
	if auditErr := e.AuditWriter.Write(AuditEvent{
		Event: "job.run.early_failure",
		JobID: jobID,
		Details: map[string]any{
			"run_id": run.ID,
			"error":  runErr.Error(),
		},
	}); auditErr != nil {
		e.Logger.Warn("audit write failed (early failure)", logging.Any("err", auditErr))
	}
}

// writeRunLog persists the full provider output to a per-run log file at
// <runs_dir>/<job_id>/<run_id>.log. Returns the absolute path on success.
func (e *Executor) writeRunLog(jobID, runID, output string) (string, error) {
	logDir := filepath.Join(e.RunStore.Dir(), jobID)
	if err := os.MkdirAll(logDir, fsutil.DirPerms); err != nil {
		return "", fmt.Errorf("create run log dir: %w", err)
	}
	logPath := filepath.Join(logDir, runID+".log")
	if err := fsutil.WriteFileAtomic(logPath, []byte(output), fsutil.SecretPerms); err != nil {
		return "", fmt.Errorf("write run log: %w", err)
	}
	return logPath, nil
}

// isCLIProvider reports whether the provider ID identifies a CLI-harness
// provider (one that spawns a child process and relies on the OS sandbox
// or its own policy flags for file access control).
func isCLIProvider(id string) bool {
	switch config.ProviderID(id) {
	case config.ProviderClaudeCLI,
		config.ProviderOpenClaudeCLI,
		config.ProviderGeminiCLI,
		config.ProviderCodexCLI:
		return true
	default:
		return false
	}
}
