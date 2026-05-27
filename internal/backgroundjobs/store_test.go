package backgroundjobs

import (
	"context"
	"sync"
	"testing"

	"dreamer/internal/logging"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	lg, err := logging.New(dir, "error", 0)
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	return NewStore(dir, lg)
}

func TestStore_Load_MissingFile_ReturnsEmptyState(t *testing.T) {
	s := newTestStore(t)
	state, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(state.Jobs) != 0 {
		t.Errorf("expected empty jobs map, got %d", len(state.Jobs))
	}
	if state.Version != 1 {
		t.Errorf("expected version 1, got %d", state.Version)
	}
}

func TestStore_Update_CreatesJobsFile(t *testing.T) {
	s := newTestStore(t)
	err := s.Update(context.Background(), func(st *State) error {
		id, _ := GenerateJobID()
		st.Jobs[id] = &Job{
			ID:       id,
			Name:     "test job",
			Enabled:  true,
			ProviderID: "openclaude-cli",
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(loaded.Jobs) != 1 {
		t.Errorf("expected 1 job, got %d", len(loaded.Jobs))
	}
}

func TestStore_Update_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	id, _ := GenerateJobID()
	name := "round-trip test"

	err := s.Update(context.Background(), func(st *State) error {
		st.Jobs[id] = &Job{
			ID:          id,
			Name:        name,
			Prompt:      "do something",
			ProjectPath: "/tmp/test",
			ProviderID:  "claude-cli",
			Enabled:     true,
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	job, ok := loaded.Jobs[id]
	if !ok {
		t.Fatalf("job %q not found after load", id)
	}
	if job.Name != name {
		t.Errorf("Name = %q, want %q", job.Name, name)
	}
	if job.Prompt != "do something" {
		t.Errorf("Prompt = %q, want %q", job.Prompt, "do something")
	}
}

func TestStore_Update_PreservesOnMutateError(t *testing.T) {
	s := newTestStore(t)
	id, _ := GenerateJobID()

	// Create initial state.
	if err := s.Update(context.Background(), func(st *State) error {
		st.Jobs[id] = &Job{ID: id, Name: "original"}
		return nil
	}); err != nil {
		t.Fatalf("first Update: %v", err)
	}

	// Mutate with error — should not corrupt existing state.
	_ = s.Update(context.Background(), func(st *State) error {
		st.Jobs["other"] = &Job{ID: "other"}
		return context.Canceled
	})

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Jobs) != 1 {
		t.Errorf("expected 1 job after failed mutation, got %d", len(loaded.Jobs))
	}
	if _, ok := loaded.Jobs[id]; !ok {
		t.Error("original job missing after failed mutation")
	}
}

func TestStore_Update_ConcurrentSafety(t *testing.T) {
	s := newTestStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.Update(context.Background(), func(st *State) error {
				id, _ := GenerateJobID()
				st.Jobs[id] = &Job{ID: id, Name: "concurrent"}
				return nil
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent Update error: %v", err)
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Jobs) != 10 {
		t.Errorf("expected 10 jobs after concurrent writes, got %d", len(loaded.Jobs))
	}
}

func TestStore_Update_CancelledContext(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.Update(ctx, func(st *State) error {
		return nil
	})
	if err == nil {
		t.Error("Update with cancelled context expected error")
	}
}

func TestStore_Dir(t *testing.T) {
	s := newTestStore(t)
	dir := s.Dir()
	if dir == "" {
		t.Error("Dir() returned empty string")
	}
}
