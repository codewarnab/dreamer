package analyzer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakePoolSession struct {
	closed bool
}

func (s *fakePoolSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	return "ok", nil
}
func (s *fakePoolSession) Close() error { s.closed = true; return nil }

func TestSessionPoolSequentialReuses(t *testing.T) {
	var built int32
	factory := func() (Session, error) {
		atomic.AddInt32(&built, 1)
		return &fakePoolSession{}, nil
	}
	pool := NewSessionPool(1, factory)
	defer pool.Close()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		s, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		pool.Release(s, true)
	}
	if got := atomic.LoadInt32(&built); got != 1 {
		t.Fatalf("factory invocations = %d, want 1 (sequential reuse)", got)
	}
}

func TestSessionPoolParallelBounded(t *testing.T) {
	var live, peak int32
	factory := func() (Session, error) {
		now := atomic.AddInt32(&live, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if now <= old || atomic.CompareAndSwapInt32(&peak, old, now) {
				break
			}
		}
		return &fakePoolSession{}, nil
	}
	pool := NewSessionPool(3, factory)
	defer pool.Close()

	var wg sync.WaitGroup
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := pool.Acquire(ctx)
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt32(&live, -1)
			pool.Release(s, true)
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&peak); got > 3 {
		t.Fatalf("peak live sessions = %d, want <= 3", got)
	}
}

func TestSessionPoolReleaseBrokenDiscards(t *testing.T) {
	var built int32
	factory := func() (Session, error) {
		atomic.AddInt32(&built, 1)
		return &fakePoolSession{}, nil
	}
	pool := NewSessionPool(2, factory)
	defer pool.Close()

	ctx := context.Background()
	s, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	pool.Release(s, false)
	s2, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire after broken release: %v", err)
	}
	pool.Release(s2, true)
	if got := atomic.LoadInt32(&built); got != 2 {
		t.Fatalf("factory invocations = %d, want 2 (broken session must not be reused)", got)
	}
}

func TestSessionPoolAcquireCancelled(t *testing.T) {
	factory := func() (Session, error) { return &fakePoolSession{}, nil }
	pool := NewSessionPool(1, factory)
	defer pool.Close()

	s, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer pool.Release(s, true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pool.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire with cancelled ctx err = %v, want context.Canceled", err)
	}
}
