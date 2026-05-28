package pipeline

import (
	"sync"
	"testing"
	"time"

	"dreamer/internal/chat"
)

func TestDiscoveryCacheCheck_Empty(t *testing.T) {
	c := NewDiscoveryCache()
	sources := []chat.Source{{Path: "/a", Tool: "tool", ModifiedTime: time.Now()}}
	if c.Check("/project", sources, "sha1") {
		t.Fatal("fresh cache should return false")
	}
}

func TestDiscoveryCacheCheck_Match(t *testing.T) {
	c := NewDiscoveryCache()
	now := time.Now()
	sources := []chat.Source{
		{Path: "/a", Tool: "tool", ModifiedTime: now},
		{Path: "/b", Tool: "tool", ModifiedTime: now.Add(time.Second)},
	}
	c.Update("/project", sources, "sha1")
	if !c.Check("/project", sources, "sha1") {
		t.Fatal("same sources + SHA should return true")
	}
}

func TestDiscoveryCacheCheck_MtimeChanged(t *testing.T) {
	c := NewDiscoveryCache()
	now := time.Now()
	sources := []chat.Source{{Path: "/a", Tool: "tool", ModifiedTime: now}}
	c.Update("/project", sources, "sha1")

	modified := []chat.Source{{Path: "/a", Tool: "tool", ModifiedTime: now.Add(time.Second)}}
	if c.Check("/project", modified, "sha1") {
		t.Fatal("modified mtime should return false")
	}
}

func TestDiscoveryCacheCheck_SourceAdded(t *testing.T) {
	c := NewDiscoveryCache()
	now := time.Now()
	c.Update("/project", []chat.Source{{Path: "/a", Tool: "tool", ModifiedTime: now}}, "sha1")

	added := []chat.Source{
		{Path: "/a", Tool: "tool", ModifiedTime: now},
		{Path: "/b", Tool: "tool", ModifiedTime: now},
	}
	if c.Check("/project", added, "sha1") {
		t.Fatal("added source should return false")
	}
}

func TestDiscoveryCacheCheck_SourceRemoved(t *testing.T) {
	c := NewDiscoveryCache()
	now := time.Now()
	c.Update("/project", []chat.Source{
		{Path: "/a", Tool: "tool", ModifiedTime: now},
		{Path: "/b", Tool: "tool", ModifiedTime: now},
	}, "sha1")

	removed := []chat.Source{{Path: "/a", Tool: "tool", ModifiedTime: now}}
	if c.Check("/project", removed, "sha1") {
		t.Fatal("removed source should return false")
	}
}

func TestDiscoveryCacheCheck_SHAChanged(t *testing.T) {
	c := NewDiscoveryCache()
	now := time.Now()
	sources := []chat.Source{{Path: "/a", Tool: "tool", ModifiedTime: now}}
	c.Update("/project", sources, "sha1")
	if c.Check("/project", sources, "sha2") {
		t.Fatal("different repo HEAD should return false")
	}
}

func TestDiscoveryCacheUpdate_ThenCheck(t *testing.T) {
	c := NewDiscoveryCache()
	now := time.Now()
	sources := []chat.Source{{Path: "/a", Tool: "tool", ModifiedTime: now}}
	c.Update("/project", sources, "head1")
	if !c.Check("/project", sources, "head1") {
		t.Fatal("round-trip update→check should return true")
	}

	// Update with new state; old check should fail.
	newSources := []chat.Source{{Path: "/a", Tool: "tool", ModifiedTime: now.Add(time.Minute)}}
	c.Update("/project", newSources, "head2")
	if c.Check("/project", sources, "head1") {
		t.Fatal("stale check after update should return false")
	}
	if !c.Check("/project", newSources, "head2") {
		t.Fatal("fresh check after update should return true")
	}
}

func TestDiscoveryCacheConcurrent(t *testing.T) {
	c := NewDiscoveryCache()
	now := time.Now()
	sources := []chat.Source{{Path: "/a", Tool: "tool", ModifiedTime: now}}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.Update("/project", sources, "sha")
		}()
		go func() {
			defer wg.Done()
			c.Check("/project", sources, "sha")
		}()
	}
	wg.Wait()
	// After concurrent updates, a final Check with the last-written state
	// should return true (cache is consistent, not corrupted).
	if !c.Check("/project", sources, "sha") {
		t.Fatal("concurrent Update/Check corrupted the cache")
	}
}
