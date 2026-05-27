package backgroundjobs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	mu  sync.Mutex
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
	return nil
}
