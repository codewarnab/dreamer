// Package backgroundjobs — activity monitoring for provider child processes.
//
// Activity monitoring captures what happened during a background job run:
// which child processes were spawned and what network connections were made.
// This is the audit trail that lets users detect when a provider executed
// shell interpreters or made unexpected outbound connections.
//
// The implementation is platform-specific:
//   - activity_windows.go: IO completion port (event-based, zero polling) + TCP snapshot
//   - activity_other.go: no-op stub (Linux/macOS have stronger OS sandbox)
package backgroundjobs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dreamer/internal/fsutil"
)

// Activity event type constants. These are the values of ActivityEvent.Type.
const (
	ActivityTypeProcessStart = "process_start"
	ActivityTypeProcessExit  = "process_exit"
	ActivityTypeConnection   = "connection"
)

// activityFileExt is the file extension for per-run activity logs.
const activityFileExt = ".activity.jsonl"

// ActivityEvent is a single entry in the per-run activity log.
// Captures either a process event or a network connection event.
type ActivityEvent struct {
	Timestamp time.Time `json:"ts"`
	// Type is one of: "process_start", "process_exit", "connection".
	Type string `json:"type"`
	// ProcessName is the executable name (e.g. "powershell.exe").
	ProcessName string `json:"exe,omitempty"`
	// PID is the process identifier.
	PID uint32 `json:"pid,omitempty"`
	// ParentPID is the parent process identifier.
	ParentPID uint32 `json:"ppid,omitempty"`
	// ExitCode is set for process_exit events.
	ExitCode *uint32 `json:"exit_code,omitempty"`
	// RemoteAddr is the remote IP address:port for connection events.
	RemoteAddr string `json:"remote_addr,omitempty"`
	// RemoteHost is the reverse-DNS hostname for connection events.
	// Empty when reverse DNS fails or is not attempted.
	RemoteHost string `json:"remote_host,omitempty"`
	// State is the TCP connection state (e.g. "ESTABLISHED").
	State string `json:"state,omitempty"`
	// Detail carries extra context (e.g. "SHELL_INTERPRETER").
	Detail string `json:"detail,omitempty"`
	// Category classifies the process for the UI color coding.
	// One of: "provider", "tool", "shell", "unknown".
	Category string `json:"cat,omitempty"`
}

// ProcessCategory classifies a process for UI color coding.
type ProcessCategory string

const (
	CategoryProvider ProcessCategory = "provider" // the main provider binary
	CategoryTool     ProcessCategory = "tool"     // expected child tools (git, go, make)
	CategoryShell    ProcessCategory = "shell"    // shell interpreters (powershell, bash, python)
	CategoryUnknown  ProcessCategory = "unknown"  // unrecognized process
)

// knownShellInterpreters is the set of executable names (lowercase, without
// extension) that are considered shell interpreters. A provider spawning these
// can execute arbitrary code, which is the primary sandbox escape vector.
var knownShellInterpreters = map[string]bool{
	"powershell": true,
	"pwsh":       true,
	"cmd":        true,
	"bash":       true,
	"sh":         true,
	"zsh":        true,
	"ksh":        true,
	"fish":       true,
	"csh":        true,
	"tcsh":       true,
	"python":     true,
	"python3":    true,
	"node":       true,
	"ruby":       true,
	"perl":       true,
	"tclsh":      true,
	"wscript":    true,
	"cscript":    true,
	"mshta":      true,
	"certutil":   true,
	"regsvr32":   true,
	"rundll32":   true,
}

// knownProviderBinaries is the set of executable names (lowercase, without
// extension) that are expected provider binaries.
var knownProviderBinaries = map[string]bool{
	"claude":         true,
	"claude.exe":     true,
	"openclaude":     true,
	"openclaude.exe": true,
	"gemini":         true,
	"gemini.exe":     true,
	"codex":          true,
	"codex.exe":      true,
}

// knownChildTools is the set of executable names (lowercase, without
// extension) that are commonly spawned by providers for legitimate tasks.
var knownChildTools = map[string]bool{
	"git":   true,
	"go":    true,
	"make":  true,
	"npm":   true,
	"npx":   true,
	"cargo": true,
	"rustc": true,
	"tsc":   true,
	"grep":  true,
	"rg":    true,
	"find":  true,
	"ls":    true,
	"cat":   true,
	"head":  true,
	"tail":  true,
	"wc":    true,
	"sort":  true,
	"jq":    true,
	"curl":  true,
	"wget":  true,
}

// ClassifyProcess returns the category for a process based on its executable name.
// The exeName should be the base filename (e.g. "powershell.exe").
func ClassifyProcess(exeName string) ProcessCategory {
	lower := strings.ToLower(exeName)
	name := strings.TrimSuffix(lower, ".exe")

	if knownShellInterpreters[name] {
		return CategoryShell
	}
	if knownProviderBinaries[lower] || knownProviderBinaries[name] {
		return CategoryProvider
	}
	if knownChildTools[name] {
		return CategoryTool
	}
	return CategoryUnknown
}

// IsShellInterpreter reports whether the executable is a known shell interpreter.
func IsShellInterpreter(exeName string) bool {
	lower := strings.ToLower(exeName)
	name := strings.TrimSuffix(lower, ".exe")
	return knownShellInterpreters[name]
}

// ActivityStore writes activity events to a per-run JSONL file.
// The store is bound to a single (jobID, runID) pair; the caller
// creates one store per run and passes it to the platform monitor.
// The file handle is opened lazily on first Write and kept open
// until Close is called. Callers must call Close when done.
type ActivityStore struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

// NewActivityStore creates a store bound to a specific run.
// The file is <runsDir>/<jobID>/<runID>.activity.jsonl.
func NewActivityStore(runsDir, jobID, runID string) *ActivityStore {
	dir := filepath.Join(runsDir, jobID)
	return &ActivityStore{
		path: filepath.Join(dir, runID+activityFileExt),
	}
}

// Write appends an activity event to the per-run JSONL file.
func (s *ActivityStore) Write(event ActivityEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.f == nil {
		dir := filepath.Dir(s.path)
		if err := os.MkdirAll(dir, fsutil.DirPerms); err != nil {
			return fmt.Errorf("activity: create dir: %w", err)
		}
		f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fsutil.FilePerms)
		if err != nil {
			return fmt.Errorf("activity: open log: %w", err)
		}
		s.f = f
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("activity: marshal event: %w", err)
	}
	data = append(data, '\n')

	if _, err := s.f.Write(data); err != nil {
		return fmt.Errorf("activity: write event: %w", err)
	}
	return nil
}

// Close syncs and closes the underlying file. Safe to call multiple times.
func (s *ActivityStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Sync()
	if closeErr := s.f.Close(); err == nil {
		err = closeErr
	}
	s.f = nil
	return err
}

// ReadAll reads all activity events for a run.
func ReadAllActivity(runsDir, jobID, runID string) ([]ActivityEvent, error) {
	path := filepath.Join(runsDir, jobID, runID+activityFileExt)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("activity: open log: %w", err)
	}
	defer f.Close()

	var events []ActivityEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev ActivityEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // skip malformed lines
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("activity: scan log: %w", err)
	}
	return events, nil
}

// ActivityLogPath returns the path to the activity log for a run.
func ActivityLogPath(runsDir, jobID, runID string) string {
	return filepath.Join(runsDir, jobID, runID+activityFileExt)
}
