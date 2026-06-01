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
	"dreamer/internal/migrate"
)

const (
	jobsFile      = "jobs.json"
	storeLockFile = "store.lock"
	storeDir      = "background-jobs"

	// storeVersion is the current schema version for jobs.json.
	storeVersion = 1
)

// jobsMigrations is the ordered migration registry for jobs.json.
var jobsMigrations = migrate.Registry{
	Name:       "jobs.json",
	CurrentVer: storeVersion,
	Migrations: []migrate.Migration{
		{
			FromVersion: 0,
			ToVersion:   1,
			Description: "add version field to legacy files",
			Migrate: func(data []byte) ([]byte, error) {
				return migrate.SetVersion(data, 1)
			},
		},
	},
}

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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() (*State, error) {
	path := filepath.Join(s.dir, jobsFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{Jobs: map[string]*Job{}, Version: storeVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read jobs file: %w", err)
	}
	migrated, _, err := jobsMigrations.Run(data)
	if err != nil {
		return nil, fmt.Errorf("migrate jobs file: %w", err)
	}
	var st State
	if err := json.Unmarshal(migrated, &st); err != nil {
		return nil, fmt.Errorf("unmarshal jobs: %w", err)
	}
	if st.Jobs == nil {
		st.Jobs = map[string]*Job{}
	}
	return &st, nil
}

func (s *Store) save(state *State) error {
	path := filepath.Join(s.dir, jobsFile)

	// Backup on upgrade.
	if priorData, readErr := os.ReadFile(path); readErr == nil {
		priorVersion, hasVersion, _ := migrate.PeekVersion(priorData)
		if !hasVersion {
			priorVersion = 0
		}
		if priorVersion < storeVersion {
			backupPath := fmt.Sprintf("%s.v%d.bak", path, priorVersion)
			if _, statErr := os.Stat(backupPath); os.IsNotExist(statErr) {
				_ = fsutil.WriteFileAtomic(backupPath, priorData, fsutil.FilePerms)
			}
		}
	}

	state.Version = storeVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return fsutil.WriteFileAtomic(path, data, fsutil.FilePerms)
}
