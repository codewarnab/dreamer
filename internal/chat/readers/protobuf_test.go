package readers

import (
	"os"
	"path/filepath"
	"strings"
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

// ---------------------------------------------------------------------------
// extractPrintableRuns
// ---------------------------------------------------------------------------

func TestExtractPrintableRunsFromASCIIData(t *testing.T) {
	data := []byte("hello world this is a long run")
	runs := extractPrintableRuns(data)
	if len(runs) == 0 {
		t.Fatal("expected at least one printable run")
	}
	found := false
	for _, r := range runs {
		if strings.Contains(r, "hello world") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected run containing 'hello world', got %v", runs)
	}
}

func TestExtractPrintableRunsSplitsOnNonPrintable(t *testing.T) {
	data := []byte("first part\x00second part")
	runs := extractPrintableRuns(data)
	if len(runs) != 2 {
		t.Errorf("expected 2 runs, got %d: %v", len(runs), runs)
	}
}

func TestExtractPrintableRunsDropsShort(t *testing.T) {
	data := []byte("ab\x00cd\x00ef")
	runs := extractPrintableRuns(data)
	for _, r := range runs {
		if len(r) < minCandidateLength {
			t.Errorf("run %q shorter than minCandidateLength=%d", r, minCandidateLength)
		}
	}
}

func TestExtractPrintableRunsEmpty(t *testing.T) {
	data := []byte("\x00\x01\x02\x03")
	runs := extractPrintableRuns(data)
	if len(runs) != 0 {
		t.Errorf("expected 0 runs for non-printable data, got %d", len(runs))
	}
}

// ---------------------------------------------------------------------------
// likelyEmbeddedProto
// ---------------------------------------------------------------------------

func TestLikelyEmbeddedProtoShortField(t *testing.T) {
	if likelyEmbeddedProto([]byte{0x01}) {
		t.Error("expected false for 1-byte field")
	}
}

func TestLikelyEmbeddedProtoDecodableString(t *testing.T) {
	// A field that looks like a valid UTF-8 string won't be recursed into.
	field := []byte("this is a readable string")
	if likelyEmbeddedProto(field) {
		t.Error("expected false for decodable string field")
	}
}

func TestLikelyEmbeddedProtoBinaryContent(t *testing.T) {
	field := []byte{0x08, 0x01, 0x10, 0x02, 0x1a, 0x03}
	if !likelyEmbeddedProto(field) {
		t.Error("expected true for binary content that's not a string")
	}
}

// ---------------------------------------------------------------------------
// collectProtoStrings: varint (wire type 0) and fixed32 (wire type 5)
// ---------------------------------------------------------------------------

func TestCollectProtoStringsWireType0(t *testing.T) {
	// field 1, wire type 0 (varint), value = 42
	payload := encodeVarint((1 << 3) | 0)
	payload = append(payload, encodeVarint(42)...)
	// Also add a string field so we get a candidate
	payload = append(payload, encodeLengthDelimitedField(2, []byte("a string value here"))...)

	var candidates []string
	collectProtoStrings(payload, 0, &candidates)
	found := false
	for _, c := range candidates {
		if strings.Contains(c, "a string value") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to find 'a string value' in candidates, got %v", candidates)
	}
}

func TestCollectProtoStringsWireType5(t *testing.T) {
	// field 1, wire type 5 (fixed32), then a string field
	payload := encodeVarint((1 << 3) | 5)
	payload = append(payload, []byte{0x01, 0x02, 0x03, 0x04}...) // 4 bytes
	payload = append(payload, encodeLengthDelimitedField(2, []byte("after fixed32 field ok"))...)

	var candidates []string
	collectProtoStrings(payload, 0, &candidates)
	found := false
	for _, c := range candidates {
		if strings.Contains(c, "after fixed32") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to find 'after fixed32', got %v", candidates)
	}
}

func TestCollectProtoStringsWireType1Fixed64(t *testing.T) {
	// field 1, wire type 1 (fixed64), then a string field
	payload := encodeVarint((1 << 3) | 1)
	payload = append(payload, []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}...) // 8 bytes
	payload = append(payload, encodeLengthDelimitedField(2, []byte("after fixed64 string ok"))...)

	var candidates []string
	collectProtoStrings(payload, 0, &candidates)
	found := false
	for _, c := range candidates {
		if strings.Contains(c, "after fixed64") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to find 'after fixed64', got %v", candidates)
	}
}

// ---------------------------------------------------------------------------
// messageFromRolePrefixedString
// ---------------------------------------------------------------------------

