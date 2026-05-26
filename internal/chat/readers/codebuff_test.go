package readers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadCodebuffMessagesUserAndAI(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat-messages.json")

	content := `[
		{"id":"m1","variant":"user","content":"fix the bug","timestamp":"2026-05-19T10:00:00Z"},
		{"id":"m2","variant":"ai","content":"I found the issue","timestamp":"2026-05-19T10:01:00Z"}
	]`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := ReadCodebuffMessages(file)
	if err != nil {
		t.Fatalf("ReadCodebuffMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}

	if msgs[0].Role != "user" {
		t.Errorf("msgs[0].Role = %q, want %q", msgs[0].Role, "user")
	}
	if msgs[0].Content != "fix the bug" {
		t.Errorf("msgs[0].Content = %q, want %q", msgs[0].Content, "fix the bug")
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("msgs[1].Role = %q, want %q", msgs[1].Role, "assistant")
	}
}

func TestReadCodebuffMessagesAgentVariant(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat-messages.json")

	content := `[
		{"id":"m1","variant":"user","content":"test","timestamp":"2026-05-19T10:00:00Z"},
		{"id":"m2","variant":"agent","content":"agent response","timestamp":"2026-05-19T10:00:05Z"}
	]`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := ReadCodebuffMessages(file)
	if err != nil {
		t.Fatalf("ReadCodebuffMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("msgs[1].Role = %q, want %q", msgs[1].Role, "assistant")
	}
}

func TestReadCodebuffMessagesSkipsErrorVariant(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat-messages.json")

	content := `[
		{"id":"m1","variant":"user","content":"test","timestamp":"2026-05-19T10:00:00Z"},
		{"id":"m2","variant":"error","content":"something failed","timestamp":"2026-05-19T10:00:01Z"},
		{"id":"m3","variant":"ai","content":"ok","timestamp":"2026-05-19T10:00:02Z"}
	]`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := ReadCodebuffMessages(file)
	if err != nil {
		t.Fatalf("ReadCodebuffMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
}

func TestReadCodebuffMessagesEmptyContentSkipped(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat-messages.json")

	content := `[
		{"id":"m1","variant":"user","content":"test","timestamp":"2026-05-19T10:00:00Z"},
		{"id":"m2","variant":"ai","content":"","timestamp":"2026-05-19T10:00:01Z"}
	]`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := ReadCodebuffMessages(file)
	if err != nil {
		t.Fatalf("ReadCodebuffMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
}

func TestReadCodebuffMessagesBlockTextFallback(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat-messages.json")

	content := `[
		{"id":"m1","variant":"user","content":"test","timestamp":"2026-05-19T10:00:00Z"},
		{"id":"m2","variant":"ai","content":"","blocks":[{"type":"text","content":"extracted from blocks"}],"timestamp":"2026-05-19T10:00:01Z"}
	]`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := ReadCodebuffMessages(file)
	if err != nil {
		t.Fatalf("ReadCodebuffMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[1].Content != "extracted from blocks" {
		t.Errorf("msgs[1].Content = %q, want %q", msgs[1].Content, "extracted from blocks")
	}
}

func TestReadCodebuffMessagesTimestampParsing(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat-messages.json")

	content := `[
		{"id":"m1","variant":"user","content":"test","timestamp":"2026-05-19T10:00:00.123456789Z"}
	]`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := ReadCodebuffMessages(file)
	if err != nil {
		t.Fatalf("ReadCodebuffMessages: %v", err)
	}
	expected := time.Date(2026, 5, 19, 10, 0, 0, 123456789, time.UTC)
	if !msgs[0].Timestamp.Equal(expected) {
		t.Errorf("timestamp = %v, want %v", msgs[0].Timestamp, expected)
	}
}

func TestReadCodebuffMessagesFileNotFound(t *testing.T) {
	_, err := ReadCodebuffMessages("/nonexistent/chat-messages.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadCodebuffMessagesInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat-messages.json")
	if err := os.WriteFile(file, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadCodebuffMessages(file)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestReadCodebuffMessagesEmptyArray(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat-messages.json")
	if err := os.WriteFile(file, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := ReadCodebuffMessages(file)
	if err != nil {
		t.Fatalf("ReadCodebuffMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("got %d messages, want 0", len(msgs))
	}
}

func TestReadCodebuffMessagesAgentBlockExtraction(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat-messages.json")

	content := `[
		{"id":"m1","variant":"user","content":"run tests","timestamp":"2026-05-19T10:00:00Z"},
		{"id":"m2","variant":"ai","content":"","blocks":[
			{"type":"agent","content":"analyzing codebase","blocks":[
				{"type":"text","content":"found 3 issues"}
			]},
			{"type":"text","content":"done"}
		],"timestamp":"2026-05-19T10:01:00Z"}
	]`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := ReadCodebuffMessages(file)
	if err != nil {
		t.Fatalf("ReadCodebuffMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}

	want := "analyzing codebase\nfound 3 issues\ndone"
	if msgs[1].Content != want {
		t.Errorf("msgs[1].Content = %q, want %q", msgs[1].Content, want)
	}
}

// ---------------------------------------------------------------------------
// parseCodebuffTimestamp: invalid string, empty string, RFC3339 fallback
// ---------------------------------------------------------------------------

func TestParseCodebuffTimestampInvalidString(t *testing.T) {
	got := parseCodebuffTimestamp("not a timestamp")
	if !got.IsZero() {
		t.Errorf("expected zero time for invalid string, got %v", got)
	}
}

func TestParseCodebuffTimestampEmpty(t *testing.T) {
	got := parseCodebuffTimestamp("")
	if !got.IsZero() {
		t.Errorf("expected zero time for empty string, got %v", got)
	}
}

func TestParseCodebuffTimestampRFC3339NoNanos(t *testing.T) {
	got := parseCodebuffTimestamp("2026-05-19T10:00:00Z")
	if got.IsZero() {
		t.Error("expected non-zero time for valid RFC3339")
	}
	if got.Year() != 2026 {
		t.Errorf("year = %d, want 2026", got.Year())
	}
}

func TestParseCodebuffTimestampWhitespacePadded(t *testing.T) {
	got := parseCodebuffTimestamp("  2026-05-19T10:00:00Z  ")
	if got.IsZero() {
		t.Error("expected non-zero time for whitespace-padded timestamp")
	}
}

// ---------------------------------------------------------------------------
// codebuffRoleFromVariant: edge cases
// ---------------------------------------------------------------------------

func TestCodebuffRoleFromVariantUnknown(t *testing.T) {
	if got := codebuffRoleFromVariant("system"); got != "" {
		t.Errorf("got %q, want empty for system variant", got)
	}
	if got := codebuffRoleFromVariant(""); got != "" {
		t.Errorf("got %q, want empty for empty variant", got)
	}
	if got := codebuffRoleFromVariant("  USER  "); got != "user" {
		t.Errorf("got %q, want user for case-insensitive USER", got)
	}
}

// ---------------------------------------------------------------------------
// extractCodebuffBlockText: unknown block types
// ---------------------------------------------------------------------------

func TestExtractCodebuffBlockTextUnknownTypes(t *testing.T) {
	blocks := []codebuffRawBlock{
		{Type: "unknown", Content: "should be ignored"},
		{Type: "image", Content: "base64data"},
	}
	got := extractCodebuffBlockText(blocks)
	if got != "" {
		t.Errorf("got %q, want empty for unknown block types", got)
	}
}

// ---------------------------------------------------------------------------
// ReadCodebuffMessages: variant not user/ai/agent
// ---------------------------------------------------------------------------

func TestReadCodebuffMessagesSkipsUnknownVariant(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat-messages.json")
	content := `[
		{"id":"m1","variant":"user","content":"hello","timestamp":"2026-05-19T10:00:00Z"},
		{"id":"m2","variant":"system","content":"internal notification","timestamp":"2026-05-19T10:00:01Z"},
		{"id":"m3","variant":"ai","content":"response","timestamp":"2026-05-19T10:00:02Z"}
	]`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs, err := ReadCodebuffMessages(file)
	if err != nil {
		t.Fatalf("ReadCodebuffMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d, want 2 (system variant should be skipped)", len(msgs))
	}
}
