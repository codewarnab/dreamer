package backgroundjobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
	"dreamer/internal/logging"

	// Register providers so LookupProviderCapabilities works in tests.
	_ "dreamer/internal/analyzer/providers/claudeacp"
	_ "dreamer/internal/analyzer/providers/openclaudecli"
)

// writeMinimalConfig writes a minimal valid config.yaml for tests.
func writeMinimalConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "projects: []\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// mockSession implements analyzer.Session for testing.
type mockSession struct {
	runFunc func(ctx context.Context, prompt string, timeout time.Duration) (string, error)
}

func (m *mockSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, prompt, timeout)
	}
	return "mock output", nil
}

func (m *mockSession) Close() error { return nil }

// mockProvider implements analyzer.Provider for testing.
type mockProvider struct {
	id          string
	session     analyzer.Session
	startErr    error
	closeErr    error
	startCalled bool
}

func (m *mockProvider) ID() string { return m.id }

func (m *mockProvider) Start(ctx context.Context) error {
	m.startCalled = true
	return m.startErr
}

func (m *mockProvider) NewSession(ctx context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	return m.session, nil
}

func (m *mockProvider) Close() error { return m.closeErr }

// newTestExecutor creates an Executor with a mock provider factory.
func newTestExecutor(t *testing.T, provider *mockProvider) (*Executor, *Store, *RunStore) {
	t.Helper()
	outputRoot := t.TempDir()
	lg := newTestLogger(t)
	store := NewStore(outputRoot, lg)
	runStore := NewRunStore(store.Dir(), lg)
	audit := NewAuditWriter(store.Dir(), logging.Silent())
	cfgPath := writeMinimalConfig(t)

	executor := &Executor{
		Store:       store,
		RunStore:    runStore,
		AuditWriter: audit,
		ConfigPath:  cfgPath,
		Logger:      lg,
		NewProvider: func(id config.ProviderID, cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
			return provider, nil
		},
	}
	return executor, store, runStore
}

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	lg, err := logging.New(t.TempDir(), "error", 1)
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	t.Cleanup(func() { lg.Close() })
	return lg
}

// insertTestJob inserts a job directly into the store for testing.
func insertTestJob(t *testing.T, store *Store, job *Job) {
	t.Helper()
	err := store.Update(context.Background(), func(s *State) error {
		if s.Jobs == nil {
			s.Jobs = make(map[string]*Job)
		}
		s.Jobs[job.ID] = job
		return nil
	})
	if err != nil {
		t.Fatalf("insert test job: %v", err)
	}
}

func testReadOnlyJob(t *testing.T, id string) *Job {
	t.Helper()
	return &Job{
		ID:          id,
		Name:        "test-job",
		Prompt:      "do something",
		ProjectPath: t.TempDir(),
		ProviderID:  "openclaude-cli",
		Schedule: ScheduleSpec{
			Kind:      ScheduleDaily,
			TimeOfDay: "09:00",
			Timezone:  "UTC",
		},
		Enabled: true,
		Permissions: PermissionProfile{
			FileAccess: FileAccessReadOnly,
		},
	}
}

func TestExecutor_Run_Success(t *testing.T) {
	provider := &mockProvider{
		id:      "openclaude-cli",
		session: &mockSession{},
	}
	executor, store, runStore := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	insertTestJob(t, store, job)

	result, err := executor.Run(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Record.Status != RunStatusCompleted {
		t.Errorf("status = %q, want %q", result.Record.Status, RunStatusCompleted)
	}
	if result.Record.OutputSummary != "mock output" {
		t.Errorf("output = %q, want %q", result.Record.OutputSummary, "mock output")
	}
	if !provider.startCalled {
		t.Error("provider.Start was not called")
	}

	// Verify runs were recorded (running + completed).
	runs, err := runStore.List(job.ID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) < 1 {
		t.Fatalf("len(runs) = %d, want >= 1", len(runs))
	}
	// The last run should be completed.
	last := runs[0]
	if last.Status != RunStatusCompleted {
		t.Errorf("last run status = %q, want %q", last.Status, RunStatusCompleted)
	}
}

func TestExecutor_Run_JobNotFound(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, _, _ := newTestExecutor(t, provider)

	_, err := executor.Run(context.Background(), "nonexistent000001")
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want contains 'not found'", err.Error())
	}
}

