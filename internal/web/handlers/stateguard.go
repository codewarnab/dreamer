package handlers

import "sync"

// projectEntry wraps a mutex for per-project locking.
type projectEntry struct {
	mu sync.Mutex
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

// Lock acquires the per-project mutex and returns a release function.
// The returned closure is single-shot: calling it more than once is a
// no-op. Always defer the returned closure after the ok/error check.
func (pl *ProjectLock) Lock(projectName string) func() {
	// pl.mu is held for the entire check-and-write block below, so there is
	// no TOCTOU race. NewProjectLock initialises pl.locks via make(), so a
	// nil-map write here is impossible when this type is used correctly.
	pl.mu.Lock()
	entry, ok := pl.locks[projectName]
	if !ok {
		entry = &projectEntry{}
		pl.locks[projectName] = entry
	}
	pl.mu.Unlock()
	entry.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(entry.mu.Unlock)
	}
}
