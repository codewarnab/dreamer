package readers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadGeminiCLIReturnsUserAndAssistantTurns(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	payload := `{"sessionId":"abc","projectHash":"hash","kind":"conversation","messages":[]}
{"id":"1","timestamp":"2026-04-01T10:00:00Z","type":"user","content":"hi gemini"}
{"id":"2","timestamp":"2026-04-01T10:00:05Z","type":"gemini","content":[{"text":"hello there"}]}
{"id":"3","timestamp":"2026-04-01T10:00:06Z","type":"info","content":"loaded memory"}
{"$set":{"summary":"new"}}
{"$rewindTo":"1"}
{"id":"4","timestamp":"2026-04-01T10:00:10Z","type":"gemini","content":"second reply"}
`
	if err := os.WriteFile(sessionPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadGeminiCLI(sessionPath)
	if err != nil {
		t.Fatalf("ReadGeminiCLI returned error: %v", err)
	}

	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %#v", len(messages), messages)
	}

	want := []ChatMessage{
		{Role: "user", Content: "hi gemini"},
		{Role: "assistant", Content: "hello there"},
		{Role: "assistant", Content: "second reply"},
	}
	for index, expected := range want {
		actual := messages[index]
		if actual.Role != expected.Role || actual.Content != expected.Content {
			t.Errorf("message[%d] = %+v, want %+v", index, actual, expected)
		}
	}
}

func TestReadGeminiCLIRecoversFromFirstLineMessages(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	payload := `{"sessionId":"abc","messages":[{"type":"user","content":"restored prompt"},{"type":"gemini","content":"restored reply"}]}
`
	if err := os.WriteFile(sessionPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadGeminiCLI(sessionPath)
	if err != nil {
		t.Fatalf("ReadGeminiCLI returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 restored messages, got %d", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "restored prompt" {
		t.Errorf("restored message[0] = %+v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Content != "restored reply" {
		t.Errorf("restored message[1] = %+v", messages[1])
	}
}

func TestReadGeminiCLISkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	payload := "not json\n{\"sessionId\":\"abc\"}\n{\"type\":\"user\",\"content\":\"valid\"}\n"
	if err := os.WriteFile(sessionPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadGeminiCLI(sessionPath)
	if err != nil {
		t.Fatalf("ReadGeminiCLI returned error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Content != "valid" {
		t.Errorf("message content = %q, want valid", messages[0].Content)
	}
}

// ---------------------------------------------------------------------------
// ReadGeminiCLI: open error
// ---------------------------------------------------------------------------

func TestReadGeminiCLIOpenError(t *testing.T) {
	_, err := ReadGeminiCLI(filepath.Join(t.TempDir(), "missing.jsonl"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// ---------------------------------------------------------------------------
// isGeminiSentinel
// ---------------------------------------------------------------------------

func TestIsGeminiSentinel(t *testing.T) {
	if !isGeminiSentinel(map[string]any{"$set": "summary"}) {
		t.Error("expected true for $set key")
	}
	if !isGeminiSentinel(map[string]any{"$rewindTo": "1"}) {
		t.Error("expected true for $rewindTo key")
	}
	if isGeminiSentinel(map[string]any{"type": "user"}) {
		t.Error("expected false for non-sentinel record")
	}
	if isGeminiSentinel(map[string]any{}) {
		t.Error("expected false for empty record")
	}
}

// ---------------------------------------------------------------------------
// geminiMessageFromValue: non-map, missing role, missing content
// ---------------------------------------------------------------------------

func TestGeminiMessageFromValueNonMap(t *testing.T) {
	_, ok := geminiMessageFromValue("string value")
	if ok {
		t.Error("expected !ok for non-map value")
	}
}

func TestGeminiMessageFromValueNoRole(t *testing.T) {
	_, ok := geminiMessageFromValue(map[string]any{"content": "hello"})
	if ok {
		t.Error("expected !ok for record without type/role")
	}
}

func TestGeminiMessageFromValueNoContent(t *testing.T) {
	_, ok := geminiMessageFromValue(map[string]any{"type": "user"})
	if ok {
		t.Error("expected !ok for record without content")
	}
}

func TestGeminiMessageFromValueUnknownType(t *testing.T) {
	_, ok := geminiMessageFromValue(map[string]any{"type": "system", "content": "internal"})
	if ok {
		t.Error("expected !ok for type=system")
	}
}

// ---------------------------------------------------------------------------
// geminiContentFromRecord: displayContent and text fallbacks
// ---------------------------------------------------------------------------

func TestGeminiContentFromRecordDisplayContent(t *testing.T) {
	record := map[string]any{
		"displayContent": "display text here",
	}
	got := geminiContentFromRecord(record)
	if got != "display text here" {
		t.Errorf("got %q, want %q", got, "display text here")
	}
}

func TestGeminiContentFromRecordTextFallback(t *testing.T) {
	record := map[string]any{
		"text": "plain text field",
	}
	got := geminiContentFromRecord(record)
	if got != "plain text field" {
		t.Errorf("got %q, want %q", got, "plain text field")
	}
}

func TestGeminiContentFromRecordEmpty(t *testing.T) {
	record := map[string]any{}
	got := geminiContentFromRecord(record)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// normalizeGeminiRole: info/error/warning fallthrough + explicit role field
// ---------------------------------------------------------------------------

func TestNormalizeGeminiRoleInfoTypeWithRoleField(t *testing.T) {
	record := map[string]any{
		"type": "info",
		"role": "user",
	}
	got := normalizeGeminiRole(record)
	if got != "user" {
		t.Errorf("got %q, want user", got)
	}
}

func TestNormalizeGeminiRoleWarningTypeNoRole(t *testing.T) {
	record := map[string]any{
		"type": "warning",
	}
	got := normalizeGeminiRole(record)
	if got != "" {
		t.Errorf("got %q, want empty for warning without role", got)
	}
}

func TestNormalizeGeminiRoleEmptyTypeWithRoleField(t *testing.T) {
	record := map[string]any{
		"role": "assistant",
	}
	got := normalizeGeminiRole(record)
	if got != "assistant" {
		t.Errorf("got %q, want assistant", got)
	}
}

// ---------------------------------------------------------------------------
// ReadGeminiCLI: model type maps to assistant
// ---------------------------------------------------------------------------

func TestReadGeminiCLIModelTypeTurn(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	payload := `{"type":"user","content":"hi"}
{"type":"model","content":[{"text":"model response"}]}
`
	if err := os.WriteFile(sessionPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	messages, err := ReadGeminiCLI(sessionPath)
	if err != nil {
		t.Fatalf("ReadGeminiCLI: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2, got %d", len(messages))
	}
	if messages[1].Role != "assistant" {
		t.Errorf("model role = %q, want assistant", messages[1].Role)
	}
	if !strings.Contains(messages[1].Content, "model response") {
		t.Errorf("content = %q", messages[1].Content)
	}
}

// ---------------------------------------------------------------------------
// geminiTextFromValue: array with nil items
// ---------------------------------------------------------------------------

func TestGeminiTextFromValueArrayWithNilItems(t *testing.T) {
	input := []any{"hello", nil, "world"}
	got := geminiTextFromValue(input, 0)
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Errorf("got %q, expected hello and world", got)
	}
}

// ---------------------------------------------------------------------------
// geminiTextFromValue: map with no recognized keys
// ---------------------------------------------------------------------------

func TestGeminiTextFromValueMapNoRecognizedKeys(t *testing.T) {
	input := map[string]any{"unknown": "value"}
	got := geminiTextFromValue(input, 0)
	if got != "" {
		t.Errorf("got %q, want empty for unrecognized map", got)
	}
}
