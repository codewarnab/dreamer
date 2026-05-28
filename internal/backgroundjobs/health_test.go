package backgroundjobs

import (
	"context"
	"testing"
	"time"

	"dreamer/internal/logging"
)

func TestCheckHealth_AllHealthy(t *testing.T) {
	store := newTestStore(t)
	sched := newMockScheduler()
	lg := logging.Silent()

	// Add an enabled job with a healthy schedule.
	specHash, _ := HashScheduleSpec(ScheduleSpec{Kind: ScheduleInterval, Timezone: "UTC"})
	job := &Job{
		ID:        "healthy-job",
		Name:      "test",
		Enabled:   true,
		Schedule:  ScheduleSpec{Kind: ScheduleInterval, Timezone: "UTC"},
		OSSchedule: OSScheduleState{
			ScheduleID: "mock-healthy-job",
			InstallID:  "test-install-id",
			SpecHash:   specHash,
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.AddJob(job); err != nil {
		t.Fatal(err)
	}

	checker := &HealthChecker{Scheduler: sched, Store: store, Logger: lg}
	health, err := checker.CheckHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !health.SystemHealthy {
		t.Errorf("SystemHealthy = false, want true; issues: %v", health.Issues)
	}
	if health.TotalJobs != 1 {
		t.Errorf("TotalJobs = %d, want 1", health.TotalJobs)
	}
	if health.EnabledJobs != 1 {
		t.Errorf("EnabledJobs = %d, want 1", health.EnabledJobs)
	}
}

func TestCheckHealth_MissingSchedule(t *testing.T) {
	store := newTestStore(t)
	sched := newMockScheduler()
	lg := logging.Silent()

	// Add an enabled job with no OS schedule.
	job := &Job{
		ID:        "unscheduled-job",
		Name:      "test",
		Enabled:   true,
		Schedule:  ScheduleSpec{Kind: ScheduleInterval, Timezone: "UTC"},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.AddJob(job); err != nil {
		t.Fatal(err)
	}

	checker := &HealthChecker{Scheduler: sched, Store: store, Logger: lg}
	health, err := checker.CheckHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if health.SystemHealthy {
		t.Error("SystemHealthy = true, want false for unscheduled job")
	}

	found := false
	for _, issue := range health.Issues {
		if issue.JobID == "unscheduled-job" && issue.Severity == HealthSeverityWarning {
			found = true
		}
	}
	if !found {
		t.Error("expected warning about missing OS schedule")
	}
}

func TestCheckHealth_OrphanedSchedule(t *testing.T) {
	store := newTestStore(t)
	sched := newMockScheduler()
	sched.ownIDs = []string{"orphan-schedule"}
	lg := logging.Silent()

	checker := &HealthChecker{Scheduler: sched, Store: store, Logger: lg}
	health, err := checker.CheckHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if health.SystemHealthy {
		t.Error("SystemHealthy = true, want false for orphaned schedule")
	}

	found := false
	for _, issue := range health.Issues {
		if issue.JobID == "orphan-schedule" && issue.Severity == HealthSeverityWarning {
			found = true
		}
	}
	if !found {
		t.Error("expected warning about orphaned schedule")
	}
}

func TestCheckHealth_DisabledJobNoSchedule(t *testing.T) {
	store := newTestStore(t)
	sched := newMockScheduler()
	lg := logging.Silent()

	job := &Job{
		ID:        "disabled-job",
		Name:      "test",
		Enabled:   false,
		Schedule:  ScheduleSpec{Kind: ScheduleInterval, Timezone: "UTC"},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.AddJob(job); err != nil {
		t.Fatal(err)
	}

	checker := &HealthChecker{Scheduler: sched, Store: store, Logger: lg}
	health, err := checker.CheckHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Disabled job with no schedule is OK — no issues expected.
	if !health.SystemHealthy {
		t.Errorf("SystemHealthy = false, want true; issues: %v", health.Issues)
	}
}