func TestExecutor_Run_DisabledJob(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, store, runStore := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	job.Enabled = false
	insertTestJob(t, store, job)

	result, err := executor.Run(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run disabled: %v", err)
	}
	if result.Record.Status != RunStatusSkipped {
		t.Errorf("status = %q, want %q", result.Record.Status, RunStatusSkipped)
	}
	if result.Record.SkippedReason != "job is disabled" {
		t.Errorf("reason = %q, want %q", result.Record.SkippedReason, "job is disabled")
	}

	// Verify runs were recorded.
	runs, err := runStore.List(job.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runs) < 1 {
		t.Fatalf("len = %d, want >= 1", len(runs))
	}
}

func TestExecutor_Run_InvalidSchedule(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, store, _ := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	job.Schedule = ScheduleSpec{Kind: ScheduleDaily} // Missing TimeOfDay.
	insertTestJob(t, store, job)

	_, err := executor.Run(context.Background(), job.ID)
	if err == nil {
		t.Fatal("expected error for invalid schedule")
	}
	if !strings.Contains(err.Error(), "invalid schedule") {
		t.Errorf("error = %q, want contains 'invalid schedule'", err.Error())
	}
}

func TestExecutor_Run_ProviderNotBackgroundSafe(t *testing.T) {
	provider := &mockProvider{id: "claude-acp", session: &mockSession{}}
	executor, store, _ := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	job.ProviderID = "claude-acp"
	insertTestJob(t, store, job)

	_, err := executor.Run(context.Background(), job.ID)
	if err == nil {
		t.Fatal("expected error for non-background-safe provider")
	}
	if !strings.Contains(err.Error(), "not safe for background") {
		t.Errorf("error = %q, want contains 'not safe for background'", err.Error())
	}
}

func TestExecutor_Run_FullWorkspaceRejected(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, store, _ := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	job.Permissions.FileAccess = FileAccessFullWorkspace
	insertTestJob(t, store, job)

	_, err := executor.Run(context.Background(), job.ID)
	if err == nil {
		t.Fatal("expected error for full_workspace mode")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %q, want contains 'not supported'", err.Error())
	}
}

func TestExecutor_Run_SelectedWritesNoPaths(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, store, _ := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	job.Permissions.FileAccess = FileAccessSelectedWrites
	insertTestJob(t, store, job)

	_, err := executor.Run(context.Background(), job.ID)
	if err == nil {
		t.Fatal("expected error for selected_writes with no writable paths")
	}
	if !strings.Contains(err.Error(), "requires at least one writable path") {
		t.Errorf("error = %q, want contains 'requires at least one writable path'", err.Error())
	}
}

func TestExecutor_Run_SelectedWritesInvalidPaths(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, store, _ := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	job.Permissions.FileAccess = FileAccessSelectedWrites
	job.Permissions.WritablePaths = []string{"../../../etc/passwd"}
	insertTestJob(t, store, job)

	_, err := executor.Run(context.Background(), job.ID)
	if err == nil {
		t.Fatal("expected error for invalid writable paths")
	}
	if !strings.Contains(err.Error(), "invalid writable paths") {
		t.Errorf("error = %q, want contains 'invalid writable paths'", err.Error())
	}
}

func TestExecutor_Run_ProviderError(t *testing.T) {
	provider := &mockProvider{
		id:       "openclaude-cli",
		session:  &mockSession{},
		startErr: errors.New("provider crashed"),
	}
	executor, store, runStore := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	insertTestJob(t, store, job)

	result, err := executor.Run(context.Background(), job.ID)
	// The executor should handle provider errors gracefully.
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Record.Status != RunStatusFailed {
		t.Errorf("status = %q, want %q", result.Record.Status, RunStatusFailed)
	}

	runs, err := runStore.List(job.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runs) < 1 {
		t.Fatalf("len = %d, want >= 1", len(runs))
	}
	last := runs[0] // Most recent first.
	if last.Status != RunStatusFailed {
		t.Errorf("recorded status = %q, want %q", last.Status, RunStatusFailed)
	}
}

func TestExecutor_Run_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider := &mockProvider{
		id: "openclaude-cli",
		session: &mockSession{
			runFunc: func(runCtx context.Context, prompt string, timeout time.Duration) (string, error) {
				cancel() // Cancel the parent context during the run.
				return "", context.Canceled
			},
		},
	}
	executor, store, runStore := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	insertTestJob(t, store, job)

	result, err := executor.Run(ctx, job.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Record.Status != RunStatusCancelled {
		t.Errorf("status = %q, want %q", result.Record.Status, RunStatusCancelled)
	}

	runs, err := runStore.List(job.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runs) < 1 {
		t.Fatalf("len = %d, want >= 1", len(runs))
	}
	last := runs[0] // Most recent first.
	if last.Status != RunStatusCancelled {
		t.Errorf("recorded status = %q, want %q", last.Status, RunStatusCancelled)
	}
}

