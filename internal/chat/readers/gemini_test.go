package readers

import (
	"os"
	"path/filepath"
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
