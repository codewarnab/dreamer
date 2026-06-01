package lockskiptest

import "sync"

// badStore mirrors the B7 bug pattern: exported Load() calls unexported
// load() without the lock, while Update() correctly locks first.
type badStore struct {
	mu   sync.Mutex
	data []byte
}

func (s *badStore) load() []byte {
	return s.data
}

func (s *badStore) Update() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = s.load()
}

func (s *badStore) Load() []byte { // want "exported method Load calls unexported load without holding the lock"
	return s.load()
}

// badRWMutex: same pattern with sync.RWMutex.
type badReader struct {
	mu   sync.RWMutex
	data string
}

func (r *badReader) read() string {
	return r.data
}

func (r *badReader) Refresh() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = r.read()
}

func (r *badReader) Read() string { // want "exported method Read calls unexported read without holding the lock"
	return r.read()
}

// badMultiHelper: exported method calls multiple unlocked helpers.
type badMulti struct {
	mu      sync.Mutex
	a, b, c int
}

func (m *badMulti) sum() int   { return m.a + m.b }
func (m *badMulti) count() int { return m.c }

func (m *badMulti) Recalc() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.a = m.sum()
	m.c = m.count()
}

func (m *badMulti) Total() int { // want "exported method Total calls unexported sum without holding the lock"
	return m.sum()
}
