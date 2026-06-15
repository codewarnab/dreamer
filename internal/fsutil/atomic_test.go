package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWriteFileAtomicAppliesPerm guards the #52 regression where the perm
// argument was silently dropped and every file became 0o600 (os.CreateTemp's
// default). The temp file must be Chmod'd to the requested perm before the
// rename. Skipped on Windows, where Go's Chmod only toggles the read-only bit
// and Unix mode bits are not meaningful.
func TestWriteFileAtomicAppliesPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes not meaningful on Windows")
	}
	dir := t.TempDir()

	for _, perm := range []os.FileMode{0o600, 0o644, 0o755} {
		target := filepath.Join(dir, "perm.txt")
		if err := WriteFileAtomic(target, []byte("x"), perm); err != nil {
			t.Fatalf("WriteFileAtomic(perm=%o): %v", perm, err)
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if got := info.Mode().Perm(); got != perm {
			t.Fatalf("mode = %o, want %o", got, perm)
		}
	}
}

func TestWriteFileAtomicCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")

	if err := WriteFileAtomic(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
	// Verify no temp files remain (pattern: out.txt.tmp.*).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "out.txt" {
			t.Fatalf("stale temp file present: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WriteFileAtomic(target, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want %q", got, "new")
	}
}

func TestWriteFileAtomicPreservesPreExistingTempFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	existingTemp := target + ".tmp.12345"
	if err := os.WriteFile(existingTemp, []byte("in-flight writer"), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}

	if err := WriteFileAtomic(target, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "ok" {
		t.Fatalf("content = %q, want %q", got, "ok")
	}
	got, err = os.ReadFile(existingTemp)
	if err != nil {
		t.Fatalf("pre-existing temp was removed: %v", err)
	}
	if string(got) != "in-flight writer" {
		t.Fatalf("pre-existing temp content = %q, want %q", got, "in-flight writer")
	}
}
