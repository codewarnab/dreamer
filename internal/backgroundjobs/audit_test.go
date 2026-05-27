package backgroundjobs

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAuditWriter_Write_AppendsLine(t *testing.T) {
	dir := t.TempDir()
	w := NewAuditWriter(dir)

	for i := 0; i < 3; i++ {
		if err := w.Write(AuditEvent{Event: "test", JobID: "abc"}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	lines := readAuditLines(t, dir)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
}

func TestAuditWriter_Write_SetsTimestamp(t *testing.T) {
	dir := t.TempDir()
	w := NewAuditWriter(dir)

	if err := w.Write(AuditEvent{Event: "test"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	events := readAuditEvents(t, dir)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Timestamp.IsZero() {
		t.Error("timestamp should be set automatically")
	}
	if time.Since(events[0].Timestamp) > 10*time.Second {
		t.Error("timestamp should be recent")
	}
}

func TestAuditWriter_Write_PreservesTimestamp(t *testing.T) {
	dir := t.TempDir()
	w := NewAuditWriter(dir)

	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := w.Write(AuditEvent{Timestamp: ts, Event: "test"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	events := readAuditEvents(t, dir)
	if !events[0].Timestamp.Equal(ts) {
		t.Errorf("timestamp = %v, want %v", events[0].Timestamp, ts)
	}
}

func TestAuditWriter_Write_Concurrent(t *testing.T) {
	dir := t.TempDir()
	w := NewAuditWriter(dir)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := w.Write(AuditEvent{Event: "concurrent"}); err != nil {
					t.Errorf("Write: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	events := readAuditEvents(t, dir)
	if len(events) != 1000 {
		t.Errorf("expected 1000 events, got %d", len(events))
	}
}

func TestAuditWriter_Write_AllFields(t *testing.T) {
	dir := t.TempDir()
	w := NewAuditWriter(dir)

	event := AuditEvent{
		Event: "job.run.finish",
		JobID: "abc123",
		Actor: "os-scheduler",
		Details: map[string]any{
			"status":   "completed",
			"duration": 42,
		},
	}
	if err := w.Write(event); err != nil {
		t.Fatalf("Write: %v", err)
	}

	events := readAuditEvents(t, dir)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Event != "job.run.finish" {
		t.Errorf("Event = %q, want %q", e.Event, "job.run.finish")
	}
	if e.JobID != "abc123" {
		t.Errorf("JobID = %q, want %q", e.JobID, "abc123")
	}
	if e.Actor != "os-scheduler" {
		t.Errorf("Actor = %q, want %q", e.Actor, "os-scheduler")
	}
	if e.Details["status"] != "completed" {
		t.Errorf("Details[status] = %v, want %q", e.Details["status"], "completed")
	}
}

func readAuditLines(t *testing.T, dir string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, auditFile))
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if len(scanner.Bytes()) > 0 {
			lines = append(lines, scanner.Text())
		}
	}
	return lines
}

func readAuditEvents(t *testing.T, dir string) []AuditEvent {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, auditFile))
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}
	defer f.Close()
	var events []AuditEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e AuditEvent
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		events = append(events, e)
	}
	return events
}
