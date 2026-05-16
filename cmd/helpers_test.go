package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/chat"
)

func TestReadMessagesFromSourceAcceptsJSONOnlyForVSCodeChat(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "chat.json")
	if err := os.WriteFile(sourcePath, []byte(`{"requests":[{"message":{"text":"hello"},"response":"world"}]}`), 0o644); err != nil {
		t.Fatalf("write vscode json fixture: %v", err)
	}

	messages, err := readMessagesFromSource(chat.ChatSource{
		Path: sourcePath,
		Tool: chat.SourceTypeVSCodeChatSession,
	})
	if err != nil {
		t.Fatalf("readMessagesFromSource returned error for vscode json: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}

	_, err = readMessagesFromSource(chat.ChatSource{
		Path: sourcePath,
		Tool: chat.SourceTypeCopilotSessionJSONL,
	})
	if err == nil {
		t.Fatalf("readMessagesFromSource expected unsupported .json error for non-vscode source")
	}
	if !strings.Contains(err.Error(), ".json only for vscode") {
		t.Fatalf("error = %q, want vscode-only .json message", err)
	}
}

func TestReadMessagesFromSourceReadsProtobuf(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "chat.pb")
	payload := make([]byte, 0, 64)
	payload = append(payload, encodeLengthDelimitedField(1, []byte("user: what is wrong?"))...)
	payload = append(payload, encodeLengthDelimitedField(1, []byte("assistant: run `go test`"))...)
	if err := os.WriteFile(sourcePath, payload, 0o644); err != nil {
		t.Fatalf("write protobuf source fixture: %v", err)
	}

	messages, err := readMessagesFromSource(chat.ChatSource{
		Path: sourcePath,
		Tool: chat.SourceTypeAntigravityGemini,
	})
	if err != nil {
		t.Fatalf("readMessagesFromSource returned error: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(messages))
	}
	if messages[0].Role != "user" {
		t.Fatalf("first message role = %q, want user", messages[0].Role)
	}
	foundAssistant := false
	for _, message := range messages {
		if message.Role == "assistant" {
			foundAssistant = true
			break
		}
	}
	if !foundAssistant {
		t.Fatalf("expected at least one assistant message")
	}
}

func TestReadMessagesFromSourceSanitizesClaudeJSONLOnly(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "claude-chat.jsonl")
	contents := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"<local-command-stdout>Set model</local-command-stdout>"}}`,
		`{"type":"user","message":{"role":"user","content":"Need help with flaky tests"}}`,
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write jsonl source fixture: %v", err)
	}

	claudeMessages, err := readMessagesFromSource(chat.ChatSource{
		Path: sourcePath,
		Tool: chat.SourceTypeClaudeCodeSession,
	})
	if err != nil {
		t.Fatalf("readMessagesFromSource (claude) returned error: %v", err)
	}
	if len(claudeMessages) != 1 {
		t.Fatalf("len(claudeMessages) = %d, want 1", len(claudeMessages))
	}
	if got := claudeMessages[0].Content; got != "Need help with flaky tests" {
		t.Fatalf("claudeMessages[0].Content = %q, want sanitized prompt", got)
	}

	defaultMessages, err := readMessagesFromSource(chat.ChatSource{
		Path: sourcePath,
		Tool: chat.SourceTypeCopilotSessionJSONL,
	})
	if err != nil {
		t.Fatalf("readMessagesFromSource (copilot) returned error: %v", err)
	}
	if len(defaultMessages) != 2 {
		t.Fatalf("len(defaultMessages) = %d, want 2", len(defaultMessages))
	}
	if got := defaultMessages[0].Content; !strings.Contains(got, "<local-command-stdout>") {
		t.Fatalf("defaultMessages[0].Content = %q, want unsanitized content", got)
	}
}

func encodeLengthDelimitedField(fieldNumber uint64, value []byte) []byte {
	encoded := make([]byte, 0, 16+len(value))
	encoded = append(encoded, encodeVarint((fieldNumber<<3)|2)...)
	encoded = append(encoded, encodeVarint(uint64(len(value)))...)
	encoded = append(encoded, value...)
	return encoded
}

func encodeVarint(value uint64) []byte {
	encoded := make([]byte, 0, 10)
	for value >= 0x80 {
		encoded = append(encoded, byte(value)|0x80)
		value >>= 7
	}
	encoded = append(encoded, byte(value))
	return encoded
}

func sourcePaths(sources []chat.ChatSource) []string {
	paths := make([]string, 0, len(sources))
	for _, source := range sources {
		paths = append(paths, source.Path)
	}
	return paths
}

func assertStringSliceEqual(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(slice) = %d, want %d (got=%v want=%v)", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slice[%d] = %q, want %q (got=%v want=%v)", i, got[i], want[i], got, want)
		}
	}
}
