// Package backgroundjobs implements recurring natural-language tasks that run
// via OS scheduling (Task Scheduler, LaunchAgent, systemd). Phase 0 provides
// the shared data model, cross-process store, schedule validation, permission
// checks, and audit logging.
package backgroundjobs

import "time"

// Job is the persisted definition of a background job.
type Job struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Prompt      string       `json:"prompt"`
	ProjectName string       `json:"project_name"`
	ProjectPath string       `json:"project_path"`
	ProviderID  string       `json:"provider_id"`
	Model       string       `json:"model,omitempty"`
	Schedule    ScheduleSpec `json:"schedule"`
	Enabled     bool         `json:"enabled"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	LastRunAt   *time.Time   `json:"last_run_at,omitempty"`
	NextRunAt   *time.Time   `json:"next_run_at,omitempty"`

	Permissions PermissionProfile `json:"permissions"`
	OSSchedule  OSScheduleState   `json:"os_schedule"`
	Health      HealthState       `json:"health"`
}

// ScheduleKind identifies the type of schedule.
type ScheduleKind string

const (
	ScheduleHourly ScheduleKind = "hourly"
	ScheduleDaily  ScheduleKind = "daily"
	ScheduleWeekly ScheduleKind = "weekly"
	ScheduleCron   ScheduleKind = "cron"
)

// ScheduleSpec describes when a job should run.
type ScheduleSpec struct {
	Kind            ScheduleKind `json:"kind"`
	Every           string       `json:"every,omitempty"`
	TimeOfDay       string       `json:"time_of_day,omitempty"`
	DayOfWeek       string       `json:"day_of_week,omitempty"`
	Cron            string       `json:"cron,omitempty"`
	Timezone        string       `json:"timezone"`
	MissedRunPolicy string       `json:"missed_run_policy"`
	OverlapPolicy   string       `json:"overlap_policy"`
	MisfireWindow   string       `json:"misfire_window"`
}

// RunStatus is the lifecycle state of a single run.
type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
	RunStatusTimedOut  RunStatus = "timed_out"
	RunStatusCancelled RunStatus = "cancelled"
	RunStatusSkipped   RunStatus = "skipped"
)

// Run records one execution of a background job.
type Run struct {
	ID             string     `json:"id"`
	JobID          string     `json:"job_id"`
	ScheduledFor   time.Time  `json:"scheduled_for"`
	Status         RunStatus  `json:"status"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	DurationMillis int64      `json:"duration_millis,omitempty"`
	ProviderID     string     `json:"provider_id"`
	Model          string     `json:"model,omitempty"`
	PromptSnapshot string     `json:"prompt_snapshot"`
	Error          string     `json:"error,omitempty"`
	OutputSummary  string     `json:"output_summary,omitempty"`
	LogPath        string     `json:"log_path"`
	TouchedPaths   []string   `json:"touched_paths,omitempty"`
	SkippedReason  string     `json:"skipped_reason,omitempty"`
}

// FileAccessMode controls what the job may write.
type FileAccessMode string

const (
	FileAccessReadOnly       FileAccessMode = "read_only"
	FileAccessSelectedWrites FileAccessMode = "selected_writes"
	FileAccessFullWorkspace  FileAccessMode = "full_workspace"
)

// PermissionProfile declares the security boundary for a background job.
type PermissionProfile struct {
	FileAccess              FileAccessMode `json:"file_access"`
	WritablePaths           []string       `json:"writable_paths,omitempty"`
	ReadScope               string         `json:"read_scope"`
	ProviderNetworkRequired bool           `json:"provider_network_required"`
	ToolNetworkRequested    bool           `json:"tool_network_requested"`
	ToolNetworkEffective    bool           `json:"tool_network_effective"`
	NetworkReason           string         `json:"network_reason,omitempty"`
	ToolAccess              ToolAccess     `json:"tool_access"`
}

// ToolAccess controls shell and MCP tool access.
type ToolAccess struct {
	Mode          string   `json:"mode"`
	ShellCommands []string `json:"shell_commands,omitempty"`
	MCPTools      []string `json:"mcp_tools,omitempty"`
}

// OSScheduleState tracks the OS-level schedule artifact for a job.
type OSScheduleState struct {
	ScheduleID    string     `json:"schedule_id,omitempty"`
	InstallID     string     `json:"install_id,omitempty"`
	ConfigPathHash string    `json:"config_path_hash,omitempty"`
	SpecHash      string     `json:"spec_hash,omitempty"`
	LastInstalled *time.Time `json:"last_installed,omitempty"`
}

// HealthState carries per-job health indicators.
type HealthState struct {
	SystemScheduling string     `json:"system_scheduling"`
	JobSchedule      string     `json:"job_schedule"`
	RunState         string     `json:"run_state"`
	PermissionState  string     `json:"permission_state"`
	LastChecked      *time.Time `json:"last_checked,omitempty"`
}

// State is the top-level persisted structure for background jobs.
type State struct {
	Jobs map[string]*Job `json:"jobs"`
	// Version for future migrations.
	Version int `json:"version"`
}
