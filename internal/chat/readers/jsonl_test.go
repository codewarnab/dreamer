package readers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadJSONLParsesMessagesAndSkipsMalformedLines(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "session.jsonl")
	contents := strings.Join([]string{
		`{"role":"user","content":"hello","timestamp":"2024-01-01T00:00:00Z"}`,
		`{this is not valid json}`,
		`{"message":{"author":{"role":"assistant"},"content":{"parts":["hi there"]},"createdAt":1704067800}}`,
		`{"event":"heartbeat"}`,
	}, "\n")

	if err := os.WriteFile(filePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadJSONL(filePath)
	if err != nil {
		t.Fatalf("ReadJSONL returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 parsed messages, got %d", len(messages))
	}

	if got := messages[0].Role; got != "user" {
		t.Fatalf("first message role = %q, want user", got)
	}
	if got := messages[0].Content; got != "hello" {
		t.Fatalf("first message content = %q, want hello", got)
	}

	expectedSecondTime := time.Unix(1704067800, 0).UTC()
	if got := messages[1].Role; got != "assistant" {
		t.Fatalf("second message role = %q, want assistant", got)
	}
	if got := messages[1].Content; got != "hi there" {
		t.Fatalf("second message content = %q, want hi there", got)
	}
	if !messages[1].Timestamp.Equal(expectedSecondTime) {
		t.Fatalf("second message timestamp = %s, want %s", messages[1].Timestamp, expectedSecondTime)
	}
}

func TestReadJSONLSupportsLargeLines(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "large-session.jsonl")
	largeContent := strings.Repeat("a", 70*1024)
	line := fmt.Sprintf(`{"role":"user","content":%q}`, largeContent)

	if err := os.WriteFile(filePath, []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadJSONL(filePath)
	if err != nil {
		t.Fatalf("ReadJSONL returned error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 parsed message, got %d", len(messages))
	}
	if got := len(messages[0].Content); got != len(largeContent) {
		t.Fatalf("parsed content length = %d, want %d", got, len(largeContent))
	}
}

func TestReadJSONLReturnsOpenError(t *testing.T) {
	_, err := ReadJSONL(filepath.Join(t.TempDir(), "missing.jsonl"))
	if err == nil {
		t.Fatalf("ReadJSONL expected error for missing file")
	}
}

func TestReadJSONLParsesClaudeCodeSessionRecords(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "claude-session.jsonl")
	contents := strings.Join([]string{
		`{"type":"queue-operation","sessionId":"abc","operation":"enqueue","timestamp":"2026-05-08T12:00:00Z"}`,
		`{"type":"user","message":{"role":"user","content":"Help me debug this"},"timestamp":"2026-05-08T12:00:01Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Sure, share the error."}]},"timestamp":"2026-05-08T12:00:02Z"}`,
	}, "\n")

	if err := os.WriteFile(filePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadJSONL(filePath)
	if err != nil {
		t.Fatalf("ReadJSONL returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 parsed messages, got %d", len(messages))
	}

	if got := messages[0].Role; got != "user" {
		t.Fatalf("first message role = %q, want user", got)
	}
	if got := messages[0].Content; got != "Help me debug this" {
		t.Fatalf("first message content = %q, want expected Claude user content", got)
	}
	if got := messages[1].Role; got != "assistant" {
		t.Fatalf("second message role = %q, want assistant", got)
	}
	if got := messages[1].Content; got != "Sure, share the error." {
		t.Fatalf("second message content = %q, want expected Claude assistant content", got)
	}
}
