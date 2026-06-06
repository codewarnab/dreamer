package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
)

const (
	// maxJobsPerInstall caps the number of background jobs per install.
	maxJobsPerInstall = 128

	// maxCreateBodySize caps POST /api/jobs and POST /api/jobs/preview bodies.
	maxCreateBodySize = 64 * 1024

	// maxPromptSize caps the prompt field in create/preview payloads.
	maxPromptSize = 16 * 1024

	// defaultRunLimit is the default number of runs returned by run history.
	defaultRunLimit = 50

	// maxRunLimit is the maximum number of runs returned by run history.
	maxRunLimit = 200

	// auditDefaultLimit is the default number of audit events returned by
	// GET /api/jobs/audit. Larger than defaultRunLimit because audit events
	// are lightweight and operators typically want a broader window.
	auditDefaultLimit = 100

	// auditMaxLimit caps the ?limit= query param on the audit endpoint to
	// prevent unbounded memory allocation from large audit logs.
	auditMaxLimit = 500

	// jobDetailRecentRunCount is the number of recent runs returned in the
	// job detail view sidebar. Kept small to bound the response payload.
	jobDetailRecentRunCount = 10
)

// errJobNotFound is returned by store mutations when the job ID does not exist.
var errJobNotFound = errors.New("job not found")

// errJobLimitReached is returned when the maximum number of jobs is exceeded.
var errJobLimitReached = fmt.Errorf("job limit reached (%d)", maxJobsPerInstall)

// JobDeps holds the background job dependencies injected into handlers.
// All fields are nil-safe: when nil, jobs endpoints return 503.
type JobDeps struct {
	Store     JobStore
	Runs      RunStore
	Audit     AuditWriter
	Scheduler backgroundjobs.Scheduler // nil on unsupported platforms
	Executor  JobExecutor
	Events    *EventSink
	InstallID string
	// ResolveProviderMeta resolves provider metadata by ID. Defaults to
	// backgroundjobs.ProviderMetaByID when nil. Exposed for test injection.
	ResolveProviderMeta func(id string) *backgroundjobs.ProviderMeta
}

// JobStore is the subset of backgroundjobs.Store needed by web handlers.
type JobStore interface {
	Load() (*backgroundjobs.State, error)
	Update(ctx context.Context, mutate func(*backgroundjobs.State) error) error
}

// RunStore is the subset of backgroundjobs.RunStore needed by web handlers.
type RunStore interface {
	List(jobID string) ([]backgroundjobs.Run, error)
	Latest(jobID string) (*backgroundjobs.Run, error)
	Count(jobID string) (int, error)
	Dir() string
}

// AuditWriter is the subset of backgroundjobs.AuditWriter needed by web handlers.
type AuditWriter interface {
	Write(event backgroundjobs.AuditEvent) error
	ReadAll(limit int) ([]backgroundjobs.AuditEvent, error)
}

// JobExecutor is the subset of backgroundjobs.Executor needed by web handlers.
type JobExecutor interface {
	Run(ctx context.Context, jobID string) (backgroundjobs.RunResult, error)
}

// EventSink publishes events to the pipeline event bus.
type EventSink struct {
	PublishFunc func(evt string, payload map[string]any)
}

// createPayload is the parsed request body for job create/preview.
type createPayload struct {
	Name          string                      `json:"name"`
	Prompt        string                      `json:"prompt"`
	ProjectName   string                      `json:"project_name"`
	ProviderID    string                      `json:"provider_id"`
	Model         string                      `json:"model"`
	Schedule      backgroundjobs.ScheduleSpec `json:"schedule"`
	FileAccess    string                      `json:"file_access"`
	WritablePaths []string                    `json:"writable_paths"`
}

// editPayload is the parsed request body for PATCH /api/jobs/{id}.
// Pointer fields distinguish "not provided" (nil) from "set to empty".
type editPayload struct {
	Name          *string                      `json:"name,omitempty"`
	Prompt        *string                      `json:"prompt,omitempty"`
	ProviderID    *string                      `json:"provider_id,omitempty"`
	Model         *string                      `json:"model,omitempty"`
	Schedule      *backgroundjobs.ScheduleSpec `json:"schedule,omitempty"`
	FileAccess    *string                      `json:"file_access,omitempty"`
	WritablePaths *[]string                    `json:"writable_paths,omitempty"`
}

