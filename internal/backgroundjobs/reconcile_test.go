package backgroundjobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// mockScheduler implements Scheduler for testing.
// Records calls and supports error injection.
type mockScheduler struct {
	installed []ScheduleParams
	removed   []string
	health    map[string]ScheduleHealth
	ownIDs    []string // schedule IDs returned by ListOwn
	// Error injection — set to non-nil to make the corresponding call fail.
	installErr error
	removeErr  error
	inspectErr error
	listErr    error
}

func newMockScheduler() *mockScheduler {
	return &mockScheduler{
		health: make(map[string]ScheduleHealth),
	}
}

func (m *mockScheduler) Install(_ context.Context, params ScheduleParams) (OSScheduleState, error) {
	if m.installErr != nil {
		return OSScheduleState{}, m.installErr
	}
	m.installed = append(m.installed, params)
	return OSScheduleState{
		ScheduleID: "os-" + params.JobID,
		InstallID:  "test-install-id",
	}, nil
}

func (m *mockScheduler) Update(_ context.Context, params ScheduleParams) (OSScheduleState, error) {
	if m.installErr != nil {
		return OSScheduleState{}, m.installErr
	}
	return OSScheduleState{ScheduleID: "os-" + params.JobID}, nil
}

func (m *mockScheduler) Remove(_ context.Context, id string) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	m.removed = append(m.removed, id)
	return nil
}

func (m *mockScheduler) Inspect(_ context.Context, id string) (ScheduleHealth, error) {
	if m.inspectErr != nil {
		return ScheduleHealth{}, m.inspectErr
	}
	if h, ok := m.health[id]; ok {
		return h, nil
	}
	return ScheduleHealth{Installed: true}, nil
}

func (m *mockScheduler) ListOwn(_ context.Context) ([]string, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	result := make([]string, len(m.ownIDs))
	copy(result, m.ownIDs)
	return result, nil
}

// addTestJob is a helper that adds a simple enabled/disabled job to the store.
func addTestJob(t *testing.T, store *Store, id string, enabled bool) {
	t.Helper()
	ctx := context.Background()
	if err := store.Update(ctx, func(s *State) error {
		s.Jobs[id] = &Job{
			ID:      id,
			Name:    fmt.Sprintf("test-%s", id),
			Enabled: enabled,
			Schedule: ScheduleSpec{
				Kind:      ScheduleDaily,
				TimeOfDay: "09:00",
			},
		}
		return nil
	}); err != nil {
		t.Fatalf("addTestJob: %v", err)
	}
}

func TestComputeReconcileActions_Install(t *testing.T) {
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: &mockScheduler{},
		Store:     store,
		Logger:    newTestLogger(t),
	}

	// Add job with no OS schedule.
	addTestJob(t, store, "job1", true)

	state, _ := store.Load()
	actions, _ := r.computeReconcileActions(context.Background(), state)

	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	if actions[0].Kind != ReconcileInstall {
		t.Errorf("kind = %v, want ReconcileInstall", actions[0].Kind)
	}
}

