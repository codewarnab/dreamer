package readers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTempJSONL(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestToolFoldingSinglePair(t *testing.T) {
	path := writeTempJSONL(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me check."},{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"go test ./..."}}]},"timestamp":"2026-05-08T12:00:01Z"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"ok  dreamer/internal/config  0.012s"}]},"timestamp":"2026-05-08T12:00:02Z"}`,
	)

	msgs, err := ReadClaudeJSONLWithToolFolding(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// First: text from assistant
	if msgs[0].Role != "assistant" || msgs[0].Content != "Let me check." {
		t.Errorf("unexpected msg 0: role=%q content=%q", msgs[0].Role, msgs[0].Content)
	}

	// Second: folded tool call
	if msgs[1].Role != "assistant" {
		t.Errorf("expected assistant role for folded call, got %q", msgs[1].Role)
	}
	if msgs[1].ToolName != "Bash" {
		t.Errorf("expected ToolName=Bash, got %q", msgs[1].ToolName)
	}
	if !strings.Contains(msgs[1].Content, "[Bash] go test ./...") {
		t.Errorf("expected folded content to contain command, got %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "→ ok  dreamer") {
		t.Errorf("expected folded content to contain output, got %q", msgs[1].Content)
	}
}

func TestToolFoldingMultipleTools(t *testing.T) {
	path := writeTempJSONL(t,
		// Assistant sends two tool calls
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Read","input":{"file_path":"main.go"}},{"type":"tool_use","id":"tu2","name":"Bash","input":{"command":"go build ./..."}}]},"timestamp":"2026-05-08T12:00:01Z"}`,
		// User returns both results
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"package main\n\nfunc main() {}"},{"type":"tool_result","tool_use_id":"tu2","content":""}]},"timestamp":"2026-05-08T12:00:02Z"}`,
	)

	msgs, err := ReadClaudeJSONLWithToolFolding(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	if msgs[0].ToolName != "Read" {
		t.Errorf("expected first tool=Read, got %q", msgs[0].ToolName)
	}
	if msgs[1].ToolName != "Bash" {
		t.Errorf("expected second tool=Bash, got %q", msgs[1].ToolName)
	}
}

func TestToolFoldingOrphanedUse(t *testing.T) {
	path := writeTempJSONL(t,
		// Tool call with no result (session interrupted)
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"ls"}}]},"timestamp":"2026-05-08T12:00:01Z"}`,
	)

	msgs, err := ReadClaudeJSONLWithToolFolding(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (orphaned), got %d", len(msgs))
	}
	if msgs[0].ToolName != "Bash" {
		t.Errorf("expected ToolName=Bash, got %q", msgs[0].ToolName)
	}
	if !strings.Contains(msgs[0].Content, "[Bash] ls") {
		t.Errorf("expected orphaned call content, got %q", msgs[0].Content)
	}
	// No output arrow since no result
	if strings.Contains(msgs[0].Content, "→") {
		t.Errorf("orphaned call should not have output arrow, got %q", msgs[0].Content)
	}
}

func TestToolFoldingOrphanedResult(t *testing.T) {
	path := writeTempJSONL(t,
		// Result with no matching tool_use (e.g., partial JSONL)
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_unknown","content":"some output"}]},"timestamp":"2026-05-08T12:00:01Z"}`,
	)

	msgs, err := ReadClaudeJSONLWithToolFolding(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "[tool_result]") {
		t.Errorf("expected orphaned result format, got %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "some output") {
		t.Errorf("expected output content, got %q", msgs[0].Content)
	}
}

func TestToolFoldingOutputTruncation(t *testing.T) {
	// Generate output longer than maxToolOutputChars (500)
	longOutput := strings.Repeat("x", 600)
	path := writeTempJSONL(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Read","input":{"file_path":"big.go"}}]},"timestamp":"2026-05-08T12:00:01Z"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"`+longOutput+`"}]},"timestamp":"2026-05-08T12:00:02Z"}`,
	)

	msgs, err := ReadClaudeJSONLWithToolFolding(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Content) > 600 {
		t.Errorf("output not truncated: len=%d", len(msgs[0].Content))
	}
	if !strings.Contains(msgs[0].Content, "chars truncated") {
		t.Errorf("expected truncation marker, got %q", msgs[0].Content)
	}
}

func TestToolFoldingMixedTextAndTools(t *testing.T) {
	path := writeTempJSONL(t,
		// Assistant message with text + tool_use
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"I'll run the tests."},{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"go test"}}]},"timestamp":"2026-05-08T12:00:01Z"}`,
		// User message with tool_result + text
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"PASS"},{"type":"text","text":"Tests look good."}]},"timestamp":"2026-05-08T12:00:02Z"}`,
	)

	msgs, err := ReadClaudeJSONLWithToolFolding(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Text from assistant
	if msgs[0].Content != "I'll run the tests." {
		t.Errorf("unexpected msg 0: %q", msgs[0].Content)
	}
	// Folded tool call
	if msgs[1].ToolName != "Bash" || !strings.Contains(msgs[1].Content, "PASS") {
		t.Errorf("unexpected msg 1: %q", msgs[1].Content)
	}
	// Text from user
	if msgs[2].Role != "user" || msgs[2].Content != "Tests look good." {
		t.Errorf("unexpected msg 2: role=%q content=%q", msgs[2].Role, msgs[2].Content)
	}
}