// applyJobEdits validates and applies a partial edit payload to a job.
// Returns warnings and an error. scheduleChanged indicates whether the
// caller should reinstall the OS schedule.
func applyJobEdits(job *backgroundjobs.Job, payload editPayload, lookup func(string) *backgroundjobs.ProviderMeta) (scheduleChanged bool, warnings []string, err error) {
	if payload.Name != nil {
		name := strings.TrimSpace(*payload.Name)
		runes := []rune(name)
		if len(runes) > 64 {
			name = string(runes[:64])
		}
		job.Name = name
	}

	if payload.Prompt != nil {
		if len(*payload.Prompt) > maxPromptSize {
			return false, nil, fmt.Errorf("prompt exceeds 16 KiB limit")
		}
		job.Prompt = *payload.Prompt
	}

	if payload.ProviderID != nil {
		meta := lookup(*payload.ProviderID)
		if meta == nil {
			return false, nil, fmt.Errorf("provider %q not found", *payload.ProviderID)
		}
		if !meta.BackgroundSafe {
			return false, nil, fmt.Errorf("provider %q is not safe for background execution", *payload.ProviderID)
		}
		if meta.RequiresNetwork {
			warnings = append(warnings, "Network required by provider for model transport")
		}
		job.ProviderID = *payload.ProviderID
	}

	if payload.Model != nil {
		job.Model = *payload.Model
	}

	if payload.Schedule != nil {
		if err := backgroundjobs.ValidateSchedule(*payload.Schedule); err != nil {
			return false, nil, fmt.Errorf("invalid schedule: %w", err)
		}
		if payload.Schedule.Kind == backgroundjobs.ScheduleCron && runtime.GOOS == "windows" {
			return false, nil, fmt.Errorf("cron schedules are not supported on Windows; use daily or weekly instead")
		}
		job.Schedule = *payload.Schedule
		scheduleChanged = true
	}

	if payload.FileAccess != nil {
		switch *payload.FileAccess {
		case "read_only":
			job.Permissions.FileAccess = backgroundjobs.FileAccessReadOnly
			job.Permissions.WritablePaths = nil
		case "selected_writes":
			job.Permissions.FileAccess = backgroundjobs.FileAccessSelectedWrites
			// WritablePaths must be provided either in this payload or already
			// set on the job. Reject if neither is true so the persisted state
			// is always valid (create/preview enforce the same invariant).
			if payload.WritablePaths == nil && len(job.Permissions.WritablePaths) == 0 {
				return false, nil, fmt.Errorf("writable_paths required when file_access=selected_writes")
			}
		case "full_workspace":
			job.Permissions.FileAccess = backgroundjobs.FileAccessFullWorkspace
			job.Permissions.WritablePaths = nil
		default:
			return false, nil, fmt.Errorf("file_access must be one of: read_only, selected_writes, full_workspace")
		}
	}

	if payload.WritablePaths != nil {
		if job.Permissions.FileAccess != backgroundjobs.FileAccessSelectedWrites {
			return false, nil, fmt.Errorf("writable_paths requires file_access=selected_writes")
		}
		if err := backgroundjobs.ValidateWritablePaths(job.ProjectPath, *payload.WritablePaths); err != nil {
			return false, nil, fmt.Errorf("writable_paths validation: %w", err)
		}
		job.Permissions.WritablePaths = *payload.WritablePaths
	}

	job.UpdatedAt = time.Now().UTC()
	return scheduleChanged, warnings, nil
}

// runGuards prevents concurrent run-now requests for the same job.
// Key: job ID (string). Value: *atomic.Bool (true = running).
// NOTE: package-level sync.Map — tests using tryAcquireRun must not run in
// parallel with each other on the same job ID.
var runGuards sync.Map

func tryAcquireRun(jobID string) bool {
	guard, _ := runGuards.LoadOrStore(jobID, &atomic.Bool{})
	return guard.(*atomic.Bool).CompareAndSwap(false, true)
}

func releaseRun(jobID string) {
	if guard, ok := runGuards.Load(jobID); ok {
		guard.(*atomic.Bool).Store(false)
	}
}

// resolveProviderMeta resolves provider metadata using the deps lookup function,
// falling back to backgroundjobs.ProviderMetaByID.
func resolveProviderMeta(deps JobDeps, id string) *backgroundjobs.ProviderMeta {
	if deps.ResolveProviderMeta != nil {
		return deps.ResolveProviderMeta(id)
	}
	return backgroundjobs.ProviderMetaByID(id)
}

// validateCreatePayload validates the create/preview payload and resolves
// the project path. Returns the resolved path and any warnings.
func validateCreatePayload(cfg *config.App, payload createPayload, lookup func(string) *backgroundjobs.ProviderMeta) (projectPath string, warnings []string, err error) {
	// 1. Prompt required, max 16 KiB.
	if strings.TrimSpace(payload.Prompt) == "" {
		return "", nil, fmt.Errorf("prompt is required")
	}
	if len(payload.Prompt) > maxPromptSize {
		return "", nil, fmt.Errorf("prompt exceeds 16 KiB limit")
	}

	// 2. Validate schedule.
	if err := backgroundjobs.ValidateSchedule(payload.Schedule); err != nil {
		return "", nil, fmt.Errorf("invalid schedule: %w", err)
	}
	// Cron schedules are not supported on Windows (Task Scheduler has no cron equivalent).
	if payload.Schedule.Kind == backgroundjobs.ScheduleCron && runtime.GOOS == "windows" {
		return "", nil, fmt.Errorf("cron schedules are not supported on Windows; use daily or weekly instead")
	}

	// 3. Validate provider is background-safe.
	meta := lookup(payload.ProviderID)
	if meta == nil {
		return "", nil, fmt.Errorf("provider %q not found", payload.ProviderID)
	}
	if !meta.BackgroundSafe {
		return "", nil, fmt.Errorf("provider %q is not safe for background execution", payload.ProviderID)
	}

	// Network warning when provider requires network for model transport.
	if meta.RequiresNetwork {
		warnings = append(warnings, "Network required by provider for model transport")
	}

	// 4. Resolve project path — validate against config project list.
	projectPath, err = resolveProjectPath(cfg, payload.ProjectName)
	if err != nil {
		return "", nil, err
	}

	return projectPath, warnings, nil
}

// resolveProjectPath validates project_name against the config's project list.
// Rejects names not in config.Projects to prevent path traversal.
func resolveProjectPath(cfg *config.App, projectName string) (string, error) {
	if projectName == "" {
		if len(cfg.Projects) == 0 {
			return "", fmt.Errorf("no projects configured; pass project_name explicitly")
		}
		return cfg.Projects[0].Path, nil
	}
	for _, p := range cfg.Projects {
		if p.Name == projectName {
			return p.Path, nil
		}
	}
	return "", fmt.Errorf("project %q not found in configuration", projectName)
}

