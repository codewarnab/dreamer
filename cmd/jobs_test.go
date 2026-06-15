package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/logging"
)

// writeJobsConfig writes a minimal config with output_root set.
func writeJobsConfig(t *testing.T, outputRoot string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := fmt.Sprintf("projects: []\ndaemon:\n  output_root: %q\n", outputRoot)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// createTestJob inserts a job into the store for CLI testing.
func createTestJob(t *testing.T, outputRoot string, jobID string) {
	t.Helper()
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	job := &backgroundjobs.Job{
		ID:          jobID,
		Name:        "test-job",
		Prompt:      "analyze this",
		ProjectPath: t.TempDir(),
		ProviderID:  "openclaude-cli",
		Schedule: backgroundjobs.ScheduleSpec{
			Kind:      backgroundjobs.ScheduleDaily,
			TimeOfDay: "09:00",
			Timezone:  "UTC",
		},
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Permissions: backgroundjobs.PermissionProfile{
			FileAccess: backgroundjobs.FileAccessReadOnly,
		},
	}
	if err := store.AddJob(job); err != nil {
		t.Fatalf("add test job: %v", err)
	}
}

func TestNewJobsCommand_HasSubcommands(t *testing.T) {
	cmd := newJobsCommand()
	if cmd.Use != "jobs" {
		t.Errorf("Use = %q, want %q", cmd.Use, "jobs")
	}
	if cmd.GroupID != "" {
		t.Errorf("GroupID should be empty on constructor, got %q", cmd.GroupID)
	}
	// Verify subcommands are registered.
	subcommands := []string{"list", "create", "show", "pause", "resume", "delete", "run", "runs", "logs"}
	for _, name := range subcommands {
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestJobsCommand_Help(t *testing.T) {
	stdout, _, err := executeRootCommand("jobs", "--help")
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(stdout, "background") {
		t.Errorf("help output missing description: %s", stdout)
	}
}

func TestJobsList_NoJobs(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	stdout, _, err := executeRootCommand("jobs", "list", "--config", cfgPath)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stdout, "No jobs") {
		t.Errorf("expected 'No jobs' message, got: %s", stdout)
	}
}

func TestJobsList_WithJobs(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	createTestJob(t, outputRoot, "abc1234567890001")

	stdout, _, err := executeRootCommand("jobs", "list", "--config", cfgPath)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stdout, "abc1234567890001") {
		t.Errorf("expected job ID in output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "test-job") {
		t.Errorf("expected job name in output, got: %s", stdout)
	}
}

func TestJobsList_JSON(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	createTestJob(t, outputRoot, "abc1234567890001")

	stdout, _, err := executeRootCommand("jobs", "list", "--config", cfgPath, "--json")
	if err != nil {
		t.Fatalf("list --json: %v", err)
	}
	if !strings.Contains(stdout, `"id"`) {
		t.Errorf("expected JSON output, got: %s", stdout)
	}
}

func TestJobsShow(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	createTestJob(t, outputRoot, "abc1234567890001")

	stdout, _, err := executeRootCommand("jobs", "show", "abc1234567890001", "--config", cfgPath)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(stdout, "abc1234567890001") {
		t.Errorf("expected job ID, got: %s", stdout)
	}
	if !strings.Contains(stdout, "analyze this") {
		t.Errorf("expected prompt in output, got: %s", stdout)
	}
}

func TestJobsShow_NotFound(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	_, _, err := executeRootCommand("jobs", "show", "abc1234567890001", "--config", cfgPath)
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
}

func TestJobsPause_Resume(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	createTestJob(t, outputRoot, "abc1234567890001")

	// Pause.
	stdout, _, err := executeRootCommand("jobs", "pause", "abc1234567890001", "--config", cfgPath)
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if !strings.Contains(stdout, "paused") {
		t.Errorf("expected 'paused' message, got: %s", stdout)
	}

	// Verify paused.
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	state, _ := store.Load()
	if state.Jobs["abc1234567890001"].Enabled {
		t.Error("job should be disabled after pause")
	}

	// Resume.
	stdout, _, err = executeRootCommand("jobs", "resume", "abc1234567890001", "--config", cfgPath)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(stdout, "resumed") {
		t.Errorf("expected 'resumed' message, got: %s", stdout)
	}

	state, _ = store.Load()
	if !state.Jobs["abc1234567890001"].Enabled {
		t.Error("job should be enabled after resume")
	}
}

func TestJobsPause_Resume_RecomputesNextRunAt(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	createTestJob(t, outputRoot, "abc1234567890001")

	// Set a stale NextRunAt in the past.
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	staleTime := time.Now().UTC().Add(-2 * time.Hour)
	_ = store.Update(context.Background(), func(s *backgroundjobs.State) error {
		job := s.Jobs["abc1234567890001"]
		job.NextRunAt = &staleTime
		return nil
	})

	// Pause then resume.
	executeRootCommand("jobs", "pause", "abc1234567890001", "--config", cfgPath)
	executeRootCommand("jobs", "resume", "abc1234567890001", "--config", cfgPath)

	state, _ := store.Load()
	job := state.Jobs["abc1234567890001"]
	if job.NextRunAt == nil {
		t.Fatal("NextRunAt should be set after resume")
	}
	if !job.NextRunAt.After(staleTime) {
		t.Errorf("NextRunAt should be updated; stale=%v, got=%v", staleTime, *job.NextRunAt)
	}
	if job.NextRunAt.Before(time.Now().UTC()) {
		t.Errorf("NextRunAt should be in the future; got=%v", *job.NextRunAt)
	}
}

func TestJobsEdit_PreservesRunnerOwnedFields(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	jobID := "abc1234567890001"
	createTestJob(t, outputRoot, jobID)

	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	lastRunAt := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	nextRunAt := time.Date(2025, 3, 2, 10, 0, 0, 0, time.UTC)
	lastChecked := time.Date(2025, 3, 1, 10, 30, 0, 0, time.UTC)
	if err := store.Update(context.Background(), func(s *backgroundjobs.State) error {
		job := s.Jobs[jobID]
		job.LastRunAt = &lastRunAt
		job.NextRunAt = &nextRunAt
		job.Health = backgroundjobs.HealthState{
			SystemScheduling: backgroundjobs.SchedulingValid,
			JobSchedule:      backgroundjobs.JobScheduleValid,
			RunState:         backgroundjobs.RunStatusCompleted,
			PermissionState:  backgroundjobs.PermissionAllowed,
			LastChecked:      &lastChecked,
		}
		return nil
	}); err != nil {
		t.Fatalf("seed runner fields: %v", err)
	}

	stdout, _, err := executeRootCommand("jobs", "edit", jobID, "--config", cfgPath, "--name", "renamed-job")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(stdout, "job updated") {
		t.Errorf("expected update message, got: %s", stdout)
	}

	state, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	job := state.Jobs[jobID]
	if job.Name != "renamed-job" {
		t.Fatalf("Name = %q, want renamed-job", job.Name)
	}
	if job.LastRunAt == nil || !job.LastRunAt.Equal(lastRunAt) {
		t.Fatalf("LastRunAt = %v, want %v", job.LastRunAt, lastRunAt)
	}
	if job.NextRunAt == nil || !job.NextRunAt.Equal(nextRunAt) {
		t.Fatalf("NextRunAt = %v, want %v", job.NextRunAt, nextRunAt)
	}
	if job.Health.SystemScheduling != backgroundjobs.SchedulingValid {
		t.Errorf("SystemScheduling = %q, want %q", job.Health.SystemScheduling, backgroundjobs.SchedulingValid)
	}
	if job.Health.JobSchedule != backgroundjobs.JobScheduleValid {
		t.Errorf("JobSchedule = %q, want %q", job.Health.JobSchedule, backgroundjobs.JobScheduleValid)
	}
	if job.Health.RunState != backgroundjobs.RunStatusCompleted {
		t.Errorf("RunState = %q, want %q", job.Health.RunState, backgroundjobs.RunStatusCompleted)
	}
	if job.Health.PermissionState != backgroundjobs.PermissionAllowed {
		t.Errorf("PermissionState = %q, want %q", job.Health.PermissionState, backgroundjobs.PermissionAllowed)
	}
	if job.Health.LastChecked == nil || !job.Health.LastChecked.Equal(lastChecked) {
		t.Fatalf("LastChecked = %v, want %v", job.Health.LastChecked, lastChecked)
	}
}

func TestJobsPause_HasOutputRootFlag(t *testing.T) {
	cmd := newJobsPauseCommand()
	if cmd.Flag("output-root") == nil {
		t.Error("pause command should have --output-root flag")
	}
}

func TestJobsResume_HasOutputRootFlag(t *testing.T) {
	cmd := newJobsResumeCommand()
	if cmd.Flag("output-root") == nil {
		t.Error("resume command should have --output-root flag")
	}
}

func TestJobsDelete_ConfirmRequired(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	createTestJob(t, outputRoot, "abc1234567890001")

	// Without --yes → confirmation message.
	stdout, _, err := executeRootCommand("jobs", "delete", "abc1234567890001", "--config", cfgPath)
	if err != nil {
		t.Fatalf("delete without confirm: %v", err)
	}
	if !strings.Contains(stdout, "--yes") {
		t.Errorf("expected confirmation prompt, got: %s", stdout)
	}

	// With --yes → deletes.
	stdout, _, err = executeRootCommand("jobs", "delete", "abc1234567890001", "--config", cfgPath, "--yes")
	if err != nil {
		t.Fatalf("delete with confirm: %v", err)
	}
	if !strings.Contains(stdout, "deleted") {
		t.Errorf("expected 'deleted' message, got: %s", stdout)
	}
}

func TestJobsCreate_DryRun(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	projectDir := t.TempDir()

	stdout, _, err := executeRootCommand("jobs", "create", projectDir,
		"--config", cfgPath,
		"--prompt", "analyze this codebase",
		"--name", "my-job",
		"--provider", "openclaude-cli",
		"--schedule", "daily",
		"--time-of-day", "09:00",
		"--dry-run")
	if err != nil {
		t.Fatalf("create --dry-run: %v", err)
	}
	if !strings.Contains(stdout, "my-job") {
		t.Errorf("expected job name in dry-run output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "analyze this codebase") {
		t.Errorf("expected prompt in dry-run output, got: %s", stdout)
	}
}

func TestJobsRun_NotFound(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	_, _, err := executeRootCommand("jobs", "run", "abc1234567890001", "--config", cfgPath, "--force")
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
}

func TestJobsRun_DisabledJob(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	job := &backgroundjobs.Job{
		ID:          "abc1234567890001",
		Name:        "disabled-job",
		Prompt:      "test",
		ProjectPath: t.TempDir(),
		ProviderID:  "openclaude-cli",
		Schedule: backgroundjobs.ScheduleSpec{
			Kind:      backgroundjobs.ScheduleDaily,
			TimeOfDay: "09:00",
			Timezone:  "UTC",
		},
		Enabled:   false,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Permissions: backgroundjobs.PermissionProfile{
			FileAccess: backgroundjobs.FileAccessReadOnly,
		},
	}
	store.AddJob(job)

	stdout, _, err := executeRootCommand("jobs", "run", "abc1234567890001", "--config", cfgPath, "--force")
	if err != nil {
		t.Fatalf("run disabled: %v", err)
	}
	if !strings.Contains(stdout, "skipped") {
		t.Errorf("expected 'skipped' message, got: %s", stdout)
	}
}

// --- Wizard draft persistence tests ---

func TestWizardDraftPath(t *testing.T) {
	got := wizardDraftPath("/some/root")
	want := filepath.Join("/some/root", "background-jobs", ".wizard-draft.json")
	if got != want {
		t.Errorf("wizardDraftPath = %q, want %q", got, want)
	}
}

func TestLoadWizardDraft_MissingFile(t *testing.T) {
	ans, err := loadWizardDraft(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if (ans != jobWizardAnswers{}) {
		t.Errorf("expected zero-value answers, got %+v", ans)
	}
}

func TestLoadWizardDraft_CorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.json")
	os.WriteFile(path, []byte("{bad json"), 0o644)

	ans, err := loadWizardDraft(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if (ans != jobWizardAnswers{}) {
		t.Errorf("expected zero-value answers on corrupt file, got %+v", ans)
	}
}

func TestLoadWizardDraft_WrongVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.json")
	os.WriteFile(path, []byte(`{"version":99,"answers":{"prompt":"hi"}}`), 0o644)

	ans, err := loadWizardDraft(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ans.prompt != "" {
		t.Errorf("expected empty prompt for wrong version, got %q", ans.prompt)
	}
}

func TestSaveAndLoadWizardDraft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", ".wizard-draft.json")

	saved := jobWizardAnswers{
		name:         "my-job",
		prompt:       "analyze code",
		providerID:   "openclaude-cli",
		scheduleKind: "daily",
		timeOfDay:    "09:00",
		timezone:     "Asia/Kolkata",
	}

	if err := saveWizardDraft(path, saved); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := loadWizardDraft(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.name != saved.name {
		t.Errorf("name = %q, want %q", loaded.name, saved.name)
	}
	if loaded.prompt != saved.prompt {
		t.Errorf("prompt = %q, want %q", loaded.prompt, saved.prompt)
	}
	if loaded.providerID != saved.providerID {
		t.Errorf("providerID = %q, want %q", loaded.providerID, saved.providerID)
	}
	if loaded.scheduleKind != saved.scheduleKind {
		t.Errorf("scheduleKind = %q, want %q", loaded.scheduleKind, saved.scheduleKind)
	}
	if loaded.timezone != saved.timezone {
		t.Errorf("timezone = %q, want %q", loaded.timezone, saved.timezone)
	}
}

func TestDeleteWizardDraft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "draft.json")
	os.WriteFile(path, []byte(`{"version":1,"answers":{}}`), 0o644)

	if err := deleteWizardDraft(path); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be deleted, stat err = %v", err)
	}
}

func TestDeleteWizardDraft_Missing(t *testing.T) {
	// Should not error on missing file.
	if err := deleteWizardDraft(filepath.Join(t.TempDir(), "nope.json")); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestMergeWizardDraft_CLIOverridesDraft(t *testing.T) {
	draft := jobWizardAnswers{
		name:         "draft-job",
		prompt:       "draft prompt",
		providerID:   "openclaude-cli",
		scheduleKind: "daily",
		timeOfDay:    "09:00",
		timezone:     "UTC",
	}
	cli := jobWizardAnswers{
		prompt: "cli prompt",
	}

	merged := mergeWizardDraft(draft, cli)

	if merged.prompt != "cli prompt" {
		t.Errorf("prompt = %q, want %q", merged.prompt, "cli prompt")
	}
	if merged.name != "draft-job" {
		t.Errorf("name = %q, want %q", merged.name, "draft-job")
	}
	if merged.scheduleKind != "daily" {
		t.Errorf("scheduleKind = %q, want %q", merged.scheduleKind, "daily")
	}
}

func TestMergeWizardDraft_PathAlwaysFromCLI(t *testing.T) {
	draft := jobWizardAnswers{
		projectPath: "/old/path",
		name:        "job",
	}
	cli := jobWizardAnswers{
		projectPath: "/new/path",
	}

	merged := mergeWizardDraft(draft, cli)
	if merged.projectPath != "/new/path" {
		t.Errorf("projectPath = %q, want %q", merged.projectPath, "/new/path")
	}

	// When CLI has no path, draft path is cleared.
	merged2 := mergeWizardDraft(draft, jobWizardAnswers{})
	if merged2.projectPath != "" {
		t.Errorf("projectPath should be empty when CLI has none, got %q", merged2.projectPath)
	}
}

func TestJobsRun_WriteModeRejected(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	job := &backgroundjobs.Job{
		ID:          "abc1234567890001",
		Name:        "write-job",
		Prompt:      "test",
		ProjectPath: t.TempDir(),
		ProviderID:  "openclaude-cli",
		Schedule: backgroundjobs.ScheduleSpec{
			Kind:      backgroundjobs.ScheduleDaily,
			TimeOfDay: "09:00",
			Timezone:  "UTC",
		},
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Permissions: backgroundjobs.PermissionProfile{
			FileAccess: backgroundjobs.FileAccessSelectedWrites,
		},
	}
	store.AddJob(job)

	_, _, err := executeRootCommand("jobs", "run", "abc1234567890001", "--config", cfgPath, "--force")
	if err == nil {
		t.Fatal("expected error for write mode")
	}
}

// --- Phase 4: jobs runs / jobs logs / --dry-run / --output-file ---

func createTestRun(t *testing.T, outputRoot, jobID, runID string, status backgroundjobs.RunStatus) {
	t.Helper()
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	runStore := backgroundjobs.NewRunStore(store.Dir(), lg)
	run := backgroundjobs.Run{
		ID:             runID,
		JobID:          jobID,
		ScheduledFor:   time.Now().UTC(),
		Status:         status,
		StartedAt:      time.Now().UTC().Add(-5 * time.Second),
		DurationMillis: 5000,
		ProviderID:     "openclaude-cli",
		PromptSnapshot: "test prompt",
	}
	if status == backgroundjobs.RunStatusFailed {
		run.Error = "something went wrong"
	}
	if status == backgroundjobs.RunStatusCompleted {
		run.OutputSummary = "all good"
	}
	if err := runStore.Append(run); err != nil {
		t.Fatalf("append run: %v", err)
	}
}

func TestJobsRuns_NoRuns(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")

	stdout, _, err := executeRootCommand("jobs", "runs", "abc1234567890001", "--config", cfgPath)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if !strings.Contains(stdout, "No runs") {
		t.Errorf("expected 'No runs' message, got: %s", stdout)
	}
}

func TestJobsRuns_WithRuns(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")
	createTestRun(t, outputRoot, "abc1234567890001", "run-001", backgroundjobs.RunStatusCompleted)

	stdout, _, err := executeRootCommand("jobs", "runs", "abc1234567890001", "--config", cfgPath)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if !strings.Contains(stdout, "run-001") {
		t.Errorf("expected run ID in output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "completed") {
		t.Errorf("expected 'completed' in output, got: %s", stdout)
	}
}

func TestJobsRuns_JSON(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")
	createTestRun(t, outputRoot, "abc1234567890001", "run-001", backgroundjobs.RunStatusCompleted)

	stdout, _, err := executeRootCommand("jobs", "runs", "abc1234567890001", "--config", cfgPath, "--json")
	if err != nil {
		t.Fatalf("runs --json: %v", err)
	}
	if !strings.Contains(stdout, `"id"`) {
		t.Errorf("expected JSON output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "run-001") {
		t.Errorf("expected run ID in JSON, got: %s", stdout)
	}
}

func TestJobsRuns_StatusFilter(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")
	createTestRun(t, outputRoot, "abc1234567890001", "run-ok", backgroundjobs.RunStatusCompleted)
	createTestRun(t, outputRoot, "abc1234567890001", "run-fail", backgroundjobs.RunStatusFailed)

	stdout, _, err := executeRootCommand("jobs", "runs", "abc1234567890001", "--config", cfgPath, "--status", "failed")
	if err != nil {
		t.Fatalf("runs --status failed: %v", err)
	}
	if !strings.Contains(stdout, "run-fail") {
		t.Errorf("expected failed run in output, got: %s", stdout)
	}
	if strings.Contains(stdout, "run-ok") {
		t.Errorf("should not contain completed run when filtering by failed, got: %s", stdout)
	}
}

func TestJobsRuns_Limit(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")
	createTestRun(t, outputRoot, "abc1234567890001", "run-001", backgroundjobs.RunStatusCompleted)
	createTestRun(t, outputRoot, "abc1234567890001", "run-002", backgroundjobs.RunStatusCompleted)
	createTestRun(t, outputRoot, "abc1234567890001", "run-003", backgroundjobs.RunStatusCompleted)

	stdout, _, err := executeRootCommand("jobs", "runs", "abc1234567890001", "--config", cfgPath, "--limit", "2")
	if err != nil {
		t.Fatalf("runs --limit: %v", err)
	}
	// All runs have the same timestamp, so sort is by insertion order (most recent first).
	// run-003 and run-002 should appear; run-001 should be excluded by --limit 2.
	if !strings.Contains(stdout, "run-002") {
		t.Errorf("expected run-002 in output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "run-003") {
		t.Errorf("expected run-003 in output, got: %s", stdout)
	}
	if strings.Contains(stdout, "run-001") {
		t.Errorf("run-001 should be excluded by --limit 2, got: %s", stdout)
	}
}

func TestJobsRuns_NotFoundJob(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	_, _, err := executeRootCommand("jobs", "runs", "nonexistent", "--config", cfgPath)
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
}

func TestJobsLogs_NoRuns(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")

	stdout, _, err := executeRootCommand("jobs", "logs", "abc1234567890001", "--config", cfgPath)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(stdout, "No runs") {
		t.Errorf("expected 'No runs' message, got: %s", stdout)
	}
}

func TestJobsLogs_Fallback(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")
	createTestRun(t, outputRoot, "abc1234567890001", "run-001", backgroundjobs.RunStatusCompleted)

	stdout, _, err := executeRootCommand("jobs", "logs", "abc1234567890001", "--config", cfgPath)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	// Should fall back to OutputSummary since no log file exists.
	if !strings.Contains(stdout, "all good") {
		t.Errorf("expected output summary in fallback, got: %s", stdout)
	}
}

func TestJobsLogs_WithLogFile(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")

	// Create a run with a log file.
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	runStore := backgroundjobs.NewRunStore(store.Dir(), lg)
	logDir := runStore.LogDir("abc1234567890001")
	os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "run-001.log")
	os.WriteFile(logPath, []byte("full provider output here"), 0o644)

	run := backgroundjobs.Run{
		ID:             "run-001",
		JobID:          "abc1234567890001",
		ScheduledFor:   time.Now().UTC(),
		Status:         backgroundjobs.RunStatusCompleted,
		StartedAt:      time.Now().UTC().Add(-5 * time.Second),
		DurationMillis: 5000,
		ProviderID:     "openclaude-cli",
		OutputSummary:  "truncated",
		LogPath:        logPath,
	}
	runStore.Append(run)

	stdout, _, err := executeRootCommand("jobs", "logs", "abc1234567890001", "--config", cfgPath)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(stdout, "full provider output here") {
		t.Errorf("expected full log content, got: %s", stdout)
	}
}

func TestJobsLogs_SpecificRun(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")
	createTestRun(t, outputRoot, "abc1234567890001", "run-001", backgroundjobs.RunStatusCompleted)
	createTestRun(t, outputRoot, "abc1234567890001", "run-002", backgroundjobs.RunStatusFailed)

	stdout, _, err := executeRootCommand("jobs", "logs", "abc1234567890001", "--config", cfgPath, "--run", "run-002")
	if err != nil {
		t.Fatalf("logs --run: %v", err)
	}
	if !strings.Contains(stdout, "something went wrong") {
		t.Errorf("expected error from run-002, got: %s", stdout)
	}
}

func TestJobsLogs_NotFoundRun(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")
	createTestRun(t, outputRoot, "abc1234567890001", "run-001", backgroundjobs.RunStatusCompleted)

	_, _, err := executeRootCommand("jobs", "logs", "abc1234567890001", "--config", cfgPath, "--run", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent run")
	}
}

func TestJobsRun_DryRun(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")

	stdout, _, err := executeRootCommand("jobs", "run", "abc1234567890001", "--config", cfgPath, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(stdout, "test-job") {
		t.Errorf("expected job name in dry-run output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "openclaude-cli") {
		t.Errorf("expected provider in dry-run output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "daily") {
		t.Errorf("expected schedule kind in dry-run output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Timeout:") {
		t.Errorf("expected timeout line in dry-run output, got: %s", stdout)
	}
}

func TestJobsRun_DryRun_DisabledJob(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)

	// Create a disabled job.
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	store.AddJob(&backgroundjobs.Job{
		ID:          "abc1234567890001",
		Name:        "disabled-job",
		Prompt:      "test",
		ProjectPath: t.TempDir(),
		ProviderID:  "openclaude-cli",
		Schedule:    backgroundjobs.ScheduleSpec{Kind: backgroundjobs.ScheduleDaily, TimeOfDay: "09:00", Timezone: "UTC"},
		Enabled:     false,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		Permissions: backgroundjobs.PermissionProfile{FileAccess: backgroundjobs.FileAccessReadOnly},
	})

	// --dry-run should work even on disabled jobs.
	stdout, _, err := executeRootCommand("jobs", "run", "abc1234567890001", "--config", cfgPath, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run disabled: %v", err)
	}
	if !strings.Contains(stdout, "disabled-job") {
		t.Errorf("expected job name in dry-run output, got: %s", stdout)
	}
}

func TestJobsRun_OutputFile(t *testing.T) {
	// --output-file is accepted alongside --dry-run (no provider needed).
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")

	outputFile := filepath.Join(t.TempDir(), "output.txt")

	stdout, _, err := executeRootCommand("jobs", "run", "abc1234567890001",
		"--config", cfgPath, "--dry-run", "--output-file", outputFile)
	if err != nil {
		t.Fatalf("run --dry-run --output-file: %v", err)
	}
	// --dry-run prints job info; --output-file is accepted (no effect in dry-run).
	if !strings.Contains(stdout, "test-job") {
		t.Errorf("expected job name in dry-run output, got: %s", stdout)
	}
}

func TestJobsRuns_HasFlags(t *testing.T) {
	cmd := newJobsRunsCommand()
	if cmd.Flag("json") == nil {
		t.Error("runs command should have --json flag")
	}
	if cmd.Flag("limit") == nil {
		t.Error("runs command should have --limit flag")
	}
	if cmd.Flag("status") == nil {
		t.Error("runs command should have --status flag")
	}
}

func TestJobsLogs_HasFlags(t *testing.T) {
	cmd := newJobsLogsCommand()
	if cmd.Flag("run") == nil {
		t.Error("logs command should have --run flag")
	}
	if cmd.Flag("tail") == nil {
		t.Error("logs command should have --tail flag")
	}
	if cmd.Flag("follow") == nil {
		t.Error("logs command should have --follow flag")
	}
}

func TestJobsRun_HasNewFlags(t *testing.T) {
	cmd := newJobsRunCommand()
	if cmd.Flag("output-file") == nil {
		t.Error("run command should have --output-file flag")
	}
	if cmd.Flag("dry-run") == nil {
		t.Error("run command should have --dry-run flag")
	}
}

func TestJobsLogs_Tail(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	outputRoot := t.TempDir()
	cfgPath := writeJobsConfig(t, outputRoot)
	createTestJob(t, outputRoot, "abc1234567890001")

	// Create a run with a multi-line log file.
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	runStore := backgroundjobs.NewRunStore(store.Dir(), lg)
	logDir := runStore.LogDir("abc1234567890001")
	os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "run-tail.log")
	os.WriteFile(logPath, []byte("line1\nline2\nline3\nline4\nline5"), 0o644)

	run := backgroundjobs.Run{
		ID:        "run-tail",
		JobID:     "abc1234567890001",
		Status:    backgroundjobs.RunStatusCompleted,
		StartedAt: time.Now().UTC(),
		LogPath:   logPath,
	}
	runStore.Append(run)

	stdout, _, err := executeRootCommand("jobs", "logs", "abc1234567890001", "--config", cfgPath, "--tail", "2")
	if err != nil {
		t.Fatalf("logs --tail: %v", err)
	}
	if !strings.Contains(stdout, "line4") {
		t.Errorf("expected line4, got: %s", stdout)
	}
	if !strings.Contains(stdout, "line5") {
		t.Errorf("expected line5, got: %s", stdout)
	}
	if strings.Contains(stdout, "line1") {
		t.Errorf("line1 should be excluded by --tail 2, got: %s", stdout)
	}
}

func TestJobsRuns_MissingArg(t *testing.T) {
	stdout, _, err := executeRootCommand("jobs", "runs")
	if err == nil {
		t.Fatal("expected error for missing job-id")
	}
	if !strings.Contains(err.Error(), "job-id") {
		t.Errorf("expected error mentioning job-id, got: %s", err)
	}
	_ = stdout
}

func TestJobsLogs_MissingArg(t *testing.T) {
	stdout, _, err := executeRootCommand("jobs", "logs")
	if err == nil {
		t.Fatal("expected error for missing job-id")
	}
	if !strings.Contains(err.Error(), "job-id") {
		t.Errorf("expected error mentioning job-id, got: %s", err)
	}
	_ = stdout
}

func TestWaitForRunCompletion_TransitionsToCompleted(t *testing.T) {
	outputRoot := t.TempDir()
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	runStore := backgroundjobs.NewRunStore(store.Dir(), lg)

	// Insert a running run.
	running := backgroundjobs.Run{
		ID:        "run-follow-1",
		JobID:     "job-1",
		Status:    backgroundjobs.RunStatusRunning,
		StartedAt: time.Now().UTC(),
	}
	if err := runStore.Append(running); err != nil {
		t.Fatalf("append running run: %v", err)
	}

	// After 100ms, append a completed run with the same ID.
	go func() {
		time.Sleep(100 * time.Millisecond)
		completed := backgroundjobs.Run{
			ID:         "run-follow-1",
			JobID:      "job-1",
			Status:     backgroundjobs.RunStatusCompleted,
			StartedAt:  running.StartedAt,
			FinishedAt: func() *time.Time { v := time.Now().UTC(); return &v }(),
		}
		runStore.Append(completed)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := waitForRunCompletion(ctx, runStore, "job-1", "run-follow-1")
	if err != nil {
		t.Fatalf("waitForRunCompletion: %v", err)
	}
	if result.Status != backgroundjobs.RunStatusCompleted {
		t.Errorf("status = %q, want %q", result.Status, backgroundjobs.RunStatusCompleted)
	}
}

func TestWaitForRunCompletion_ContextCancelled(t *testing.T) {
	outputRoot := t.TempDir()
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	runStore := backgroundjobs.NewRunStore(store.Dir(), lg)

	// Insert a running run (never transitions).
	running := backgroundjobs.Run{
		ID:        "run-cancel-1",
		JobID:     "job-1",
		Status:    backgroundjobs.RunStatusRunning,
		StartedAt: time.Now().UTC(),
	}
	if err := runStore.Append(running); err != nil {
		t.Fatalf("append running run: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := waitForRunCompletion(ctx, runStore, "job-1", "run-cancel-1")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}
