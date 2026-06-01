package lockordertest

import (
	"io"
	"os"

	"fsutil"
)

// Lock acquired first, then read — correct order.
func goodLockBeforeRead() {
	release, _ := fsutil.AcquireLock("/tmp/test.lock")
	data := readAll()
	_ = data
	_ = release
}

// os.ReadFile after lock — correct order.
func goodReadFileAfterLock() {
	release, _ := fsutil.AcquireLock("/tmp/test.lock")
	data, _ := os.ReadFile("/tmp/test.json")
	_ = data
	_ = release
}

// os.Open after lock — correct order.
func goodOpenAfterLock() {
	release, _ := fsutil.AcquireLock("/tmp/test.lock")
	f, _ := os.Open("/tmp/test.json")
	_ = f
	_ = release
}

// io.ReadAll after lock — correct order.
func goodReadAllAfterLock() {
	release, _ := fsutil.AcquireLock("/tmp/test.lock")
	data, _ := io.ReadAll(os.Stdin)
	_ = data
	_ = release
}

// No lock at all — no TOCTOU risk to flag.
func goodNoLock() {
	data := readAll()
	_ = data
}

// Read from an in-memory source — not a file read, no TOCTOU risk.
func goodMemoryRead() {
	release, _ := fsutil.AcquireLock("/tmp/test.lock")
	buf := make([]byte, 100)
	_ = buf
	_ = release
}