// deriveNameFromPrompt derives a job name from the prompt by taking the first
// line, truncating to 64 chars, and trimming whitespace.
func deriveNameFromPrompt(prompt string) string {
	name := prompt
	if idx := strings.IndexByte(name, '\n'); idx >= 0 {
		name = name[:idx]
	}
	name = strings.TrimSpace(name)
	// Truncate to 64 runes (not bytes) for CJK safety.
	runes := []rune(name)
	if len(runes) > 64 {
		name = string(runes[:64])
	}
	if name == "" {
		name = "background job"
	}
	return name
}

// sanitizeName strips newlines and control characters from a job name
// for safe use in SSE event types.
func sanitizeName(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
}

// requireJobStore returns true if the store is available; otherwise writes 503.
func requireJobStore(w http.ResponseWriter, store JobStore) bool {
	if store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "background jobs not configured")
		return false
	}
	return true
}

// publishJobEvent publishes a background job event to the event bus.
func publishJobEvent(sink *EventSink, evt string, payload map[string]any) {
	if sink == nil || sink.PublishFunc == nil {
		return
	}
	sink.PublishFunc(evt, payload)
}

// setJobEnabled toggles a job's enabled state and returns the job name,
// schedule, and any error. Returns errJobNotFound when the ID is missing.
func setJobEnabled(store JobStore, jobID string, enabled bool) (jobName string, schedule backgroundjobs.ScheduleSpec, err error) {
	err = store.Update(context.Background(), func(s *backgroundjobs.State) error {
		job, ok := s.Jobs[jobID]
		if !ok {
			return errJobNotFound
		}
		job.Enabled = enabled
		job.UpdatedAt = time.Now().UTC()
		jobName = job.Name
		schedule = job.Schedule
		return nil
	})
	return
}

// --- Handlers ---

// JobList returns an http.HandlerFunc for GET /api/jobs.
func JobList(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}

		state, err := deps.Jobs.Store.Load()
		if err != nil {
			deps.Logger.Error("jobs list load", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to load jobs")
			return
		}

		jobs := make([]map[string]any, 0, len(state.Jobs))
		for _, job := range state.Jobs {
			runCount, _ := deps.Jobs.Runs.Count(job.ID)
			entry := map[string]any{
				"id":               job.ID,
				"name":             sanitizeName(job.Name),
				"provider_id":      job.ProviderID,
				"schedule":         job.Schedule,
				"schedule_summary": buildScheduleSummary(job.Schedule),
				"enabled":          job.Enabled,
				"run_count":        runCount,
			}
			if job.NextRunAt != nil {
				entry["next_run_at"] = job.NextRunAt.UTC().Format(time.RFC3339)
			}
			if job.LastRunAt != nil {
				entry["last_run_at"] = job.LastRunAt.UTC().Format(time.RFC3339)
			}
			entry["run_state"] = string(job.Health.RunState)
			jobs = append(jobs, entry)
		}

		// System health summary.
		enabledCount := 0
		for _, j := range state.Jobs {
			if j.Enabled {
				enabledCount++
			}
		}
		schedulingAvailable := deps.Jobs.Scheduler != nil

		writeJSON(w, http.StatusOK, map[string]any{
			"jobs": jobs,
			"system_health": map[string]any{
				"scheduling_available": schedulingAvailable,
				"total_jobs":           len(state.Jobs),
				"enabled_jobs":         enabledCount,
			},
		})
	}
}

