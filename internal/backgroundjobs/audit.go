package backgroundjobs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"dreamer/internal/fsutil"
)

const auditFile = "audit.jsonl"

// AuditEvent is a single entry in the append-only audit log.
type AuditEvent struct {
	Timestamp time.Time      `json:"timestamp"`
	Event     string         `json:"event"`
	JobID     string         `json:"job_id,omitempty"`
	Actor     string         `json:"actor,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// AuditWriter appends JSONL audit events to <storeDir>/audit.jsonl.
type AuditWriter struct {
	dir string
	mu  sync.RWMutex
}

// NewAuditWriter creates an AuditWriter rooted at storeDir.
func NewAuditWriter(storeDir string) *AuditWriter {
	return &AuditWriter{dir: storeDir}
}

// Write appends a single audit event. Thread-safe.
func (w *AuditWriter) Write(event AuditEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	data = append(data, '\n')

	// O_APPEND on Linux/Windows guarantees atomic appends for writes
	// smaller than the filesystem block size (typically 4 KiB). Each
	// audit event is well under that limit, so a cross-process lock is
	// not needed. The in-process mutex protects the MkdirAll + OpenFile
	// sequence from races between goroutines.
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := os.MkdirAll(w.dir, fsutil.DirPerms); err != nil {
		return fmt.Errorf("create audit dir: %w", err)
	}
	path := filepath.Join(w.dir, auditFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fsutil.FilePerms)
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync audit event: %w", err)
	}
	return nil
}

// ReadAll reads audit events from the JSONL file, newest-first.
// Skips malformed lines. Returns empty slice when the file does not exist.
// When limit > 0, returns at most that many events.
func (w *AuditWriter) ReadAll(limit int) ([]AuditEvent, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	path := filepath.Join(w.dir, auditFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []AuditEvent{}, nil
		}
		return nil, fmt.Errorf("open audit file: %w", err)
	}
	defer f.Close()

	var events []AuditEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue // skip malformed lines
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("read audit file: %w", err)
	}

	// Sort newest-first.
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})

	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}
