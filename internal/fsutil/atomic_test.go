package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

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
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
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

func TestWriteFileAtomicCleansUpStaleTempFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(target+".tmp", []byte("crashed mid-write"), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}
	if err := WriteFileAtomic(target, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("stale .tmp must be replaced by rename; stat err=%v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "ok" {
		t.Fatalf("content = %q, want %q", got, "ok")
	}
}
