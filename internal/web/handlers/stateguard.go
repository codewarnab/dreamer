package handlers

import "sync"

// projectEntry wraps a mutex with a locked flag so Unlock is idempotent
// (double-unlock returns silently instead of panicking).
type projectEntry struct {
	mu     sync.Mutex
	locked bool
}

// ProjectLock serializes state Load→Mutate→Save cycles per project
// so concurrent web handlers don't clobber each other's mutations.
type ProjectLock struct {
	mu    sync.Mutex
	locks map[string]*projectEntry
}

// NewProjectLock creates a ProjectLock ready for use.
func NewProjectLock() *ProjectLock {
	return &ProjectLock{locks: make(map[string]*projectEntry)}
}

// Lock acquires the per-project mutex. Always pair with defer Unlock.
func (pl *ProjectLock) Lock(projectName string) {
	pl.mu.Lock()
	entry, ok := pl.locks[projectName]
	if !ok {
		entry = &projectEntry{}
		pl.locks[projectName] = entry
	}
	pl.mu.Unlock()
	entry.mu.Lock()
	entry.locked = true
}

// Unlock releases the per-project mutex. Safe to call multiple times
// for the same project (second call is a no-op) or for a project that
// was never Locked.
func (pl *ProjectLock) Unlock(projectName string) {
	pl.mu.Lock()
	entry := pl.locks[projectName]
	pl.mu.Unlock()
	if entry != nil && entry.locked {
		entry.locked = false
		entry.mu.Unlock()
	}
}