// JobCreate returns an http.HandlerFunc for POST /api/jobs.
func JobCreate(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxCreateBodySize)
		var payload createPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		cfg := deps.Config()
		projectPath, warnings, err := validateCreatePayload(cfg, payload, func(id string) *backgroundjobs.ProviderMeta {
			return resolveProviderMeta(deps.Jobs, id)
		})
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Derive name if empty.
		name := strings.TrimSpace(payload.Name)
		if name == "" {
			name = deriveNameFromPrompt(payload.Prompt)
		}
		// Truncate name to 64 runes for safety.
		runes := []rune(name)
		if len(runes) > 64 {
			name = string(runes[:64])
		}

		// Parse and validate permissions.
		fileAccess := backgroundjobs.FileAccessReadOnly
		if payload.FileAccess != "" {
			switch payload.FileAccess {
			case "read_only":
				fileAccess = backgroundjobs.FileAccessReadOnly
			case "selected_writes":
				fileAccess = backgroundjobs.FileAccessSelectedWrites
			case "full_workspace":
				fileAccess = backgroundjobs.FileAccessFullWorkspace
			default:
				writeJSONError(w, http.StatusBadRequest, "file_access must be one of: read_only, selected_writes, full_workspace")
				return
			}
		}

		var writablePaths []string
		if len(payload.WritablePaths) > 0 {
			if fileAccess != backgroundjobs.FileAccessSelectedWrites {
				writeJSONError(w, http.StatusBadRequest, "writable_paths requires file_access=selected_writes")
				return
			}
			if err := backgroundjobs.ValidateWritablePaths(projectPath, payload.WritablePaths); err != nil {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("writable_paths validation: %v", err))
				return
			}
			writablePaths = payload.WritablePaths
		} else if fileAccess == backgroundjobs.FileAccessSelectedWrites {
			writeJSONError(w, http.StatusBadRequest, "writable_paths required when file_access=selected_writes")
			return
		}

		jobID, err := backgroundjobs.GenerateJobID()
		if err != nil {
			deps.Logger.Error("jobs create generate id", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to generate job id")
			return
		}

		now := time.Now().UTC()
		nextRun, nextErr := backgroundjobs.NextRun(payload.Schedule, now)
		if nextErr != nil {
			deps.Logger.Warn("jobs create next run", logging.ErrAttr(nextErr)...)
		}

		job := &backgroundjobs.Job{
			ID:          jobID,
			Name:        name,
			Prompt:      payload.Prompt,
			ProjectName: payload.ProjectName,
			ProjectPath: projectPath,
			ProviderID:  payload.ProviderID,
			Model:       payload.Model,
			Schedule:    payload.Schedule,
			Enabled:     true,
			CreatedAt:   now,
			UpdatedAt:   now,
			NextRunAt:   &nextRun,
			Permissions: backgroundjobs.PermissionProfile{
				FileAccess:              fileAccess,
				WritablePaths:           writablePaths,
				ProviderNetworkRequired: true,
			},
		}

		if err := deps.Jobs.Store.Update(r.Context(), func(s *backgroundjobs.State) error {
			if len(s.Jobs) >= maxJobsPerInstall {
				return errJobLimitReached
			}
			if s.Jobs == nil {
				s.Jobs = make(map[string]*backgroundjobs.Job)
			}
			s.Jobs[jobID] = job
			return nil
		}); err != nil {
			if errors.Is(err, errJobLimitReached) {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			deps.Logger.Error("jobs create store", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to create job")
			return
		}

		// Best-effort OS schedule install.
		if deps.Jobs.Scheduler != nil {
			_, schedErr := deps.Jobs.Scheduler.Install(r.Context(), backgroundjobs.ScheduleParams{
				JobID:    jobID,
				Schedule: payload.Schedule,
				Name:     name,
				Enabled:  true,
			})
			if schedErr != nil {
				warnings = append(warnings, "OS schedule install failed — run 'dreamer jobs reconcile' to retry")
				deps.Logger.Warn("jobs create schedule install", logging.ErrAttr(schedErr)...)
			}
		}

		// Audit event — best-effort, non-critical.
		if deps.Jobs.Audit != nil {
			_ = deps.Jobs.Audit.Write(backgroundjobs.AuditEvent{
				Timestamp: now,
				Event:     "job.create",
				JobID:     jobID,
				Actor:     "web",
				Details: map[string]any{
					"name": sanitizeName(name),
				},
			})
		}

		publishJobEvent(deps.Jobs.Events, pipeline.EventJobCreated, map[string]any{
			"job_id": jobID,
		})

		writeJSON(w, http.StatusCreated, map[string]any{
			"job_id":      jobID,
			"next_run_at": nextRun.UTC().Format(time.RFC3339),
			"warnings":    warnings,
		})
	}
}

// JobEdit returns an http.HandlerFunc for PATCH /api/jobs/{id}.
func JobEdit(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}

		jobID := extractJobID(r.URL.Path)
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing job id")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxCreateBodySize)
		var payload editPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		// Validate that at least one field is provided.
		if payload.Name == nil && payload.Prompt == nil && payload.ProviderID == nil &&
			payload.Model == nil && payload.Schedule == nil && payload.FileAccess == nil &&
			payload.WritablePaths == nil {
			writeJSONError(w, http.StatusBadRequest, "no fields to update")
			return
		}

		var scheduleChanged bool
		var warnings []string
		var updatedJob *backgroundjobs.Job

		err := deps.Jobs.Store.Update(r.Context(), func(s *backgroundjobs.State) error {
			job, ok := s.Jobs[jobID]
			if !ok {
				return errJobNotFound
			}
			var editErr error
			scheduleChanged, warnings, editErr = applyJobEdits(job, payload, func(id string) *backgroundjobs.ProviderMeta {
				return resolveProviderMeta(deps.Jobs, id)
			})
			if editErr != nil {
				return editErr
			}
			if scheduleChanged {
				nextRun, nextErr := backgroundjobs.NextRun(job.Schedule, time.Now().UTC())
				if nextErr == nil {
					job.NextRunAt = &nextRun
				}
			}
			updatedJob = job
			return nil
		})
		if err != nil {
			if errors.Is(err, errJobNotFound) {
				writeJSONError(w, http.StatusNotFound, "job not found")
				return
			}
			// Validation errors from applyJobEdits.
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Reinstall OS schedule if schedule changed (best-effort).
		if scheduleChanged && deps.Jobs.Scheduler != nil && updatedJob != nil {
			_, schedErr := deps.Jobs.Scheduler.Install(r.Context(), backgroundjobs.ScheduleParams{
				JobID:    jobID,
				Schedule: updatedJob.Schedule,
				Name:     sanitizeName(updatedJob.Name),
				Enabled:  updatedJob.Enabled,
			})
			if schedErr != nil {
				warnings = append(warnings, "OS schedule update failed — run 'dreamer jobs reconcile' to retry")
				deps.Logger.Warn("jobs edit schedule install", logging.ErrAttr(schedErr)...)
			}
		}

		// Audit — best-effort.
		if deps.Jobs.Audit != nil {
			_ = deps.Jobs.Audit.Write(backgroundjobs.AuditEvent{
				Timestamp: time.Now().UTC(),
				Event:     "job.edit",
				JobID:     jobID,
				Actor:     "web",
			})
		}

		publishJobEvent(deps.Jobs.Events, pipeline.EventJobUpdated, map[string]any{
			"job_id": jobID,
		})

		result := map[string]any{
			"job_id":   jobID,
			"updated":  true,
			"warnings": warnings,
		}
		if updatedJob != nil {
			result["schedule_summary"] = buildScheduleSummary(updatedJob.Schedule)
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// JobPreview returns an http.HandlerFunc for POST /api/jobs/preview.
func JobPreview(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxCreateBodySize)
		var payload createPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		cfg := deps.Config()
		projectPath, warnings, err := validateCreatePayload(cfg, payload, func(id string) *backgroundjobs.ProviderMeta {
			return resolveProviderMeta(deps.Jobs, id)
		})
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Parse and validate permissions.
		fileAccess := backgroundjobs.FileAccessReadOnly
		if payload.FileAccess != "" {
			switch payload.FileAccess {
			case "read_only":
				fileAccess = backgroundjobs.FileAccessReadOnly
			case "selected_writes":
				fileAccess = backgroundjobs.FileAccessSelectedWrites
			case "full_workspace":
				fileAccess = backgroundjobs.FileAccessFullWorkspace
			default:
				writeJSONError(w, http.StatusBadRequest, "file_access must be one of: read_only, selected_writes, full_workspace")
				return
			}
		}

		var writablePaths []string
		if len(payload.WritablePaths) > 0 {
			if fileAccess != backgroundjobs.FileAccessSelectedWrites {
				writeJSONError(w, http.StatusBadRequest, "writable_paths requires file_access=selected_writes")
				return
			}
			if err := backgroundjobs.ValidateWritablePaths(projectPath, payload.WritablePaths); err != nil {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("writable_paths validation: %v", err))
				return
			}
			writablePaths = payload.WritablePaths
		} else if fileAccess == backgroundjobs.FileAccessSelectedWrites {
			writeJSONError(w, http.StatusBadRequest, "writable_paths required when file_access=selected_writes")
			return
		}

		// Compute next 3 runs.
		now := time.Now().UTC()
		nextRuns := make([]string, 0, 3)
		cursor := now
		for i := 0; i < 3; i++ {
			next, nextErr := backgroundjobs.NextRun(payload.Schedule, cursor)
			if nextErr != nil {
				break
			}
			nextRuns = append(nextRuns, next.UTC().Format(time.RFC3339))
			cursor = next.Add(time.Second)
		}

		// Provider info.
		meta := resolveProviderMeta(deps.Jobs, payload.ProviderID)
		providerInfo := map[string]any{
			"id":              meta.ID,
			"display_name":    meta.DisplayName,
			"background_safe": meta.BackgroundSafe,
		}

		// Schedule summary.
		scheduleSummary := buildScheduleSummary(payload.Schedule)

		writeJSON(w, http.StatusOK, map[string]any{
			"valid":            true,
			"can_create":       true,
			"warnings":         warnings,
			"schedule_summary": scheduleSummary,
			"next_3_runs":      nextRuns,
			"provider":         providerInfo,
			"permissions": map[string]any{
				"file_access":      fileAccess,
				"writable_paths":   writablePaths,
				"network_required": meta.RequiresNetwork,
			},
		})
	}
}

