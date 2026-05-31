package cmd

import (
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
	subcommands := []string{"list", "create", "show", "pause", "resume", "delete", "run"}
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
