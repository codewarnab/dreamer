package analyzer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dreamer/internal/fsutil"
)

// modelListCacheTTL is the maximum age of a cached model list before a fresh fetch
// is attempted. Five minutes balances freshness against request latency: model lists
// change at most when a provider releases a new version (hours to days), while a live
// HTTP fetch adds ~50 ms. Sub-minute TTLs would add measurable page-load latency with
// no practical benefit.
const modelListCacheTTL = 5 * time.Minute

// modelListDiskTTL is the maximum age of a cache file entry loaded at startup.
// Entries older than this are skipped during Load — seven days is generous since
// model lists are stable for days to weeks.
const modelListDiskTTL = 7 * 24 * time.Hour

// modelListCacheFile is the filename used for the persisted cache inside the cache dir.
const modelListCacheFile = "model-list-cache.json"

// ModelListCache caches live model lists fetched via ModelLister with a TTL.
// Zero value is immediately usable. Safe for concurrent use.
//
// The cache reduces /api/provider-meta latency by avoiding a live network round-trip
// on every page load. Entries older than modelListCacheTTL are treated as missing
// and trigger a fresh fetch.
type ModelListCache struct {
	mu      sync.Mutex
	entries map[string]modelListEntry // keyed by provider ID
}

type modelListEntry struct {
	models    []string
	fetchedAt time.Time
}

// diskEntry is the JSON-serialisable form of one cache entry.
type diskEntry struct {
	Models    []string  `json:"models"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Get returns the cached list for providerID if it is fresher than modelListCacheTTL.
// Returns nil when the cache is cold or the entry has expired.
func (c *ModelListCache) Get(providerID string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[providerID]
	if !ok {
		return nil
	}
	if time.Since(entry.fetchedAt) > modelListCacheTTL {
		return nil
	}
	return entry.models
}

// Peek returns the cached list for providerID ignoring the freshness TTL that
// Get enforces. It is for callers that want any reasonably recent on-disk list
// (e.g. the setup wizard, which warms from a possibly hours-old cache file)
// rather than a strictly-fresh one. Entries are still bounded by modelListDiskTTL
// at Load time. Returns nil when there is no entry.
func (c *ModelListCache) Peek(providerID string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[providerID]
	if !ok {
		return nil
	}
	return entry.models
}

// Set stores models for providerID with the current time as the fetch timestamp.
// Replaces any existing entry.
func (c *ModelListCache) Set(providerID string, models []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]modelListEntry)
	}
	c.entries[providerID] = modelListEntry{
		models:    models,
		fetchedAt: time.Now(),
	}
}

// Load reads the cache file from dir and populates the in-memory cache with
// entries that are no older than modelListDiskTTL. Entries beyond the TTL are
// silently skipped. Returns nil when the file does not exist or the cache
// directory is empty — safe to call unconditionally at startup.
func (c *ModelListCache) Load(dir string) error {
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, modelListCacheFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // cold start — nothing to warm
	}
	if err != nil {
		return fmt.Errorf("read model list cache %q: %w", path, err)
	}

	var disk map[string]diskEntry
	if err := json.Unmarshal(data, &disk); err != nil {
		// Corrupted cache file — log-worthy but non-fatal; the daemon will
		// overwrite it on the next successful fetch.
		return fmt.Errorf("parse model list cache %q: %w", path, err)
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]modelListEntry, len(disk))
	}
	for id, de := range disk {
		if now.Sub(de.FetchedAt) > modelListDiskTTL {
			continue // stale entry — skip, do not warm from it
		}
		if len(de.Models) == 0 {
			continue
		}
		c.entries[id] = modelListEntry{models: de.Models, fetchedAt: de.FetchedAt}
	}
	return nil
}

// Save atomically writes all in-memory cache entries to dir/model-list-cache.json.
// Failures are non-fatal to the daemon — the caller should log but not return an error.
func (c *ModelListCache) Save(dir string) error {
	if dir == "" {
		return nil
	}

	c.mu.Lock()
	disk := make(map[string]diskEntry, len(c.entries))
	for id, e := range c.entries {
		disk[id] = diskEntry{Models: e.models, FetchedAt: e.fetchedAt}
	}
	c.mu.Unlock()

	data, err := json.Marshal(disk)
	if err != nil {
		return fmt.Errorf("marshal model list cache: %w", err)
	}

	path := filepath.Join(dir, modelListCacheFile)
	// fsutil.WriteFileAtomic uses temp+rename so a crash mid-write never leaves
	// a partial file. SecretPerms (0600) keeps the file owner-readable only —
	// model lists aren't secrets but the dir may also hold key material.
	if err := fsutil.WriteFileAtomic(path, data, fsutil.SecretPerms); err != nil {
		return fmt.Errorf("write model list cache %q: %w", path, err)
	}
	return nil
}