// JobHealth returns an http.HandlerFunc for GET /api/jobs/health.
func JobHealth(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}

		state, err := deps.Jobs.Store.Load()
		if err != nil {
			deps.Logger.Error("jobs health load", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to load jobs")
			return
		}

		enabledCount := 0
		for _, j := range state.Jobs {
			if j.Enabled {
				enabledCount++
			}
		}

		result := map[string]any{
			"scheduling_available": deps.Jobs.Scheduler != nil,
			"total_jobs":           len(state.Jobs),
			"enabled_jobs":         enabledCount,
		}

		// Per-job schedule health via scheduler Inspect.
		if deps.Jobs.Scheduler != nil {
			scheduledCount := 0
			var issues []map[string]any
			for jobID, job := range state.Jobs {
				if !job.Enabled {
					continue
				}
				health, inspectErr := deps.Jobs.Scheduler.Inspect(r.Context(), jobID)
				if inspectErr != nil {
					issues = append(issues, map[string]any{
						"job_id":   jobID,
						"severity": "warning",
						"message":  fmt.Sprintf("inspect schedule: %v", inspectErr),
					})
					continue
				}
				if health.Installed {
					scheduledCount++
				} else {
					issues = append(issues, map[string]any{
						"job_id":   jobID,
						"severity": "warning",
						"message":  "enabled job has no OS schedule — run 'dreamer jobs reconcile'",
					})
				}
			}
			result["scheduled_jobs"] = scheduledCount
			result["issues"] = issues
			result["system_healthy"] = len(issues) == 0
		}

		writeJSON(w, http.StatusOK, result)
	}
}

// JobAuditLog returns an http.HandlerFunc for GET /api/jobs/audit.
func JobAuditLog(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Jobs.Audit == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit log not available")
			return
		}

		limit := auditDefaultLimit
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				if n > auditMaxLimit {
					n = auditMaxLimit
				}
				limit = n
			}
		}

		events, err := deps.Jobs.Audit.ReadAll(limit)
		if err != nil {
			deps.Logger.Error("audit read", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to read audit log")
			return
		}

		// Filter by job_id if specified.
		if jobFilter := r.URL.Query().Get("job_id"); jobFilter != "" {
			filtered := make([]backgroundjobs.AuditEvent, 0, len(events))
			for _, ev := range events {
				if ev.JobID == jobFilter {
					filtered = append(filtered, ev)
				}
			}
			events = filtered
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"events": events,
			"total":  len(events),
		})
	}
}

