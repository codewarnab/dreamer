package state

import (
	"os"
	"sync"
	"time"
)

// stateCacheEntry holds the cached state and history for a single project,
// along with the on-disk stat metadata used for invalidation.
type stateCacheEntry struct {
	state     *State
	history   *History
	stateMod  time.Time
	stateSize int64
	histMod   time.Time
	histSize  int64
}

// StateCache is a read-through cache for state.json and history.json.
// Entries are validated by comparing os.Stat mtime+size against the cached
// values. Invalidate drops an entry so the next read re-fetches from disk.
// Thread-safe; designed for concurrent web handler use.
type StateCache struct {
	mu    sync.RWMutex
	items map[string]*stateCacheEntry
}

// NewStateCache creates an empty StateCache ready for use.
func NewStateCache() *StateCache {
	return &StateCache{items: make(map[string]*stateCacheEntry)}
}

// GetState returns the cached state for a project if the on-disk file's
// mtime and size match the cached values; otherwise reads from disk and
// updates the cache. Returns the same result as Load on cache miss.
func (sc *StateCache) GetState(outputRoot, projectName string) (*State, error) {
	statePath, err := PathForProject(outputRoot, projectName)
	if err != nil {
		return nil, err
	}

	sc.mu.RLock()
	entry, ok := sc.items[projectName]
	if ok && entry.state != nil {
		if info, statErr := os.Stat(statePath); statErr == nil {
			if info.ModTime().Equal(entry.stateMod) && info.Size() == entry.stateSize {
				st := entry.state
				sc.mu.RUnlock()
				return st, nil
			}
		}
	}
	sc.mu.RUnlock()

	// Cache miss — read from disk.
	st, err := Load(outputRoot, projectName)
	if err != nil {
		return nil, err
	}

	info, statErr := os.Stat(statePath)
	if statErr != nil {
		return st, nil // stat failed; return the loaded state without caching
	}

	sc.mu.Lock()
	existing := sc.items[projectName]
	if existing == nil {
		existing = &stateCacheEntry{}
	}
	existing.state = st
	existing.stateMod = info.ModTime()
	existing.stateSize = info.Size()
	sc.items[projectName] = existing
	sc.mu.Unlock()

	return st, nil
}

// GetHistory returns the cached history for a project if the on-disk
// file's mtime and size match; otherwise reads from disk and updates
// the cache.
func (sc *StateCache) GetHistory(outputRoot, projectName string) (*History, error) {
	histPath, err := HistoryPath(outputRoot, projectName)
	if err != nil {
		return nil, err
	}

	sc.mu.RLock()
	entry, ok := sc.items[projectName]
	if ok && entry.history != nil {
		if info, statErr := os.Stat(histPath); statErr == nil {
			if info.ModTime().Equal(entry.histMod) && info.Size() == entry.histSize {
				h := entry.history
				sc.mu.RUnlock()
				return h, nil
			}
		}
	}
	sc.mu.RUnlock()

	// Cache miss — read from disk.
	hist, err := LoadHistory(outputRoot, projectName)
	if err != nil {
		return nil, err
	}

	info, statErr := os.Stat(histPath)
	if statErr != nil {
		return hist, nil // stat failed; return loaded history without caching
	}

	sc.mu.Lock()
	existing := sc.items[projectName]
	if existing == nil {
		existing = &stateCacheEntry{}
	}
	existing.history = hist
	existing.histMod = info.ModTime()
	existing.histSize = info.Size()
	sc.items[projectName] = existing
	sc.mu.Unlock()

	return hist, nil
}

// Invalidate drops the cached entry for a project so the next GetState
// or GetHistory re-reads from disk. Call this after state.Save or
// state.SaveHistory to ensure subsequent reads see the new data.
func (sc *StateCache) Invalidate(projectName string) {
	sc.mu.Lock()
	delete(sc.items, projectName)
	sc.mu.Unlock()
}
