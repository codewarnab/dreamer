package jobqueue

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"dreamer/internal/fsutil"
)

// storeVersion enables forward-compatible migrations of jobs.json.
const storeVersion = 1

// Store handles persistence of the job list to jobs.json.
type Store struct {
	path string
	mu   sync.Mutex
}

// persistedState is the on-disk schema for jobs.json.
type persistedState struct {
	Version int    `json:"version"`
	Jobs    []*Job `json:"jobs"`
}

// NewStore creates a store that reads/writes jobs.json at the given path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path returns the filesystem path used by this store.
func (s *Store) Path() string {
	return s.path
}

// Load reads jobs.json and returns the job list. A missing file returns an
// empty list (not an error) — this is the normal state on first run.
func (s *Store) Load() ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read jobs file %q: %w", s.path, err)
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal jobs file %q: %w", s.path, err)
	}
	if state.Version != storeVersion {
		return nil, fmt.Errorf("jobs file %q has version %d, expected %d", s.path, state.Version, storeVersion)
	}
	return state.Jobs, nil
}

// Save atomically writes the job list to jobs.json.
func (s *Store) Save(jobs []*Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := persistedState{
		Version: storeVersion,
		Jobs:    jobs,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal jobs state: %w", err)
	}
	if err := fsutil.WriteFileAtomic(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write jobs file %q: %w", s.path, err)
	}
	return nil
}