// JobDetail returns an http.HandlerFunc for GET /api/jobs/{id}.
func JobDetail(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}

		jobID := extractJobID(r.URL.Path)
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing job id")
			return
		}

		state, err := deps.Jobs.Store.Load()
		if err != nil {
			deps.Logger.Error("job detail load", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to load job")
			return
		}

		job, ok := state.Jobs[jobID]
		if !ok {
			writeJSONError(w, http.StatusNotFound, "job not found")
			return
		}

		// Latest run.
		var latestRun *backgroundjobs.Run
		if deps.Jobs.Runs != nil {
			latestRun, _ = deps.Jobs.Runs.Latest(jobID)
		}

		// Recent runs (last jobDetailRecentRunCount).
		var recentRuns []backgroundjobs.Run
		if deps.Jobs.Runs != nil {
			allRuns, _ := deps.Jobs.Runs.List(jobID)
			if len(allRuns) > jobDetailRecentRunCount {
				recentRuns = allRuns[:jobDetailRecentRunCount]
			} else {
				recentRuns = allRuns
			}
		}

		// OS schedule info.
		var osSchedule map[string]any
		if deps.Jobs.Scheduler != nil {
			health, err := deps.Jobs.Scheduler.Inspect(r.Context(), jobID)
			if err == nil {
				osSchedule = map[string]any{
					"installed": health.Installed,
					"enabled":   health.Enabled,
				}
				if health.NextRunTime != nil {
					osSchedule["next_run_time"] = health.NextRunTime.UTC().Format(time.RFC3339)
				}
			}
		}

		// Next 3 runs.
		now := time.Now().UTC()
		nextRuns := make([]string, 0, 3)
		cursor := now
		for i := 0; i < 3; i++ {
			next, nextErr := backgroundjobs.NextRun(job.Schedule, cursor)
			if nextErr != nil {
				break
			}
			nextRuns = append(nextRuns, next.UTC().Format(time.RFC3339))
			cursor = next.Add(time.Second)
		}

		// Build response without exposing project_path.
		jobView := map[string]any{
			"id":           job.ID,
			"name":         sanitizeName(job.Name),
			"prompt":       job.Prompt,
			"project_name": job.ProjectName,
			"provider_id":  job.ProviderID,
			"model":        job.Model,
			"schedule":     job.Schedule,
			"enabled":      job.Enabled,
			"created_at":   job.CreatedAt,
			"updated_at":   job.UpdatedAt,
			"permissions":  job.Permissions,
			"health":       job.Health,
		}
		if job.LastRunAt != nil {
			jobView["last_run_at"] = job.LastRunAt
		}
		if job.NextRunAt != nil {
			jobView["next_run_at"] = job.NextRunAt
		}

		// Sandbox status from the latest run (if available).
		var sandboxStatus any
		if latestRun != nil {
			sandboxStatus = latestRun.SandboxStatus
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"job":              jobView,
			"latest_run":       latestRun,
			"recent_runs":      recentRuns,
			"os_schedule":      osSchedule,
			"schedule_summary": buildScheduleSummary(job.Schedule),
			"next_3_runs":      nextRuns,
			"sandbox_status":   sandboxStatus,
		})
	}
}

// JobDelete returns an http.HandlerFunc for DELETE /api/jobs/{id}.
func JobDelete(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}

		jobID := extractJobID(r.URL.Path)
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing job id")
			return
		}

		// Verify job exists.
		state, err := deps.Jobs.Store.Load()
		if err != nil {
			deps.Logger.Error("job delete load", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to load job")
			return
		}
		if _, ok := state.Jobs[jobID]; !ok {
			writeJSONError(w, http.StatusNotFound, "job not found")
			return
		}

		// Remove OS schedule first (best-effort).
		if deps.Jobs.Scheduler != nil {
			_ = deps.Jobs.Scheduler.Remove(r.Context(), jobID)
		}

		// Delete from store.
		if err := deps.Jobs.Store.Update(r.Context(), func(s *backgroundjobs.State) error {
			delete(s.Jobs, jobID)
			return nil
		}); err != nil {
			deps.Logger.Error("job delete store", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to delete job")
			return
		}

		// Cleanup run guard entry.
		runGuards.Delete(jobID)

		// Audit — best-effort, non-critical.
		if deps.Jobs.Audit != nil {
			_ = deps.Jobs.Audit.Write(backgroundjobs.AuditEvent{
				Timestamp: time.Now().UTC(),
				Event:     "job.delete",
				JobID:     jobID,
				Actor:     "web",
			})
		}

		publishJobEvent(deps.Jobs.Events, pipeline.EventJobDeleted, map[string]any{
			"job_id": jobID,
		})

		w.WriteHeader(http.StatusNoContent)
	}
}

// JobRunNow returns an http.HandlerFunc for POST /api/jobs/{id}/run.
func JobRunNow(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}

		jobID := extractJobID(r.URL.Path)
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing job id")
			return
		}

		if !tryAcquireRun(jobID) {
			writeJSONError(w, http.StatusConflict, "run already in progress")
			return
		}

		// Verify job exists.
		state, err := deps.Jobs.Store.Load()
		if err != nil {
			releaseRun(jobID)
			deps.Logger.Error("job run load", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to load job")
			return
		}
		job, ok := state.Jobs[jobID]
		if !ok {
			releaseRun(jobID)
			writeJSONError(w, http.StatusNotFound, "job not found")
			return
		}

		// Return 202 Accepted immediately; execute in background.
		writeJSON(w, http.StatusAccepted, map[string]any{
			"job_id":   jobID,
			"job_name": sanitizeName(job.Name),
			"status":   "running",
		})

		publishJobEvent(deps.Jobs.Events, pipeline.EventJobRunStart, map[string]any{
			"job_id": jobID,
		})

		go func() {
			defer releaseRun(jobID)
			defer func() {
				if r := recover(); r != nil {
					deps.Logger.Error("job run panic", logging.Any("panic", r))
					publishJobEvent(deps.Jobs.Events, pipeline.EventJobRunDone, map[string]any{
						"job_id": jobID,
						"status": "failed",
					})
				}
			}()

			result, err := deps.Jobs.Executor.Run(deps.ShutdownCtx, jobID)
			if err != nil {
				deps.Logger.Error("job run executor", logging.ErrAttr(err)...)
				publishJobEvent(deps.Jobs.Events, pipeline.EventJobRunDone, map[string]any{
					"job_id": jobID,
					"status": "failed",
				})
				return
			}

			eventData := map[string]any{
				"job_id":          jobID,
				"run_id":          result.Record.ID,
				"status":          string(result.Record.Status),
				"duration_millis": result.Record.DurationMillis,
			}
			if len(result.Record.Warnings) > 0 {
				eventData["warnings"] = result.Record.Warnings
			}
			publishJobEvent(deps.Jobs.Events, pipeline.EventJobRunDone, eventData)
		}()
	}
}

