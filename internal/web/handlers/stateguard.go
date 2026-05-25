package handlers

import "sync"

// ProjectLock serializes state Load→Mutate→Save cycles per project
// so concurrent web handlers don't clobber each other's mutations.
type ProjectLock struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewProjectLock creates a ProjectLock ready for use.
func NewProjectLock() *ProjectLock {
	return &ProjectLock{locks: make(map[string]*sync.Mutex)}
}

// Lock acquires the per-project mutex. Always pair with defer Unlock.
func (pl *ProjectLock) Lock(projectName string) {
	pl.mu.Lock()
	entry, ok := pl.locks[projectName]
	if !ok {
		entry = &sync.Mutex{}
		pl.locks[projectName] = entry
	}
	pl.mu.Unlock()
	entry.Lock()
}

// Unlock releases the per-project mutex. No-op if projectName was never
// Locked (defensive against caller bugs — avoids nil-pointer panic).
func (pl *ProjectLock) Unlock(projectName string) {
	pl.mu.Lock()
	entry := pl.locks[projectName]
	pl.mu.Unlock()
	if entry != nil {
		entry.Unlock()
	}
}
