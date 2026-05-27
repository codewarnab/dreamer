package backgroundjobs

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStateJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	state := State{
		Version: 1,
		Jobs: map[string]*Job{
			"abcdef1234567890": {
				ID:          "abcdef1234567890",
				Name:        "test job",
				Prompt:      "do something",
				ProjectName: "myproject",
				ProjectPath: "/tmp/project",
				ProviderID:  "openclaude-cli",
				Model:       "mimo-v2.5-pro",
				Schedule: ScheduleSpec{
					Kind:            ScheduleDaily,
					TimeOfDay:       "09:00",
					Timezone:        "America/New_York",
					MissedRunPolicy: "skip",
					OverlapPolicy:   "skip",
				},
				Enabled:   true,
				CreatedAt: now,
				UpdatedAt: now,
				LastRunAt: &now,
				Permissions: PermissionProfile{
					FileAccess:    FileAccessReadOnly,
					ReadScope:     "project",
					ToolAccess:    ToolAccess{Mode: "none"},
				},
				Health: HealthState{
					SystemScheduling: "needs_install",
					JobSchedule:      "active",
					RunState:         "never_run",
					PermissionState:  "ok",
				},
			},
		},
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded State
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Version != 1 {
		t.Errorf("Version = %d, want 1", decoded.Version)
	}
	if len(decoded.Jobs) != 1 {
		t.Fatalf("Jobs count = %d, want 1", len(decoded.Jobs))
	}

	job := decoded.Jobs["abcdef1234567890"]
	if job.Name != "test job" {
		t.Errorf("Name = %q, want %q", job.Name, "test job")
	}
	if job.Schedule.Kind != ScheduleDaily {
		t.Errorf("Schedule.Kind = %q, want %q", job.Schedule.Kind, ScheduleDaily)
	}
	if job.Schedule.TimeOfDay != "09:00" {
		t.Errorf("Schedule.TimeOfDay = %q, want %q", job.Schedule.TimeOfDay, "09:00")
	}
	if job.Permissions.FileAccess != FileAccessReadOnly {
		t.Errorf("Permissions.FileAccess = %q, want %q", job.Permissions.FileAccess, FileAccessReadOnly)
	}
	if !job.LastRunAt.Equal(now) {
		t.Errorf("LastRunAt = %v, want %v", job.LastRunAt, now)
	}
	if job.Health.RunState != "never_run" {
		t.Errorf("Health.RunState = %q, want %q", job.Health.RunState, "never_run")
	}
}

func TestRunJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	finish := now.Add(5 * time.Minute)
	run := Run{
		ID:             "run-abc",
		JobID:          "job-123",
		ScheduledFor:   now,
		Status:         RunStatusCompleted,
		StartedAt:      now,
		FinishedAt:     &finish,
		DurationMillis: 300000,
		ProviderID:     "openclaude-cli",
		Model:          "mimo-v2.5-pro",
		PromptSnapshot: "do something",
		OutputSummary:  "done",
		LogPath:        "/tmp/logs/run-abc.log",
		TouchedPaths:   []string{"/tmp/reports/output.md"},
	}

	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Run
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != "run-abc" {
		t.Errorf("ID = %q, want %q", decoded.ID, "run-abc")
	}
	if decoded.Status != RunStatusCompleted {
		t.Errorf("Status = %q, want %q", decoded.Status, RunStatusCompleted)
	}
	if decoded.DurationMillis != 300000 {
		t.Errorf("DurationMillis = %d, want 300000", decoded.DurationMillis)
	}
	if len(decoded.TouchedPaths) != 1 || decoded.TouchedPaths[0] != "/tmp/reports/output.md" {
		t.Errorf("TouchedPaths = %v, want [/tmp/reports/output.md]", decoded.TouchedPaths)
	}
	if !decoded.FinishedAt.Equal(finish) {
		t.Errorf("FinishedAt = %v, want %v", decoded.FinishedAt, finish)
	}
}

func TestRunStatusTerminal(t *testing.T) {
	terminal := map[RunStatus]bool{
		RunStatusCompleted: true,
		RunStatusFailed:    true,
		RunStatusTimedOut:  true,
		RunStatusCancelled: true,
		RunStatusSkipped:   true,
	}
	// Running is not terminal.
	if terminal[RunStatusRunning] {
		t.Error("RunStatusRunning should not be terminal")
	}
	// Every non-running status should be terminal.
	allStatuses := []RunStatus{
		RunStatusRunning, RunStatusCompleted, RunStatusFailed,
		RunStatusTimedOut, RunStatusCancelled, RunStatusSkipped,
	}
	for _, s := range allStatuses {
		if s == RunStatusRunning {
			continue
		}
		if !terminal[s] {
			t.Errorf("RunStatus %q should be terminal", s)
		}
	}
}

func TestScheduleKindValues(t *testing.T) {
	kinds := []ScheduleKind{ScheduleHourly, ScheduleDaily, ScheduleWeekly, ScheduleCron}
	seen := map[ScheduleKind]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("duplicate ScheduleKind value: %q", k)
		}
		seen[k] = true
	}
}

func TestFileAccessModeValues(t *testing.T) {
	modes := []FileAccessMode{FileAccessReadOnly, FileAccessSelectedWrites, FileAccessFullWorkspace}
	seen := map[FileAccessMode]bool{}
	for _, m := range modes {
		if seen[m] {
			t.Errorf("duplicate FileAccessMode value: %q", m)
		}
		seen[m] = true
	}
}