func TestExecutor_Run_UpdatesJobTimestamps(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, store, _ := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	insertTestJob(t, store, job)

	result, err := executor.Run(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Record.Status != RunStatusCompleted {
		t.Fatalf("status = %q", result.Record.Status)
	}

	// Reload and check timestamps.
	state, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	updated := state.Jobs[job.ID]
	if updated == nil {
		t.Fatal("job not found after run")
	}
	if updated.LastRunAt == nil {
		t.Error("LastRunAt is nil")
	}
	if updated.NextRunAt == nil {
		t.Error("NextRunAt is nil")
	}
	if updated.Health.RunState != RunStatusCompleted {
		t.Errorf("RunState = %q, want %q", updated.Health.RunState, RunStatusCompleted)
	}
}

func TestExecutor_Run_AuditClaimAndFinish(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, store, _ := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	insertTestJob(t, store, job)

	_, err := executor.Run(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Read audit file.
	auditPath := filepath.Join(store.Dir(), "audit.jsonl")
	data, fileErr := os.ReadFile(auditPath)
	if fileErr != nil {
		t.Fatalf("read audit bytes: %v", fileErr)
	}
	if len(data) == 0 {
		t.Error("audit file is empty")
	}
	content := string(data)
	if !strings.Contains(content, "job.run.claim") {
		t.Error("audit missing job.run.claim event")
	}
	if !strings.Contains(content, "job.run.finish") {
		t.Error("audit missing job.run.finish event")
	}
}

func TestExecutor_Run_PromptSnapshot(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, store, runStore := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	job.Prompt = strings.Repeat("Hello世界", 200) // 200 * 8 = 1600 bytes, 200 * 7 = 1400 runes
	insertTestJob(t, store, job)

	result, err := executor.Run(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// PromptSnapshot should be truncated to 500 runes.
	snapshot := result.Record.PromptSnapshot
	runeCount := utf8.RuneCountInString(snapshot)
	if runeCount > 500 {
		t.Errorf("PromptSnapshot has %d runes, want <= 500", runeCount)
	}
	if !utf8.ValidString(snapshot) {
		t.Error("PromptSnapshot contains invalid UTF-8")
	}

	// Verify in run store too.
	runs, _ := runStore.List(job.ID)
	if len(runs) > 0 && !utf8.ValidString(runs[0].PromptSnapshot) {
		t.Error("stored PromptSnapshot contains invalid UTF-8")
	}
}

func TestExecutor_Run_BackgroundSystemMessage(t *testing.T) {
	var capturedCfg analyzer.SessionConfig
	provider := &mockProvider{
		id: "openclaude-cli",
		session: &mockSession{
			runFunc: func(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
				return "ok", nil
			},
		},
	}

	outputRoot := t.TempDir()
	lg := newTestLogger(t)
	store := NewStore(outputRoot, lg)
	runStore := NewRunStore(store.Dir(), lg)
	audit := NewAuditWriter(store.Dir(), logging.Silent())
	cfgPath := writeMinimalConfig(t)

	executor := &Executor{
		Store:       store,
		RunStore:    runStore,
		AuditWriter: audit,
		ConfigPath:  cfgPath,
		Logger:      lg,
		NewProvider: func(id config.ProviderID, cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
			return &capturingProvider{
				inner:    provider,
				captured: &capturedCfg,
			}, nil
		},
	}

	job := testReadOnlyJob(t, "abc1234567890001")
	insertTestJob(t, store, job)

	_, err := executor.Run(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if capturedCfg.SystemMessage != backgroundSystemMessage {
		t.Errorf("system message = %q, want %q", capturedCfg.SystemMessage, backgroundSystemMessage)
	}
	if !capturedCfg.ReadOnly {
		t.Error("ReadOnly should be true")
	}
}

// capturingProvider wraps a mockProvider to capture SessionConfig.
type capturingProvider struct {
	inner    *mockProvider
	captured *analyzer.SessionConfig
}

func (c *capturingProvider) ID() string                      { return c.inner.id }
func (c *capturingProvider) Start(ctx context.Context) error { return c.inner.Start(ctx) }
func (c *capturingProvider) Close() error                    { return c.inner.Close() }
func (c *capturingProvider) NewSession(ctx context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	*c.captured = cfg
	return c.inner.NewSession(ctx, cfg)
}

func TestExecutor_Run_JobDeletedDuringRun(t *testing.T) {
	provider := &mockProvider{
		id: "openclaude-cli",
		session: &mockSession{
			runFunc: func(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
				return "output", nil
			},
		},
	}

	outputRoot := t.TempDir()
	lg := newTestLogger(t)
	store := NewStore(outputRoot, lg)
	runStore := NewRunStore(store.Dir(), lg)
	audit := NewAuditWriter(store.Dir(), logging.Silent())
	cfgPath := writeMinimalConfig(t)

	executor := &Executor{
		Store:       store,
		RunStore:    runStore,
		AuditWriter: audit,
		ConfigPath:  cfgPath,
		Logger:      lg,
		NewProvider: func(id config.ProviderID, cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
			return &deletingProvider{
				inner:   provider,
				store:   store,
				jobID:   "abc1234567890001",
				deleted: false,
			}, nil
		},
	}

	job := testReadOnlyJob(t, "abc1234567890001")
	insertTestJob(t, store, job)

	// Should not panic even though job is deleted during run.
	result, err := executor.Run(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Record.Status != RunStatusCompleted {
		t.Errorf("status = %q, want %q", result.Record.Status, RunStatusCompleted)
	}
}

// deletingProvider deletes the job from the store when NewSession is called.
type deletingProvider struct {
	inner   *mockProvider
	store   *Store
	jobID   string
	deleted bool
}

func (d *deletingProvider) ID() string                      { return d.inner.id }
func (d *deletingProvider) Start(ctx context.Context) error { return d.inner.Start(ctx) }
func (d *deletingProvider) Close() error                    { return d.inner.Close() }
func (d *deletingProvider) NewSession(ctx context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	if !d.deleted {
		_ = d.store.Update(ctx, func(s *State) error {
			delete(s.Jobs, d.jobID)
			return nil
		})
		d.deleted = true
	}
	return d.inner.NewSession(ctx, cfg)
}

func TestGenerateRunID_Format(t *testing.T) {
	id, err := GenerateRunID()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(id) != 17 { // "r" + 16 hex chars
		t.Errorf("len = %d, want 17", len(id))
	}
	if id[0] != 'r' {
		t.Errorf("first char = %q, want 'r'", id[0])
	}
	for _, c := range id[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex char %q in id", c)
		}
	}
}

func TestTruncateUTF8_MultiByte(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		want     string
	}{
		{"ascii short", "hello", 10, "hello"},
		{"ascii exact", "hello", 5, "hello"},
		{"ascii truncate", "hello world", 5, "hello"},
		{"multibyte short", "Hello世界", 10, "Hello世界"},
		{"multibyte truncate", "Hello世界!", 6, "Hello世"},
		{"empty", "", 5, ""},
		{"zero max", "hello", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateUTF8(tt.input, tt.maxRunes)
			if got != tt.want {
				t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tt.input, tt.maxRunes, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8: %q", got)
			}
		})
	}
}

// B11: Disabled-job skip should emit a job.run.skipped audit event.
func TestExecutor_Run_DisabledJob_EmitsAuditEvent(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, store, _ := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	job.Enabled = false
	insertTestJob(t, store, job)

	_, err := executor.Run(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run disabled: %v", err)
	}

	// Read audit log and verify job.run.skipped event exists.
	audit := NewAuditWriter(store.Dir(), logging.Silent())
	events, err := audit.ReadAll(100)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Event == "job.run.skipped" && ev.JobID == job.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected job.run.skipped audit event for disabled job, not found")
	}
}

// B10: Claim audit should be written after lock acquisition, not before.
// This test verifies the ordering is correct by checking that a successful
// run has both claim and finish events in the correct order.
func TestExecutor_Run_ClaimAuditAfterLock(t *testing.T) {
	provider := &mockProvider{id: "openclaude-cli", session: &mockSession{}}
	executor, store, _ := newTestExecutor(t, provider)

	job := testReadOnlyJob(t, "abc1234567890001")
	insertTestJob(t, store, job)

	_, err := executor.Run(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Read audit log and verify claim comes before finish.
	audit := NewAuditWriter(store.Dir(), logging.Silent())
	events, err := audit.ReadAll(100)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}

	claimIdx := -1
	finishIdx := -1
	for i, ev := range events {
		if ev.JobID == job.ID {
			if ev.Event == "job.run.claim" {
				claimIdx = i
			} else if ev.Event == "job.run.finish" {
				finishIdx = i
			}
		}
	}

	if claimIdx < 0 {
		t.Error("expected job.run.claim audit event")
	}
	if finishIdx < 0 {
		t.Error("expected job.run.finish audit event")
	}
	// ReadAll returns newest-first, so finish (written after claim) has lower index.
	if claimIdx >= 0 && finishIdx >= 0 && finishIdx > claimIdx {
		t.Error("job.run.claim should be written before job.run.finish (claim should appear later in newest-first order)")
	}
}
