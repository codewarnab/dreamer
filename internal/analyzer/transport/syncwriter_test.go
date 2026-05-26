package transport

import (
	"sync"
	"testing"
)

func TestSafeBuffer_Write(t *testing.T) {
	var b SafeBuffer
	n, err := b.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("wrote %d bytes, want 5", n)
	}
	if got := b.String(); got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestSafeBuffer_WriteString(t *testing.T) {
	var b SafeBuffer
	n, err := b.WriteString("world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("wrote %d bytes, want 5", n)
	}
	if got := b.String(); got != "world" {
		t.Fatalf("got %q, want %q", got, "world")
	}
}

func TestSafeBuffer_ConcurrentWrite(t *testing.T) {
	var b SafeBuffer
	var wg sync.WaitGroup
	const goroutines = 100
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			b.WriteString("x")
		}()
	}
	wg.Wait()
	if got := b.String(); len(got) != goroutines {
		t.Fatalf("got length %d, want %d", len(got), goroutines)
	}
}

func TestSafeBuffer_ConcurrentWriteAndRead(t *testing.T) {
	var b SafeBuffer
	var wg sync.WaitGroup
	const goroutines = 50
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			b.WriteString("a")
		}()
		go func() {
			defer wg.Done()
			b.Write([]byte("b"))
		}()
	}
	wg.Wait()
	if got := b.String(); len(got) != goroutines*2 {
		t.Fatalf("got length %d, want %d", len(got), goroutines*2)
	}
}

func TestSafeBuffer_ConcurrentStringDuringWrite(t *testing.T) {
	var b SafeBuffer
	var wg sync.WaitGroup
	const goroutines = 100
	wg.Add(goroutines * 2)
	// Writers keep appending while readers call String() concurrently.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			b.WriteString("x")
		}()
		go func() {
			defer wg.Done()
			_ = b.String() // must not panic or corrupt
		}()
	}
	wg.Wait()
	// All writes completed; length must be exactly goroutines.
	if got := b.String(); len(got) != goroutines {
		t.Fatalf("got length %d, want %d", len(got), goroutines)
	}
}

func TestSafeBuffer_EmptyString(t *testing.T) {
	var b SafeBuffer
	if got := b.String(); got != "" {
		t.Fatalf("empty buffer: got %q, want %q", got, "")
	}
}

func TestSafeBuffer_Len(t *testing.T) {
	var b SafeBuffer
	if got := b.Len(); got != 0 {
		t.Fatalf("empty Len = %d, want 0", got)
	}
	b.WriteString("hello")
	if got := b.Len(); got != 5 {
		t.Fatalf("Len after Write = %d, want 5", got)
	}
}

func TestSafeBuffer_Reset(t *testing.T) {
	var b SafeBuffer
	b.WriteString("hello")
	b.Reset()
	if got := b.Len(); got != 0 {
		t.Fatalf("Len after Reset = %d, want 0", got)
	}
	if got := b.String(); got != "" {
		t.Fatalf("String after Reset = %q, want empty", got)
	}
}

func TestSafeBuffer_LenConcurrent(t *testing.T) {
	var b SafeBuffer
	var wg sync.WaitGroup
	const goroutines = 100
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			b.WriteString("x")
		}()
		go func() {
			defer wg.Done()
			_ = b.Len()
		}()
	}
	wg.Wait()
	if got := b.Len(); got != goroutines {
		t.Fatalf("Len = %d, want %d", got, goroutines)
	}
}
