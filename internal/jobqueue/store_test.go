package jobqueue

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	s := NewStore(path)

	jobs := []*Job{
		{ID: "aaa", Project: "proj-a", Status: StatusPending},
		{ID: "bbb", Project: "proj-b", Status: StatusCompleted, FindingsAdded: 5},
	}
	if err := s.Save(jobs); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("len = %d, want 2", len(loaded))
	}
	if loaded[0].ID != "aaa" || loaded[0].Project != "proj-a" {
		t.Fatalf("job[0] = %+v", loaded[0])
	}
	if loaded[1].FindingsAdded != 5 {
		t.Fatalf("job[1].FindingsAdded = %d, want 5", loaded[1].FindingsAdded)
	}
}

func TestStoreLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")
	s := NewStore(path)

	jobs, err := s.Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if jobs != nil {
		t.Fatalf("expected nil, got %d jobs", len(jobs))
	}
}

func TestStoreLoadRejectsCorruptContent(t *testing.T) {
	// Table-driven: various malformed inputs that should all return errors.
	tests := []struct {
		name    string
		content string
	}{
		{"invalid JSON", "{not valid json!!!"},
		{"empty file", ""},
		{"partial JSON", `{"version": 1, "jobs": [`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "jobs.json")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			s := NewStore(path)
			_, err := s.Load()
			if err == nil {
				t.Fatal("expected error on corrupt content")
			}
		})
	}
}

func TestStoreLoadRejectsWrongVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"jobs":[]}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	s := NewStore(path)
	_, err := s.Load()
	if err == nil {
		t.Fatal("expected error on wrong version")
	}
}

func TestStorePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	s := NewStore(path)
	if got := s.Path(); got != path {
		t.Fatalf("Path() = %q, want %q", got, path)
	}
}
