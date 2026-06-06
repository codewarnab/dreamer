package analyzer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestGet_ReturnNilOnColdCache(t *testing.T) {
	var c ModelListCache
	if got := c.Get("opencode-server"); got != nil {
		t.Errorf("cold cache: expected nil, got %v", got)
	}
}

func TestGet_ReturnsCachedListWithinTTL(t *testing.T) {
	var c ModelListCache
	models := []string{"model-a", "model-b"}
	c.Set("opencode-server", models)

	got := c.Get("opencode-server")
	if got == nil {
		t.Fatal("expected cached list, got nil")
	}
	if len(got) != len(models) || got[0] != models[0] || got[1] != models[1] {
		t.Errorf("got %v, want %v", got, models)
	}
}

func TestGet_ReturnNilAfterTTLExpires(t *testing.T) {
	var c ModelListCache
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]modelListEntry)
	}
	// Inject a stale entry directly.
	c.entries["opencode-server"] = modelListEntry{
		models:    []string{"stale-model"},
		fetchedAt: time.Now().Add(-(modelListCacheTTL + time.Second)),
	}
	c.mu.Unlock()

	if got := c.Get("opencode-server"); got != nil {
		t.Errorf("stale entry: expected nil, got %v", got)
	}
}

func TestSet_OverwritesPriorEntry(t *testing.T) {
	var c ModelListCache
	c.Set("opencode-server", []string{"old-model"})
	c.Set("opencode-server", []string{"new-model"})

	got := c.Get("opencode-server")
	if got == nil || len(got) != 1 || got[0] != "new-model" {
		t.Errorf("expected [new-model], got %v", got)
	}
}

func TestConcurrentGetSet_NoDataRace(t *testing.T) {
	var c ModelListCache
	var wg sync.WaitGroup
	const goroutines = 50

	for i := 0; i < goroutines; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.Set("opencode-server", []string{"model-x"})
		}()
		go func() {
			defer wg.Done()
			c.Get("opencode-server")
		}()
	}
	wg.Wait()
}

func TestLoad_ReturnNilWhenFileAbsent(t *testing.T) {
	var c ModelListCache
	dir := t.TempDir()
	if err := c.Load(dir); err != nil {
		t.Fatalf("Load on empty dir: unexpected error: %v", err)
	}
	if got := c.Get("any-provider"); got != nil {
		t.Errorf("expected nil after empty Load, got %v", got)
	}
}

func TestLoad_ReturnNilForEmptyDir(t *testing.T) {
	var c ModelListCache
	if err := c.Load(""); err != nil {
		t.Fatalf("Load with empty dir: unexpected error: %v", err)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	models := []string{"claude-sonnet-4.5", "claude-sonnet-4"}

	// Populate and save.
	var src ModelListCache
	src.Set("kiro-acp", models)
	if err := src.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the file was written.
	if _, err := os.Stat(filepath.Join(dir, modelListCacheFile)); err != nil {
		t.Fatalf("cache file not created: %v", err)
	}

	// Load into a fresh cache and verify the models are present.
	var dst ModelListCache
	if err := dst.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := dst.Get("kiro-acp")
	if got == nil {
		t.Fatal("Load: expected models, got nil")
	}
	if len(got) != len(models) || got[0] != models[0] || got[1] != models[1] {
		t.Errorf("Load: got %v, want %v", got, models)
	}
}

func TestLoad_SkipsEntriesOlderThanDiskTTL(t *testing.T) {
	dir := t.TempDir()

	// Write a cache file with a stale entry manually.
	stale := map[string]diskEntry{
		"old-provider": {
			Models:    []string{"old-model"},
			FetchedAt: time.Now().Add(-(modelListDiskTTL + time.Hour)),
		},
	}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal stale entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, modelListCacheFile), data, 0644); err != nil {
		t.Fatalf("write stale cache file: %v", err)
	}

	var c ModelListCache
	if err := c.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The stale entry must not be loaded into the in-memory cache.
	if got := c.Get("old-provider"); got != nil {
		t.Errorf("stale disk entry should be skipped, got %v", got)
	}
}

func TestLoad_ReturnsErrorOnCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, modelListCacheFile), []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	var c ModelListCache
	if err := c.Load(dir); err == nil {
		t.Error("Load: expected error on corrupted file, got nil")
	}
}

func TestSave_ReturnNilForEmptyDir(t *testing.T) {
	var c ModelListCache
	c.Set("kiro-acp", []string{"claude-sonnet-4.5"})
	if err := c.Save(""); err != nil {
		t.Fatalf("Save with empty dir: unexpected error: %v", err)
	}
}
