package backgroundjobs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
)

const (
	jobsFile      = "jobs.json"
	storeLockFile = "store.lock"
	storeDir      = "background-jobs"
)

// Store provides cross-process CRUD for background job state. All mutations
// go through Update, which acquires a file-based lock, loads current state,
// applies the mutation, and saves atomically. The in-process mutex serializes
// goroutines within the same process (file locks only block other processes).
type Store struct {
	dir    string
	logger *logging.Logger
	mu     sync.Mutex // serializes goroutines within the same process; file lock handles cross-process
}

// NewStore creates a Store rooted at <outputRoot>/background-jobs.
func NewStore(outputRoot string, logger *logging.Logger) *Store {
	return &Store{
		dir:    filepath.Join(outputRoot, storeDir),
		logger: logger,
	}
}

// Dir returns the store directory path.
func (s *Store) Dir() string { return s.dir }

// Update acquires the store lock, loads state, applies the mutation, and
// saves atomically. This is the only mutation API — callers must not hold
// state across calls because another process may have changed it.
func (s *Store) Update(ctx context.Context, mutate func(*State) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, fsutil.DirPerms); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	lockPath := filepath.Join(s.dir, storeLockFile)
	release, err := fsutil.AcquireLock(lockPath, s.logger)
	if err != nil {
		return fmt.Errorf("acquire store lock: %w", err)
	}
	defer release()

	state, err := s.load()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	if err := mutate(state); err != nil {
		return err
	}

	return s.save(state)
}

// Load returns a read-only snapshot. Use Update for mutations.
func (s *Store) Load() (*State, error) {
	return s.load()
}

func (s *Store) load() (*State, error) {
	path := filepath.Join(s.dir, jobsFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{Jobs: map[string]*Job{}, Version: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read jobs file: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("unmarshal jobs: %w", err)
	}
	if st.Jobs == nil {
		st.Jobs = map[string]*Job{}
	}
	return &st, nil
}

func (s *Store) save(state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	path := filepath.Join(s.dir, jobsFile)
	return fsutil.WriteFileAtomic(path, data, fsutil.FilePerms)
}
