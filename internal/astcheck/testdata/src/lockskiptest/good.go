package lockskiptest

import "sync"

// goodLocked: exported method correctly locks before calling helper.
type goodStore struct {
	mu   sync.Mutex
	data []byte
}

func (s *goodStore) load() []byte {
	return s.data
}

func (s *goodStore) Update() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = s.load()
}

func (s *goodStore) Load() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// goodNoHelper: exported method doesn't call any unexported helpers.
type goodDirect struct {
	mu   sync.Mutex
	data string
}

func (d *goodDirect) Get() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.data
}

// goodNoMutex: no mutex field — no lock concern.
type plainStore struct {
	data []byte
}

func (p *plainStore) load() []byte {
	return p.data
}

func (p *plainStore) Load() []byte {
	return p.load()
}

// goodUnexported: unexported method calling helper without lock is fine —
// callers are responsible for locking at the public API boundary.
type goodInternal struct {
	mu   sync.Mutex
	data int
}

func (i *goodInternal) read() int {
	return i.data
}

func (i *goodInternal) snapshot() int {
	return i.read()
}

func (i *goodInternal) Get() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.snapshot()
}

// goodCallExported: exported method calling another exported method is fine —
// each exported method manages its own locking.
type goodChain struct {
	mu   sync.Mutex
	data int
}

func (c *goodChain) Get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data
}

func (c *goodChain) Double() int {
	return c.Get() * 2
}
