package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/mcpserver"
)

// recordfinding's contract: model-correctable failures return ok:false on
// stdout with a nil error (cobra exits 0). Protocol failures return an
// error (cobra exits non-zero). These tests pin that boundary.

func newValidFindingJSON(t *testing.T) []byte {
	t.Helper()
	encoded, err := json.Marshal(mcpserver.FindingInput{
		Category:   "test",
		Mistake:    "missing edge-case test",
		Confidence: 0.9,
		Guardrail:  mcpserver.GuardrailInput{Kind: "test", Tool: "go test", Rule: "TestX"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}

func TestRunRecordFinding_Success(t *testing.T) {
	out := filepath.Join(t.TempDir(), "findings.jsonl")
	var stdout bytes.Buffer
	if err := runRecordFinding(out, bytes.NewReader(newValidFindingJSON(t)), &stdout); err != nil {
		t.Fatalf("runRecordFinding: %v", err)
	}
	var result mcpserver.RecordResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		t.Fatalf("unmarshal stdout: %v (raw=%q)", err, stdout.String())
	}
	if !result.OK || result.Total != 1 {
		t.Fatalf("expected ok:true total:1, got %+v", result)
	}
}

func TestRunRecordFinding_EmptyStdin(t *testing.T) {
	out := filepath.Join(t.TempDir(), "findings.jsonl")
	var stdout bytes.Buffer
	if err := runRecordFinding(out, bytes.NewReader(nil), &stdout); err != nil {
		t.Fatalf("model-correctable failure should not error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"ok":false`) {
		t.Fatalf("expected ok:false on stdout, got %q", stdout.String())
	}
}

func TestRunRecordFinding_OversizedStdin(t *testing.T) {
	out := filepath.Join(t.TempDir(), "findings.jsonl")
	huge := bytes.Repeat([]byte{'x'}, maxStdinBytes+1)
	var stdout bytes.Buffer
	if err := runRecordFinding(out, bytes.NewReader(huge), &stdout); err != nil {
		t.Fatalf("oversize should be model-correctable, got error: %v", err)
	}
	if !strings.Contains(stdout.String(), "input too large") {
		t.Fatalf("expected oversize message on stdout, got %q", stdout.String())
	}
}

func TestRunRecordFinding_InvalidJSON(t *testing.T) {
	out := filepath.Join(t.TempDir(), "findings.jsonl")
	var stdout bytes.Buffer
	if err := runRecordFinding(out, bytes.NewReader([]byte("not json")), &stdout); err != nil {
		t.Fatalf("invalid JSON should be model-correctable, got error: %v", err)
	}
	if !strings.Contains(stdout.String(), "invalid JSON") {
		t.Fatalf("expected invalid-JSON message on stdout, got %q", stdout.String())
	}
}

func TestRunRecordFinding_ValidationFailure(t *testing.T) {
	out := filepath.Join(t.TempDir(), "findings.jsonl")
	bad, _ := json.Marshal(mcpserver.FindingInput{
		Category:   "nonexistent",
		Mistake:    "x",
		Confidence: 0.5,
		Guardrail:  mcpserver.GuardrailInput{Kind: "x", Tool: "y", Rule: "z"},
	})
	var stdout bytes.Buffer
	if err := runRecordFinding(out, bytes.NewReader(bad), &stdout); err != nil {
		t.Fatalf("validation failure should be model-correctable, got error: %v", err)
	}
	if !strings.Contains(stdout.String(), "invalid category") {
		t.Fatalf("expected category error on stdout, got %q", stdout.String())
	}
}

func TestRunRecordFinding_BadOutputPath(t *testing.T) {
	var stdout bytes.Buffer
	// Path outside os.TempDir() → ValidateOutputPath rejects → protocol error.
	err := runRecordFinding("/etc/passwd", bytes.NewReader(newValidFindingJSON(t)), &stdout)
	if err == nil {
		t.Fatal("expected non-nil error for bad output path")
	}
}
