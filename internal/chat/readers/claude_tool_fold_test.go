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
	if msgs[1].Content != "[Bash]" {
		t.Errorf("expected folded content to be just tool name, got %q", msgs[1].Content)
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
	if msgs[0].Content != "[Bash]" {
		t.Errorf("expected just tool name, got %q", msgs[0].Content)
	}
}

func TestToolFoldingOrphanedResultDropped(t *testing.T) {
	path := writeTempJSONL(t,
		// Result with no matching tool_use (e.g., partial JSONL)
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_unknown","content":"some output"}]},"timestamp":"2026-05-08T12:00:01Z"}`,
	)

	msgs, err := ReadClaudeJSONLWithToolFolding(path)
	if err != nil {
		t.Fatal(err)
	}

	// With DropToolDetails=true, orphaned results are dropped entirely.
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages (orphaned result dropped), got %d", len(msgs))
	}
}

func TestToolFoldingOutputDropped(t *testing.T) {
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
	// With DropToolDetails=true, output is dropped — just tool name.
	if msgs[0].Content != "[Read]" {
		t.Errorf("expected just tool name, got %q", msgs[0].Content)
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
	// Folded tool call — just tool name when DropToolDetails=true
	if msgs[1].ToolName != "Bash" || msgs[1].Content != "[Bash]" {
		t.Errorf("unexpected msg 1: tool=%q content=%q", msgs[1].ToolName, msgs[1].Content)
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

func TestToolFoldingInputDropped(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		input    string
	}{
		{"Bash", "Bash", `{"command":"go test"}`},
		{"Read", "Read", `{"file_path":"main.go"}`},
		{"Edit", "Edit", `{"file_path":"main.go"}`},
		{"Grep", "Grep", `{"pattern":"TODO"}`},
		{"Glob", "Glob", `{"pattern":"**/*.go"}`},
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
			// With DropToolDetails=true, just tool name, no input.
			expected := "[" + tt.toolName + "]"
			if msgs[0].Content != expected {
				t.Errorf("expected %q, got %q", expected, msgs[0].Content)
			}
		})
	}
}

func TestSanitizeClaudeMessagesSurvivesFolded(t *testing.T) {
	// Folded messages (just tool names) should NOT be dropped by the sanitizer.
	msgs := []ChatMessage{
		{Role: "assistant", Content: "[Bash]", Timestamp: time.Now()},
		{Role: "assistant", Content: "[Read]", Timestamp: time.Now()},
		{Role: "assistant", Content: "[Edit]", Timestamp: time.Now()},
	}

	sanitized := SanitizeClaudeMessages(msgs)
	if len(sanitized) != len(msgs) {
		t.Errorf("expected %d messages to survive sanitization, got %d", len(msgs), len(sanitized))
	}
}

// ---------------------------------------------------------------------------
// serializeToolInput: additional edge cases
// ---------------------------------------------------------------------------

func TestSerializeToolInput_StringInputForKnownTool(t *testing.T) {
	// When Bash receives a string instead of map, input["command"].(string) fails
	// and it falls through to JSON fallback.
	input := map[string]any{"command": 123}
	got := serializeToolInput("Bash", input)
	if got == "" {
		t.Error("expected non-empty fallback for int command value")
	}
}

func TestSerializeToolInput_EditWithOnlyPath(t *testing.T) {
	input := map[string]any{"path": "handler.go", "old": "foo", "new": "bar"}
	got := serializeToolInput("Edit", input)
	if got != "handler.go" {
		t.Errorf("expected path fallback, got %q", got)
	}
}

func TestSerializeToolInput_WriteWithFilePath(t *testing.T) {
	input := map[string]any{"file_path": "output.txt", "content": "data"}
	got := serializeToolInput("Write", input)
	if got != "output.txt" {
		t.Errorf("expected file_path extraction, got %q", got)
	}
}

func TestSerializeToolInput_GrepLowercase(t *testing.T) {
	input := map[string]any{"pattern": "TODO"}
	got := serializeToolInput("grep", input)
	if got != "TODO" {
		t.Errorf("expected pattern extraction, got %q", got)
	}
}

func TestSerializeToolInput_GlobLowercase(t *testing.T) {
	input := map[string]any{"pattern": "*.go"}
	got := serializeToolInput("glob", input)
	if got != "*.go" {
		t.Errorf("expected pattern extraction, got %q", got)
	}
}

func TestSerializeToolInput_ReadLowercaseWithFilePath(t *testing.T) {
	input := map[string]any{"file_path": "main.go"}
	got := serializeToolInput("read", input)
	if got != "main.go" {
		t.Errorf("expected file_path extraction, got %q", got)
	}
}

func TestSerializeToolInput_BashWithoutCommandKeyFallsBack(t *testing.T) {
	input := map[string]any{"other": "value"}
	got := serializeToolInput("Bash", input)
	if got == "" {
		t.Error("expected non-empty JSON fallback")
	}
	if !strings.Contains(got, "other") {
		t.Errorf("expected JSON with 'other' key, got %q", got)
	}
}

func TestSerializeToolInput_EmptyMapFallback(t *testing.T) {
	input := map[string]any{}
	got := serializeToolInput("CustomTool", input)
	if got != "{}" {
		t.Errorf("expected empty JSON object, got %q", got)
	}
}
