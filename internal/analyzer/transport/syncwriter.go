package transport

import (
	"io"
	"strings"
	"sync"
)

// SafeBuffer is a mutex-protected strings.Builder safe for concurrent
// writes from multiple goroutines (e.g., OS pipe driver + error handler).
type SafeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

var _ io.Writer = (*SafeBuffer)(nil)

// Write implements io.Writer. Safe for concurrent use.
func (b *SafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// WriteString writes s to the buffer. Safe for concurrent use.
func (b *SafeBuffer) WriteString(s string) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.WriteString(s)
}

// String returns the accumulated contents. Safe for concurrent use.
func (b *SafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Len returns the number of accumulated bytes. Safe for concurrent use.
func (b *SafeBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// Reset resets the buffer to empty. Safe for concurrent use.
func (b *SafeBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}
