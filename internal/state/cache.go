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
// Thread-safe; designed for concurrent web handler use. GetState and
// GetHistory return detached copies so callers cannot mutate cached data.
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
				st := cloneState(entry.state)
				sc.mu.RUnlock()
				return st, nil
			}
		}
	}
	sc.mu.RUnlock()

	// Cache miss — hold write lock for the entire Load+Stat sequence
	// to prevent a concurrent Save+Invalidate from causing a TOCTOU
	// where we store old data with new mtime/size.
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Double-check: another goroutine may have populated the entry
	// while we waited for the lock.
	entry, ok = sc.items[projectName]
	if ok && entry.state != nil {
		if info, statErr := os.Stat(statePath); statErr == nil {
			if info.ModTime().Equal(entry.stateMod) && info.Size() == entry.stateSize {
				return cloneState(entry.state), nil
			}
		}
	}

	st, err := Load(outputRoot, projectName)
	if err != nil {
		return nil, err
	}

	info, statErr := os.Stat(statePath)
	if statErr != nil {
		return st, nil // stat failed; return the loaded state without caching
	}

	existing := sc.items[projectName]
	if existing == nil {
		existing = &stateCacheEntry{}
	}
	existing.state = st
	existing.stateMod = info.ModTime()
	existing.stateSize = info.Size()
	sc.items[projectName] = existing

	return cloneState(st), nil
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
				h := cloneHistory(entry.history)
				sc.mu.RUnlock()
				return h, nil
			}
		}
	}
	sc.mu.RUnlock()

	// Cache miss — hold write lock for the entire Load+Stat sequence.
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Double-check after acquiring write lock.
	entry, ok = sc.items[projectName]
	if ok && entry.history != nil {
		if info, statErr := os.Stat(histPath); statErr == nil {
			if info.ModTime().Equal(entry.histMod) && info.Size() == entry.histSize {
				return cloneHistory(entry.history), nil
			}
		}
	}

	hist, err := LoadHistory(outputRoot, projectName)
	if err != nil {
		return nil, err
	}

	info, statErr := os.Stat(histPath)
	if statErr != nil {
		return hist, nil // stat failed; return loaded history without caching
	}

	existing := sc.items[projectName]
	if existing == nil {
		existing = &stateCacheEntry{}
	}
	existing.history = hist
	existing.histMod = info.ModTime()
	existing.histSize = info.Size()
	sc.items[projectName] = existing

	return cloneHistory(hist), nil
}

// Invalidate drops the cached entry for a project so the next GetState
// or GetHistory re-reads from disk. Call this after state.Save or
// state.SaveHistory to ensure subsequent reads see the new data.
func (sc *StateCache) Invalidate(projectName string) {
	sc.mu.Lock()
	delete(sc.items, projectName)
	sc.mu.Unlock()
}

func cloneState(st *State) *State {
	if st == nil {
		return nil
	}
	copyState := *st
	copyState.ChatHashes = cloneStringMap(st.ChatHashes)
	copyState.FindingHashes = append([]string(nil), st.FindingHashes...)
	copyState.ProviderUsage = cloneProviderUsageMap(st.ProviderUsage)
	copyState.UsageStats = cloneInt64Map(st.UsageStats)
	copyState.LastRunPerCategory = cloneTimeMap(st.LastRunPerCategory)
	copyState.Findings = cloneFindingStateMap(st.Findings)
	copyState.CachedPhase1 = cloneCachedPhase1Result(st.CachedPhase1)
	return &copyState
}

func cloneHistory(history *History) *History {
	if history == nil {
		return nil
	}
	copyHistory := *history
	copyHistory.Days = make([]DaySummary, len(history.Days))
	for i, day := range history.Days {
		copyHistory.Days[i] = day
		copyHistory.Days[i].PerCategory = cloneIntMap(day.PerCategory)
	}
	return &copyHistory
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneProviderUsageMap(src map[string]ProviderUsage) map[string]ProviderUsage {
	if src == nil {
		return nil
	}
	dst := make(map[string]ProviderUsage, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneInt64Map(src map[string]int64) map[string]int64 {
	if src == nil {
		return nil
	}
	dst := make(map[string]int64, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneTimeMap(src map[string]time.Time) map[string]time.Time {
	if src == nil {
		return nil
	}
	dst := make(map[string]time.Time, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneFindingStateMap(src map[string]FindingState) map[string]FindingState {
	if src == nil {
		return nil
	}
	dst := make(map[string]FindingState, len(src))
	for key, value := range src {
		if value.AppliedReversal != nil {
			reversal := *value.AppliedReversal
			value.AppliedReversal = &reversal
		}
		if value.ApplySpec != nil {
			spec := *value.ApplySpec
			value.ApplySpec = &spec
		}
		dst[key] = value
	}
	return dst
}

func cloneCachedPhase1Result(src *CachedPhase1Result) *CachedPhase1Result {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Mistakes = make(map[string][]CachedMistake, len(src.Mistakes))
	for key, mistakes := range src.Mistakes {
		dst.Mistakes[key] = append([]CachedMistake(nil), mistakes...)
	}
	return &dst
}

func cloneIntMap(src map[string]int) map[string]int {
	if src == nil {
		return nil
	}
	dst := make(map[string]int, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