func TestComputeReconcileActions_RemoveDisabled(t *testing.T) {
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: &mockScheduler{},
		Store:     store,
		Logger:    newTestLogger(t),
	}

	// Add disabled job with OS schedule.
	ctx := context.Background()
	if err := store.Update(ctx, func(s *State) error {
		s.Jobs["job1"] = &Job{
			ID:      "job1",
			Name:    "disabled-job",
			Enabled: false,
			OSSchedule: OSScheduleState{
				ScheduleID: "os-job1",
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	state, _ := store.Load()
	actions, _ := r.computeReconcileActions(ctx, state)

	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	if actions[0].Kind != ReconcileRemoveDisabled {
		t.Errorf("kind = %v, want ReconcileRemoveDisabled", actions[0].Kind)
	}
}

func TestComputeReconcileActions_InSync(t *testing.T) {
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: &mockScheduler{},
		Store:     store,
		Logger:    newTestLogger(t),
	}

	// Add enabled job with matching OS schedule.
	specHash, _ := HashScheduleSpec(ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "09:00"})
	ctx := context.Background()
	if err := store.Update(ctx, func(s *State) error {
		s.Jobs["job1"] = &Job{
			ID:      "job1",
			Name:    "synced-job",
			Enabled: true,
			Schedule: ScheduleSpec{
				Kind:      ScheduleDaily,
				TimeOfDay: "09:00",
			},
			OSSchedule: OSScheduleState{
				ScheduleID: "os-job1",
				InstallID:  "test-install-id",
				SpecHash:   specHash,
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	state, _ := store.Load()
	actions, _ := r.computeReconcileActions(ctx, state)

	if len(actions) != 0 {
		t.Errorf("got %d actions for in-sync job, want 0", len(actions))
	}
}

func TestReconcileSchedules_DryRun(t *testing.T) {
	sched := &mockScheduler{}
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: sched,
		Store:     store,
		Logger:    newTestLogger(t),
	}

	// Add job with no OS schedule.
	addTestJob(t, store, "job1", true)

	result, err := r.ReconcileSchedules(context.Background(), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Dry-run should report what WOULD happen.
	if result.Installed != 1 {
		t.Errorf("Installed = %d, want 1", result.Installed)
	}

	// But should NOT actually install.
	if len(sched.installed) != 0 {
		t.Errorf("scheduler.Install called %d times during dry-run, want 0", len(sched.installed))
	}
}

func TestReconcileSchedules_EndToEnd(t *testing.T) {
	sched := &mockScheduler{}
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: sched,
		Store:     store,
		Logger:    newTestLogger(t),
	}

	// Add job with no OS schedule.
	addTestJob(t, store, "job1", true)

	result, err := r.ReconcileSchedules(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Installed != 1 {
		t.Errorf("Installed = %d, want 1", result.Installed)
	}
	if len(sched.installed) != 1 {
		t.Fatalf("scheduler.Install called %d times, want 1", len(sched.installed))
	}
	if sched.installed[0].JobID != "job1" {
		t.Errorf("installed job ID = %q, want job1", sched.installed[0].JobID)
	}
}

func TestReconcileSchedules_InstallError(t *testing.T) {
	sched := &mockScheduler{
		installErr: errors.New("schtasks failed"),
	}
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: sched,
		Store:     store,
		Logger:    newTestLogger(t),
	}

	addTestJob(t, store, "job1", true)

	result, err := r.ReconcileSchedules(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Errors) != 1 {
		t.Fatalf("got %d errors, want 1", len(result.Errors))
	}
	if result.Errors[0].JobID != "job1" {
		t.Errorf("error job ID = %q, want job1", result.Errors[0].JobID)
	}
	if !strings.Contains(result.Errors[0].Err.Error(), "schtasks failed") {
		t.Errorf("error = %q, want contains 'schtasks failed'", result.Errors[0].Err.Error())
	}
}

func TestSelfRepair_CallsInstall(t *testing.T) {
	sched := &mockScheduler{}
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: sched,
		Store:     store,
		Logger:    newTestLogger(t),
	}

	// Add job with no OS schedule.
	addTestJob(t, store, "job1", true)

	err := r.SelfRepair(context.Background(), "job1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sched.installed) != 1 {
		t.Errorf("Install called %d times, want 1", len(sched.installed))
	}
}

func TestSelfRepair_SkipsWhenInstalled(t *testing.T) {
	specHash, _ := HashScheduleSpec(ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "09:00"})
	sched := &mockScheduler{}
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: sched,
		Store:     store,
		Logger:    newTestLogger(t),
	}

	// Add job with matching OS schedule.
	ctx := context.Background()
	if err := store.Update(ctx, func(s *State) error {
		s.Jobs["job1"] = &Job{
			ID:      "job1",
			Name:    "synced-job",
			Enabled: true,
			Schedule: ScheduleSpec{
				Kind:      ScheduleDaily,
				TimeOfDay: "09:00",
			},
			OSSchedule: OSScheduleState{
				ScheduleID: "os-job1",
				InstallID:  "test-install-id",
				SpecHash:   specHash,
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err := r.SelfRepair(ctx, "job1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sched.installed) != 0 {
		t.Errorf("Install called %d times, want 0 (already installed)", len(sched.installed))
	}
}

func TestReconcileSchedules_DisabledJobRemovesSchedule(t *testing.T) {
	sched := &mockScheduler{}
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: sched,
		Store:     store,
		Logger:    newTestLogger(t),
	}

	// Add disabled job with OS schedule.
	ctx := context.Background()
	if err := store.Update(ctx, func(s *State) error {
		s.Jobs["job1"] = &Job{
			ID:      "job1",
			Name:    "disabled-job",
			Enabled: false,
			OSSchedule: OSScheduleState{
				ScheduleID: "os-job1",
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	result, err := r.ReconcileSchedules(ctx, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Disabled != 1 {
		t.Errorf("Disabled = %d, want 1", result.Disabled)
	}
	if len(sched.removed) != 1 {
		t.Errorf("Remove called %d times, want 1", len(sched.removed))
	}
	if sched.removed[0] != "job1" {
		t.Errorf("removed ID = %q, want job1", sched.removed[0])
	}
}

// B15: Orphaned OS schedules with invalid IDs should be skipped by computeReconcileActions.
// The real ListOwn validates IDs, but if an invalid ID slips through,
// computeReconcileActions should still skip it safely.
func TestComputeReconcileActions_OrphanWithInvalidIDSkipped(t *testing.T) {
	sched := newMockScheduler()
	// Simulate an OS schedule with a non-hex ID that fails ValidateJobID.
	sched.ownIDs = []string{"not-a-valid-id"}
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: sched,
		Store:     store,
		Logger:    newTestLogger(t),
	}

	state, _ := store.Load()
	actions, _ := r.computeReconcileActions(context.Background(), state)

	// The invalid ID should not produce a ReconcileRemove action.
	for _, a := range actions {
		if a.JobID == "not-a-valid-id" && a.Kind == ReconcileRemove {
			t.Error("invalid ID should not produce ReconcileRemove action")
		}
	}
}

// B15: Valid orphaned OS schedules should be cleaned up.
func TestComputeReconcileActions_ValidOrphanRemoved(t *testing.T) {
	sched := newMockScheduler()
	// Valid hex job ID that doesn't exist in the store.
	sched.ownIDs = []string{"aabbccdd11223344"}
	store := newTestStore(t)
	r := &Reconciler{
		Scheduler: sched,
		Store:     store,
		Logger:    newTestLogger(t),
	}

	state, _ := store.Load()
	actions, _ := r.computeReconcileActions(context.Background(), state)

	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	if actions[0].Kind != ReconcileRemove {
		t.Errorf("kind = %v, want ReconcileRemove", actions[0].Kind)
	}
}