func TestMessageFromRolePrefixedStringUserVariants(t *testing.T) {
	tests := []struct {
		input string
		role  string
		text  string
	}{
		{"user: fix the bug", "user", "fix the bug"},
		{"Human: what is this?", "user", "what is this?"},
		{"PROMPT: investigate", "user", "investigate"},
		{"assistant: done", "assistant", "done"},
		{"Model: here you go", "assistant", "here you go"},
		{"GEMINI: I think...", "assistant", "I think..."},
		{"AI: nope", "assistant", "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			msg, ok := messageFromRolePrefixedString(tt.input)
			if !ok {
				t.Fatalf("expected ok for %q", tt.input)
			}
			if msg.Role != tt.role || msg.Content != tt.text {
				t.Errorf("got role=%q content=%q, want role=%q content=%q", msg.Role, msg.Content, tt.role, tt.text)
			}
		})
	}
}

func TestMessageFromRolePrefixedStringNoMatch(t *testing.T) {
	_, ok := messageFromRolePrefixedString("just some random text without prefix")
	if ok {
		t.Error("expected !ok for text without role prefix")
	}
}

func TestMessageFromRolePrefixedStringEmptyContent(t *testing.T) {
	_, ok := messageFromRolePrefixedString("user:   ")
	if ok {
		t.Error("expected !ok for empty content after prefix")
	}
}

// ---------------------------------------------------------------------------
// normalizeCandidates
// ---------------------------------------------------------------------------

func TestNormalizeCandidatesDeduplicatesAndFilters(t *testing.T) {
	input := []string{
		"hello world here",
		"hello world here",
		"  short  ",
		"",
		"another valid string",
	}
	got := normalizeCandidates(input)
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c] {
			t.Errorf("duplicate candidate: %q", c)
		}
		seen[c] = true
		if len(c) < minCandidateLength {
			t.Errorf("candidate %q shorter than minCandidateLength", c)
		}
	}
}

// ---------------------------------------------------------------------------
// dedupeMessages
// ---------------------------------------------------------------------------

func TestDedupeMessagesRemovesDuplicates(t *testing.T) {
	input := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "hello"},
	}
	got := dedupeMessages(input)
	if len(got) != 2 {
		t.Errorf("expected 2, got %d: %v", len(got), got)
	}
}

func TestDedupeMessagesSkipsEmpty(t *testing.T) {
	input := []ChatMessage{
		{Role: "", Content: ""},
		{Role: "user", Content: "valid"},
	}
	got := dedupeMessages(input)
	if len(got) != 1 {
		t.Errorf("expected 1, got %d: %v", len(got), got)
	}
}

// ---------------------------------------------------------------------------
// messageFromJSONString
// ---------------------------------------------------------------------------

func TestMessageFromJSONStringValid(t *testing.T) {
	input := `{"role":"user","content":"test message"}`
	msg, ok := messageFromJSONString(input)
	if !ok {
		t.Fatal("expected ok")
	}
	if msg.Role != "user" || msg.Content != "test message" {
		t.Errorf("got %+v", msg)
	}
}

func TestMessageFromJSONStringNotJSON(t *testing.T) {
	_, ok := messageFromJSONString("not json at all")
	if ok {
		t.Error("expected !ok for non-JSON string")
	}
}

func TestMessageFromJSONStringNoBraces(t *testing.T) {
	_, ok := messageFromJSONString("[1, 2, 3]")
	if ok {
		t.Error("expected !ok for array JSON")
	}
}

// ---------------------------------------------------------------------------
// ReadProtobuf with JSON content (parseJSONRecordsFromBytes)
// ---------------------------------------------------------------------------

func TestReadProtobufExtractsJSONLines(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "data.pb")
	data := []byte(`{"role":"user","content":"hello from json"}
{"role":"assistant","content":"reply from json"}
`)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	messages, err := ReadProtobuf(filePath)
	if err != nil {
		t.Fatalf("ReadProtobuf: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2, got %d", len(messages))
	}
	if messages[0].Content != "hello from json" {
		t.Errorf("content = %q", messages[0].Content)
	}
}

func TestReadProtobufEmptyFileReturnsNil(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "empty.pb")
	if err := os.WriteFile(filePath, []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	messages, err := ReadProtobuf(filePath)
	if err != nil {
		t.Fatalf("ReadProtobuf: %v", err)
	}
	if messages != nil {
		t.Errorf("expected nil for empty file, got %v", messages)
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
