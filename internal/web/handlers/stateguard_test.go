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
			unlock := pl.Lock("proj-a")
			defer unlock()
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
			unlock := pl.Lock(p)
			defer unlock()
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

func TestProjectLock_ReleaseClosureIdempotent(t *testing.T) {
	pl := NewProjectLock()
	unlock := pl.Lock("proj-x")
	unlock()
	// Second call is a no-op thanks to sync.Once.
	unlock()
}

func TestProjectLock_DefersReleaseOnPanic(t *testing.T) {
	pl := NewProjectLock()

	func() {
		defer func() { recover() }()
		unlock := pl.Lock("proj-panic")
		defer unlock()
		panic("test panic")
	}()

	// Lock should be released — acquiring it again must not deadlock.
	done := make(chan struct{})
	go func() {
		unlock := pl.Lock("proj-panic")
		defer unlock()
		close(done)
	}()

	select {
	case <-done:
		// OK — lock was released after panic.
	case <-time.After(time.Second):
		t.Fatal("deadlock: lock not released after panic")
	}
}

// TestProjectLock_MutualExclusion verifies that two goroutines cannot
// hold the same project lock simultaneously. Each goroutine increments
// a shared counter 1000 times while holding the lock; the final value
// must be exactly 2000.
func TestProjectLock_MutualExclusion(t *testing.T) {
	pl := NewProjectLock()

	counter := 0
	var mu sync.Mutex // protects counter only for reads after both goroutines finish
	var wg sync.WaitGroup

	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				unlock := pl.Lock("proj-exclusive")
				counter++
				unlock()
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if counter != 2000 {
		t.Fatalf("counter = %d, want 2000 (lock did not enforce mutual exclusion)", counter)
	}
}