func TestToolFoldingPlainMessages(t *testing.T) {
	// Messages without tool blocks should pass through unchanged.
	path := writeTempJSONL(t,
		`{"type":"assistant","message":{"role":"assistant","content":"Hello!"},"timestamp":"2026-05-08T12:00:01Z"}`,
		`{"type":"user","message":{"role":"user","content":"Hi there."},"timestamp":"2026-05-08T12:00:02Z"}`,
	)

	msgs, err := ReadClaudeJSONLWithToolFolding(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "Hello!" || msgs[0].ToolName != "" {
		t.Errorf("unexpected msg 0: %q tool=%q", msgs[0].Content, msgs[0].ToolName)
	}
	if msgs[1].Content != "Hi there." || msgs[1].ToolName != "" {
		t.Errorf("unexpected msg 1: %q tool=%q", msgs[1].Content, msgs[1].ToolName)
	}
}

func TestToolFoldingTimestampPreservation(t *testing.T) {
	path := writeTempJSONL(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"echo hi"}}]},"timestamp":"2026-05-08T12:00:01Z"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"hi"}]},"timestamp":"2026-05-08T12:00:05Z"}`,
	)

	msgs, err := ReadClaudeJSONLWithToolFolding(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	// Timestamp should be from the tool_use (assistant message), not the result.
	expected := time.Date(2026, 5, 8, 12, 0, 1, 0, time.UTC)
	if !msgs[0].Timestamp.Equal(expected) {
		t.Errorf("expected timestamp %v, got %v", expected, msgs[0].Timestamp)
	}
}

func TestToolFoldingInputExtraction(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		input    string
		expected string
	}{
		{"Bash", "Bash", `{"command":"go test"}`, "go test"},
		{"Read", "Read", `{"file_path":"main.go"}`, "main.go"},
		{"Edit", "Edit", `{"file_path":"main.go"}`, "main.go"},
		{"Grep", "Grep", `{"pattern":"TODO"}`, "TODO"},
		{"Glob", "Glob", `{"pattern":"**/*.go"}`, "**/*.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempJSONL(t,
				`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"`+tt.toolName+`","input":`+tt.input+`}]},"timestamp":"2026-05-08T12:00:01Z"}`,
				`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"ok"}]},"timestamp":"2026-05-08T12:00:02Z"}`,
			)

			msgs, err := ReadClaudeJSONLWithToolFolding(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 1 {
				t.Fatalf("expected 1 message, got %d", len(msgs))
			}
			if !strings.Contains(msgs[0].Content, "["+tt.toolName+"] "+tt.expected) {
				t.Errorf("expected content to contain %q, got %q", "["+tt.toolName+"] "+tt.expected, msgs[0].Content)
			}
		})
	}
}

func TestSanitizeClaudeMessagesSurvivesFolded(t *testing.T) {
	// Folded messages should NOT be dropped by the sanitizer.
	msgs := []ChatMessage{
		{Role: "assistant", Content: "[Bash] go test ./...\n→ PASS", Timestamp: time.Now()},
		{Role: "assistant", Content: "[Read] main.go\n→ (45 lines)", Timestamp: time.Now()},
		{Role: "assistant", Content: "[Edit] config.go\n→ changed lines 10-15", Timestamp: time.Now()},
	}

	sanitized := SanitizeClaudeMessages(msgs)
	if len(sanitized) != len(msgs) {
		t.Errorf("expected %d messages to survive sanitization, got %d", len(msgs), len(sanitized))
	}
}
