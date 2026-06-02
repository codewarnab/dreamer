package backgroundjobs

import (
	"path/filepath"
	"testing"
	"time"
)

func TestClassifyProcess(t *testing.T) {
	tests := []struct {
		name     string
		exeName  string
		expected ProcessCategory
	}{
		{"shell powershell", "powershell.exe", CategoryShell},
		{"shell pwsh", "pwsh.exe", CategoryShell},
		{"shell cmd", "cmd.exe", CategoryShell},
		{"shell bash", "bash", CategoryShell},
		{"shell python", "python.exe", CategoryShell},
		{"shell node", "node.exe", CategoryShell},
		{"shell ruby", "ruby", CategoryShell},
		{"shell case insensitive", "PowerShell.EXE", CategoryShell},
		{"provider claude", "claude.exe", CategoryProvider},
		{"provider claude no ext", "claude", CategoryProvider},
		{"provider gemini", "gemini.exe", CategoryProvider},
		{"provider codex", "codex", CategoryProvider},
		{"tool git", "git.exe", CategoryTool},
		{"tool go", "go.exe", CategoryTool},
		{"tool npm", "npm", CategoryTool},
		{"tool cargo", "cargo.exe", CategoryTool},
		{"unknown", "svchost.exe", CategoryUnknown},
		{"unknown random", "some_random_program", CategoryUnknown},
		{"empty", "", CategoryUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyProcess(tt.exeName)
			if got != tt.expected {
				t.Errorf("ClassifyProcess(%q) = %q, want %q", tt.exeName, got, tt.expected)
			}
		})
	}
}

func TestIsShellInterpreter(t *testing.T) {
	tests := []struct {
		name    string
		exeName string
		want    bool
	}{
		{"powershell", "powershell.exe", true},
		{"bash", "bash", true},
		{"python", "python3.exe", true},
		{"node", "node.exe", true},
		{"certutil", "certutil.exe", true},
		{"case insensitive", "Bash", true},
		{"not shell git", "git.exe", false},
		{"not shell claude", "claude.exe", false},
		{"not shell random", "svchost.exe", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsShellInterpreter(tt.exeName)
			if got != tt.want {
				t.Errorf("IsShellInterpreter(%q) = %v, want %v", tt.exeName, got, tt.want)
			}
		})
	}
}

func TestActivityStoreWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewActivityStore(dir, "job-1", "run-1")
	defer store.Close()

	events := []ActivityEvent{
		{
			Timestamp:   time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
			Type:        ActivityTypeProcessStart,
			PID:         1234,
			ProcessName: "git.exe",
			Category:    string(CategoryTool),
		},
		{
			Timestamp:  time.Date(2026, 6, 1, 12, 0, 1, 0, time.UTC),
			Type:       ActivityTypeConnection,
			PID:        1234,
			RemoteAddr: "140.82.112.3:443",
			RemoteHost: "github.com",
			State:      "ESTABLISHED",
		},
		{
			Timestamp:   time.Date(2026, 6, 1, 12, 0, 2, 0, time.UTC),
			Type:        ActivityTypeProcessStart,
			PID:         5678,
			ProcessName: "powershell.exe",
			Category:    string(CategoryShell),
			Detail:      "SHELL_INTERPRETER",
		},
		{
			Timestamp:   time.Date(2026, 6, 1, 12, 0, 3, 0, time.UTC),
			Type:        ActivityTypeProcessExit,
			PID:         5678,
			ProcessName: "powershell.exe",
		},
	}

	for _, ev := range events {
		if err := store.Write(ev); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read back.
	got, err := ReadAllActivity(dir, "job-1", "run-1")
	if err != nil {
		t.Fatalf("ReadAllActivity: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("got %d events, want %d", len(got), len(events))
	}
	for i, ev := range events {
		if got[i].Type != ev.Type {
			t.Errorf("event[%d].Type = %q, want %q", i, got[i].Type, ev.Type)
		}
		if got[i].PID != ev.PID {
			t.Errorf("event[%d].PID = %d, want %d", i, got[i].PID, ev.PID)
		}
		if got[i].ProcessName != ev.ProcessName {
			t.Errorf("event[%d].ProcessName = %q, want %q", i, got[i].ProcessName, ev.ProcessName)
		}
		if got[i].RemoteAddr != ev.RemoteAddr {
			t.Errorf("event[%d].RemoteAddr = %q, want %q", i, got[i].RemoteAddr, ev.RemoteAddr)
		}
		if got[i].Category != ev.Category {
			t.Errorf("event[%d].Category = %q, want %q", i, got[i].Category, ev.Category)
		}
	}
}

func TestReadAllActivityNoFile(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadAllActivity(dir, "nonexistent", "run-1")
	if err != nil {
		t.Fatalf("ReadAllActivity: %v", err)
	}
	if got != nil {
		t.Errorf("got %d events, want nil", len(got))
	}
}

func TestActivityLogPath(t *testing.T) {
	got := ActivityLogPath("/output/runs", "job-abc", "run-xyz")
	want := filepath.Join("/output/runs", "job-abc", "run-xyz"+activityFileExt)
	if got != want {
		t.Errorf("ActivityLogPath = %q, want %q", got, want)
	}
}

func TestActivityStoreCloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	store := NewActivityStore(dir, "job-1", "run-1")

	// Write one event to open the file.
	if err := store.Write(ActivityEvent{
		Timestamp: time.Now().UTC(),
		Type:      ActivityTypeProcessStart,
		PID:       1,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Close twice should not panic or error.
	if err := store.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestActivityStoreWriteBeforeClose(t *testing.T) {
	dir := t.TempDir()
	store := NewActivityStore(dir, "job-1", "run-1")

	// Write without explicit Close — the file should still be readable
	// via deferred cleanup.
	for i := 0; i < 3; i++ {
		if err := store.Write(ActivityEvent{
			Timestamp: time.Now().UTC(),
			Type:      ActivityTypeProcessStart,
			PID:       uint32(i),
		}); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}
	store.Close()

	got, err := ReadAllActivity(dir, "job-1", "run-1")
	if err != nil {
		t.Fatalf("ReadAllActivity: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d events, want 3", len(got))
	}
}
