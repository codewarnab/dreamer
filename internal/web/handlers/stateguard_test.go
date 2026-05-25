package handlers

import (
	"sync"
	"testing"
	"time"
)

func TestProjectLock_SerializesSameProject(t *testing.T) {
	pl := NewProjectLock()

	var order []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			pl.Lock("proj-a")
			defer pl.Unlock("proj-a")
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(order) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(order))
	}
}

func TestProjectLock_AllowsConcurrentDifferentProjects(t *testing.T) {
	pl := NewProjectLock()

	started := make(chan string, 3)
	proceed := make(chan struct{})

	var wg sync.WaitGroup
	for _, proj := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			pl.Lock(p)
			defer pl.Unlock(p)
			started <- p
			<-proceed
		}(proj)
	}

	// Wait for all three to acquire their locks.
	seen := make(map[string]bool)
	for range 3 {
		seen[<-started] = true
	}
	if len(seen) != 3 {
		t.Errorf("expected 3 different projects running concurrently, got %d", len(seen))
	}
	close(proceed)
	wg.Wait()
}

func TestProjectLock_UnlockMissingKeyNoPanic(t *testing.T) {
	pl := NewProjectLock()
	// Should not panic.
	pl.Unlock("never-locked")
}

func TestProjectLock_DefersReleaseOnPanic(t *testing.T) {
	pl := NewProjectLock()

	func() {
		defer func() { recover() }()
		pl.Lock("proj-panic")
		defer pl.Unlock("proj-panic")
		panic("test panic")
	}()

	// Lock should be released — acquiring it again must not deadlock.
	done := make(chan struct{})
	go func() {
		pl.Lock("proj-panic")
		defer pl.Unlock("proj-panic")
		close(done)
	}()

	select {
	case <-done:
		// OK — lock was released after panic.
	case <-time.After(time.Second):
		t.Fatal("deadlock: lock not released after panic")
	}
}

func TestProjectLock_DoubleUnlockNoPanic(t *testing.T) {
	pl := NewProjectLock()
	pl.Lock("proj-x")
	pl.Unlock("proj-x")
	// Second unlock must not panic.
	pl.Unlock("proj-x")
}
