package analyzer

import (
	"context"
	"errors"
	"sync"
)

// sequentialPoolCap is the SessionPool cap used in sequential mode.
const sequentialPoolCap = 1

// SessionPool owns at most `capacity` concurrently-live Sessions for one
// pipeline.Run. Callers Acquire, do one prompt, then Release; passing
// ok=false discards a session known broken.
type SessionPool struct {
	factory  func() (Session, error)
	capacity int

	mu        sync.Mutex
	cond      *sync.Cond
	live      int
	free      []Session
	all       []Session
	closed    bool
	allClosed bool

	// factoryMu serializes factory() calls so concurrent Acquires
	// never invoke the caller-supplied factory in parallel.
	factoryMu sync.Mutex
}

// NewSessionPool builds a pool. capacity<=0 falls back to sequentialPoolCap (1).
func NewSessionPool(capacity int, factory func() (Session, error)) *SessionPool {
	if capacity <= 0 {
		capacity = sequentialPoolCap
	}
	p := &SessionPool{factory: factory, capacity: capacity}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Acquire returns a usable session, blocking until one is free or ctx cancels.
func (p *SessionPool) Acquire(ctx context.Context) (Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			p.mu.Lock()
			p.cond.Broadcast()
			p.mu.Unlock()
		case <-stop:
		}
	}()

	p.mu.Lock()
	for {
		if p.closed {
			p.mu.Unlock()
			return nil, errors.New("session pool closed")
		}
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		if n := len(p.free); n > 0 {
			s := p.free[n-1]
			p.free = p.free[:n-1]
			p.mu.Unlock()
			return s, nil
		}
		if p.live < p.capacity {
			p.live++
			p.mu.Unlock()
			// Serialize factory calls; callers (and tests) may use
			// non-thread-safe factories.
			p.factoryMu.Lock()
			s, err := p.factory()
			p.factoryMu.Unlock()
			if err != nil {
				p.mu.Lock()
				p.live--
				p.cond.Broadcast()
				p.mu.Unlock()
				return nil, err
			}
			p.mu.Lock()
			// Pool may have been closed during the factory call. Discard
			// the fresh session rather than handing back something Close
			// has already orphaned.
			if p.closed {
				p.live--
				p.cond.Broadcast()
				p.mu.Unlock()
				_ = s.Close()
				return nil, errors.New("session pool closed")
			}
			p.all = append(p.all, s)
			p.mu.Unlock()
			return s, nil
		}
		p.cond.Wait()
	}
}

// Release returns a session to the pool. ok=false closes and discards it.
func (p *SessionPool) Release(s Session, ok bool) {
	if s == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !ok || p.closed {
		_ = s.Close()
		p.removeFromAllLocked(s)
		p.live--
		p.cond.Broadcast()
		return
	}
	p.free = append(p.free, s)
	p.cond.Broadcast()
}

// Close shuts down every session the pool created. Idempotent.
func (p *SessionPool) Close() error {
	p.mu.Lock()
	if p.allClosed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.allClosed = true
	sessions := p.all
	p.all = nil
	p.free = nil
	p.cond.Broadcast()
	p.mu.Unlock()

	var firstErr error
	for _, s := range sessions {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (p *SessionPool) removeFromAllLocked(s Session) {
	for i, candidate := range p.all {
		if candidate == s {
			p.all = append(p.all[:i], p.all[i+1:]...)
			return
		}
	}
}
