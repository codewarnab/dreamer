package chat

import (
	"sync"
	"time"
)

// discoveryCacheTTL is short by design: the cache only exists to dedupe the
// burst of DiscoverChats calls that happens when the dashboard renders many
// project rollups in a single request cycle. Anything longer risks showing
// stale chat lists to users.
const discoveryCacheTTL = 2 * time.Second

type discoveryCacheEntry struct {
	sources []ChatSource
	err     error
	storedAt time.Time
}

var (
	discoveryCacheMu sync.RWMutex
	discoveryCache   = make(map[string]discoveryCacheEntry)
)

// DiscoverChatsCached wraps DiscoverChats with a tiny TTL cache keyed by
// projectPath. Callers that need fresh data (e.g. just after a delete) should
// call InvalidateDiscoveryCache first or use DiscoverChats directly.
func DiscoverChatsCached(projectPath string) ([]ChatSource, error) {
	now := time.Now()
	discoveryCacheMu.RLock()
	entry, ok := discoveryCache[projectPath]
	discoveryCacheMu.RUnlock()
	if ok && now.Sub(entry.storedAt) < discoveryCacheTTL {
		return entry.sources, entry.err
	}
	sources, err := DiscoverChats(projectPath)
	discoveryCacheMu.Lock()
	discoveryCache[projectPath] = discoveryCacheEntry{sources: sources, err: err, storedAt: now}
	discoveryCacheMu.Unlock()
	return sources, err
}

// InvalidateDiscoveryCache drops the cached entry for projectPath. Call after
// any operation that mutates discoverable sources (delete, new chat written).
// Passing "" clears the entire cache.
func InvalidateDiscoveryCache(projectPath string) {
	discoveryCacheMu.Lock()
	defer discoveryCacheMu.Unlock()
	if projectPath == "" {
		discoveryCache = make(map[string]discoveryCacheEntry)
		return
	}
	delete(discoveryCache, projectPath)
}
