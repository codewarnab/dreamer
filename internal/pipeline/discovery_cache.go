package pipeline

import (
	"sync"
	"time"

	"dreamer/internal/chat"
)

// DiscoveryCache stores per-project discovery metadata so the daemon can skip
// the expensive HashFile loop when source files haven't changed. The pipeline
// remains stateless (used by one-shot analyze too); the cache lives in the
// daemon and is passed through Options.
type DiscoveryCache struct {
	mu    sync.RWMutex
	items map[string]*discoveryCacheEntry
}

type discoveryCacheEntry struct {
	sourceMTimes map[string]time.Time
	repoHeadSHA  string
}

// NewDiscoveryCache creates an empty cache ready for use.
func NewDiscoveryCache() *DiscoveryCache {
	return &DiscoveryCache{
		items: make(map[string]*discoveryCacheEntry),
	}
}

// Check returns true when the caller can skip re-hashing: every source's
// ModifiedTime matches the cached value, the source count is the same, and
// repoHeadSHA is unchanged. Returns false on the first mismatch or if the
// project has never been cached. Thread-safe.
func (c *DiscoveryCache) Check(projectPath string, sources []chat.Source, repoHeadSHA string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.items[projectPath]
	if !ok {
		return false
	}
	if entry.repoHeadSHA != repoHeadSHA {
		return false
	}
	if len(entry.sourceMTimes) != len(sources) {
		return false
	}
	for _, s := range sources {
		cached, exists := entry.sourceMTimes[s.Path]
		if !exists || !cached.Equal(s.ModifiedTime) {
			return false
		}
	}
	return true
}

// Update stores the current discovery state for a project. Call this only
// after a successful state.Save so the cache never records a state the
// pipeline didn't persist. Thread-safe.
func (c *DiscoveryCache) Update(projectPath string, sources []chat.Source, repoHeadSHA string) {
	mtimes := make(map[string]time.Time, len(sources))
	for _, s := range sources {
		mtimes[s.Path] = s.ModifiedTime
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[projectPath] = &discoveryCacheEntry{
		sourceMTimes: mtimes,
		repoHeadSHA:  repoHeadSHA,
	}
}