// JobSetEnabled returns an http.HandlerFunc for POST /api/jobs/{id}/pause|resume.
func JobSetEnabled(deps Deps, enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}

		jobID := extractJobID(r.URL.Path)
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing job id")
			return
		}

		jobName, schedule, err := setJobEnabled(deps.Jobs.Store, jobID, enabled)
		if err != nil {
			if errors.Is(err, errJobNotFound) {
				writeJSONError(w, http.StatusNotFound, "job not found")
				return
			}
			deps.Logger.Error("job set enabled store", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to update job")
			return
		}

		var warnings []string

		// Best-effort OS schedule operation.
		if deps.Jobs.Scheduler != nil {
			if enabled {
				_, schedErr := deps.Jobs.Scheduler.Install(r.Context(), backgroundjobs.ScheduleParams{
					JobID:    jobID,
					Schedule: schedule,
					Name:     jobName,
					Enabled:  true,
				})
				if schedErr != nil {
					warnings = append(warnings, "OS schedule install failed — run 'dreamer jobs reconcile' to retry")
				}
			} else {
				if schedErr := deps.Jobs.Scheduler.Remove(r.Context(), jobID); schedErr != nil {
					warnings = append(warnings, "OS schedule removal failed — run 'dreamer jobs reconcile' to retry")
				}
			}
		}

		// Audit — best-effort, non-critical.
		evt := "job.resume"
		if !enabled {
			evt = "job.pause"
		}
		if deps.Jobs.Audit != nil {
			_ = deps.Jobs.Audit.Write(backgroundjobs.AuditEvent{
				Timestamp: time.Now().UTC(),
				Event:     evt,
				JobID:     jobID,
				Actor:     "web",
				Details: map[string]any{
					"name": sanitizeName(jobName),
				},
			})
		}

		pubEvt := pipeline.EventJobResumed
		if !enabled {
			pubEvt = pipeline.EventJobPaused
		}
		publishJobEvent(deps.Jobs.Events, pubEvt, map[string]any{
			"job_id": jobID,
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"job_id":   jobID,
			"enabled":  enabled,
			"warnings": warnings,
		})
	}
}

// JobRuns returns an http.HandlerFunc for GET /api/jobs/{id}/runs.
func JobRuns(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}
		if deps.Jobs.Runs == nil {
			writeJSON(w, http.StatusOK, map[string]any{"runs": []any{}, "total": 0})
			return
		}

		jobID := extractJobID(r.URL.Path)
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing job id")
			return
		}

		// Parse ?limit=N query param.
		limit := defaultRunLimit
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > maxRunLimit {
			limit = maxRunLimit
		}

		allRuns, err := deps.Jobs.Runs.List(jobID)
		if err != nil {
			deps.Logger.Error("job runs list", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to list runs")
			return
		}

		total := len(allRuns)
		if limit < len(allRuns) {
			allRuns = allRuns[:limit]
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"runs":  allRuns,
			"total": total,
		})
	}
}

// JobRunDetail returns an http.HandlerFunc for GET /api/jobs/{id}/runs/{rid}.
func JobRunDetail(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}
		if deps.Jobs.Runs == nil {
			writeJSONError(w, http.StatusNotFound, "run not found")
			return
		}

		jobID, runID := extractJobIDAndRunID(r.URL.Path)
		if jobID == "" || runID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing job or run id")
			return
		}

		runs, err := deps.Jobs.Runs.List(jobID)
		if err != nil {
			deps.Logger.Error("job run detail list", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to list runs")
			return
		}

		for _, run := range runs {
			if run.ID == runID {
				writeJSON(w, http.StatusOK, run)
				return
			}
		}

		writeJSONError(w, http.StatusNotFound, "run not found")
	}
}

// JobActivity returns an http.HandlerFunc for
// GET /api/jobs/{id}/runs/{rid}/activity.
// Returns the activity events (process/network) for a specific run.
func JobActivity(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireJobStore(w, deps.Jobs.Store) {
			return
		}
		if deps.Jobs.Runs == nil {
			writeJSONError(w, http.StatusNotFound, "run not found")
			return
		}

		jobID, runID := extractJobIDAndRunID(r.URL.Path)
		if jobID == "" || runID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing job or run id")
			return
		}

		// Find the run to get the activity log path.
		runs, err := deps.Jobs.Runs.List(jobID)
		if err != nil {
			deps.Logger.Error("activity list runs", logging.ErrAttr(err)...)
			writeJSONError(w, http.StatusInternalServerError, "failed to list runs")
			return
		}

		var targetRun *backgroundjobs.Run
		for i := range runs {
			if runs[i].ID == runID {
				targetRun = &runs[i]
				break
			}
		}
		if targetRun == nil {
			writeJSONError(w, http.StatusNotFound, "run not found")
			return
		}

		// Read activity events from the JSONL file.
		events, actErr := backgroundjobs.ReadAllActivity(deps.Jobs.Runs.Dir(), jobID, runID)
		if actErr != nil {
			deps.Logger.Warn("activity read failed", logging.ErrAttr(actErr)...)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"events":  events,
			"summary": targetRun.ActivitySummary,
		})
	}
}

