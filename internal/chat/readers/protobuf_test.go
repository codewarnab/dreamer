package readers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadProtobufExtractsRolePrefixedMessages(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "conversation.pb")
	payload := make([]byte, 0, 128)
	payload = append(payload, encodeLengthDelimitedField(1, []byte("user: how do I fix this test?"))...)
	payload = append(payload, encodeLengthDelimitedField(1, []byte("assistant: run `go test ./...`"))...)

	if err := os.WriteFile(filePath, payload, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadProtobuf(filePath)
	if err != nil {
		t.Fatalf("ReadProtobuf returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 parsed messages, got %d", len(messages))
	}
	if got := messages[0].Role; got != "user" {
		t.Fatalf("first message role = %q, want user", got)
	}
	if got := messages[1].Role; got != "assistant" {
		t.Fatalf("second message role = %q, want assistant", got)
	}
}

func TestReadProtobufParsesEmbeddedJSONMessage(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "conversation.pb")
	payload := encodeLengthDelimitedField(2, []byte(`{"role":"assistant","content":"Here is the answer."}`))

	if err := os.WriteFile(filePath, payload, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadProtobuf(filePath)
	if err != nil {
		t.Fatalf("ReadProtobuf returned error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 parsed message, got %d", len(messages))
	}
	if got := messages[0].Role; got != "assistant" {
		t.Fatalf("message role = %q, want assistant", got)
	}
	if got := messages[0].Content; got != "Here is the answer." {
		t.Fatalf("message content = %q, want expected content", got)
	}
}

func TestReadProtobufReturnsOpenError(t *testing.T) {
	_, err := ReadProtobuf(filepath.Join(t.TempDir(), "missing.pb"))
	if err == nil {
		t.Fatalf("ReadProtobuf expected error for missing file")
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