// --- Route dispatcher ---

// RouteJobs returns a handler that dispatches /api/jobs[/...] requests.
// Handles all sub-routes: preview, health, {id}, {id}/run, {id}/pause,
// {id}/resume, {id}/runs, {id}/runs/{rid}.
func RouteJobs(deps Deps) http.HandlerFunc {
	// Pre-build per-resource handlers once, not per request.
	detail := JobDetail(deps)
	editH := JobEdit(deps)
	deleteH := JobDelete(deps)
	runNow := JobRunNow(deps)
	pause := JobSetEnabled(deps, false)
	resume := JobSetEnabled(deps, true)
	runs := JobRuns(deps)
	runDetail := JobRunDetail(deps)
	preview := JobPreview(deps)
	health := JobHealth(deps)
	auditLog := JobAuditLog(deps)
	activityH := JobActivity(deps)

	return func(w http.ResponseWriter, r *http.Request) {
		// Strip both /api/jobs/ and /api/jobs prefixes.
		rest := r.URL.Path
		rest = strings.TrimPrefix(rest, "/api/jobs/")
		if rest == r.URL.Path {
			// Prefix didn't match — try without trailing slash.
			rest = strings.TrimPrefix(rest, "/api/jobs")
		}
		rest = strings.TrimSuffix(rest, "/")

		// Empty path = collection endpoint.
		if rest == "" {
			switch r.Method {
			case http.MethodGet:
				JobList(deps)(w, r)
			case http.MethodPost:
				JobCreate(deps)(w, r)
			default:
				writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}

		parts := strings.Split(rest, "/")

		switch {
		case len(parts) == 1:
			switch parts[0] {
			case "preview":
				if r.Method != http.MethodPost {
					writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
					return
				}
				preview(w, r)
			case "health":
				if r.Method != http.MethodGet {
					writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
					return
				}
				health(w, r)
			case "audit":
				if r.Method != http.MethodGet {
					writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
					return
				}
				auditLog(w, r)
			default:
				if err := backgroundjobs.ValidateJobID(parts[0]); err != nil {
					writeJSONError(w, http.StatusBadRequest, "invalid job id")
					return
				}
				switch r.Method {
				case http.MethodGet:
					detail(w, r)
				case http.MethodPatch:
					editH(w, r)
				case http.MethodDelete:
					deleteH(w, r)
				default:
					writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
				}
			}
		case len(parts) == 2 && parts[1] == "run":
			if err := backgroundjobs.ValidateJobID(parts[0]); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid job id")
				return
			}
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			runNow(w, r)
		case len(parts) == 2 && parts[1] == "pause":
			if err := backgroundjobs.ValidateJobID(parts[0]); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid job id")
				return
			}
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			pause(w, r)
		case len(parts) == 2 && parts[1] == "resume":
			if err := backgroundjobs.ValidateJobID(parts[0]); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid job id")
				return
			}
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			resume(w, r)
		case len(parts) == 2 && parts[1] == "runs":
			if err := backgroundjobs.ValidateJobID(parts[0]); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid job id")
				return
			}
			runs(w, r)
		case len(parts) == 3 && parts[1] == "runs":
			if err := backgroundjobs.ValidateJobID(parts[0]); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid job id")
				return
			}
			runDetail(w, r)
		case len(parts) == 4 && parts[1] == "runs" && parts[3] == "activity":
			if err := backgroundjobs.ValidateJobID(parts[0]); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid job id")
				return
			}
			if r.Method != http.MethodGet {
				writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			activityH(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}

// --- Path helpers ---

// extractJobID extracts the job ID from /api/jobs/{id}[/...].
func extractJobID(path string) string {
	rest := strings.TrimPrefix(path, "/api/jobs/")
	rest = strings.TrimSuffix(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}

// extractJobIDAndRunID extracts job ID and run ID from /api/jobs/{id}/runs/{rid}.
func extractJobIDAndRunID(path string) (jobID, runID string) {
	rest := strings.TrimPrefix(path, "/api/jobs/")
	rest = strings.TrimSuffix(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) >= 3 && parts[1] == "runs" {
		return parts[0], parts[2]
	}
	if len(parts) >= 2 && parts[1] == "runs" {
		return parts[0], ""
	}
	return "", ""
}

// buildScheduleSummary returns a human-readable schedule description.
// This is the source of truth; JS templates should use the server-provided
// schedule_summary field from the API response.
func buildScheduleSummary(s backgroundjobs.ScheduleSpec) string {
	tz := s.Timezone
	if tz == "" {
		tz = "UTC"
	}
	switch s.Kind {
	case backgroundjobs.ScheduleInterval:
		if s.Every != "" {
			return fmt.Sprintf("Every %s", s.Every)
		}
		return "Every hour"
	case backgroundjobs.ScheduleDaily:
		return fmt.Sprintf("Daily at %s %s", s.TimeOfDay, tz)
	case backgroundjobs.ScheduleWeekly:
		return fmt.Sprintf("Weekly on %s at %s %s", s.DayOfWeek, s.TimeOfDay, tz)
	case backgroundjobs.ScheduleCron:
		return fmt.Sprintf("Cron: %s (%s)", s.Cron, tz)
	default:
		return string(s.Kind)
	}
}
