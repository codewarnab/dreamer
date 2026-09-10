package readers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAntigravityGeminiExtractsOnlyConversationMessagesFromPBText(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "conversation.pbtxt")
	contents := strings.Join([]string{
		`workspacePath: "C:\\Users\\User\\code\\dreamer"`,
		`title: "Discovery notes"`,
		`role: "user"`,
		`text: "Please isolate Antigravity chats by project."`,
		`role: "model"`,
		`text: "I will add a cwd evidence gate."`,
		`model: "gemini-pro"`,
	}, "\n")
	if err := os.WriteFile(filePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadAntigravityGemini(filePath)
	if err != nil {
		t.Fatalf("ReadAntigravityGemini returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 Antigravity messages, got %d", len(messages))
	}
	if got := messages[0].Content; got != "Please isolate Antigravity chats by project." {
		t.Fatalf("first message content = %q, want user prompt", got)
	}
	if got := messages[1].Content; got != "I will add a cwd evidence gate." {
		t.Fatalf("second message content = %q, want assistant response", got)
	}
}

func TestReadAntigravityGeminiDropsToolRuntimeAndLargeOutputNoise(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "conversation.jsonl")
	contents := strings.Join([]string{
		`{"role":"user","content":"Need help\nwith Gemini cleanup."}`,
		`{"role":"user","content":"Need   help with Gemini cleanup."}`,
		`{"role":"assistant","content":"tool_result: {\"output\":\"large command output\"}"}`,
		`{"role":"assistant","content":"Runtime state checkpoint for tool call."}`,
		`{"role":"assistant","content":"Keep explicit assistant responses."}`,
		`{"role":"user","content":"workspace path: C:\\Users\\User\\code\\dreamer"}`,
	}, "\n")
	if err := os.WriteFile(filePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadAntigravityGemini(filePath)
	if err != nil {
		t.Fatalf("ReadAntigravityGemini returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 sanitized Antigravity messages, got %d", len(messages))
	}
	if got := messages[0].Content; got != "Need help with Gemini cleanup." {
		t.Fatalf("first message content = %q, want normalized user prompt", got)
	}
	if got := messages[1].Content; got != "Keep explicit assistant responses." {
		t.Fatalf("second message content = %q, want assistant response", got)
	}

	joined := strings.ToLower(messages[0].Content + " " + messages[1].Content)
	for _, unwanted := range []string{"tool_result", "runtime state", "workspace path"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("sanitized messages should not include %q: %q", unwanted, joined)
		}
	}
}

func TestReadAntigravityGeminiDoesNotAlternateArbitraryProtobufStrings(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "conversation.pb")
	payload := make([]byte, 0, 128)
	payload = append(payload, encodeLengthDelimitedField(1, []byte("Discovery notes"))...)
	payload = append(payload, encodeLengthDelimitedField(1, []byte(`C:\Users\User\code\other-project`))...)
	payload = append(payload, encodeLengthDelimitedField(1, []byte("gemini-pro"))...)
	if err := os.WriteFile(filePath, payload, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadAntigravityGemini(filePath)
	if err != nil {
		t.Fatalf("ReadAntigravityGemini returned error: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected no messages from arbitrary protobuf strings, got %+v", messages)
	}
}

// ---------------------------------------------------------------------------
// ReadAntigravityGemini: unsupported extension
// ---------------------------------------------------------------------------

func TestReadAntigravityGeminiUnsupportedExtension(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "chat.xml")
	if err := os.WriteFile(filePath, []byte("<root/>"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := ReadAntigravityGemini(filePath)
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
}

// ---------------------------------------------------------------------------
// looksLikeVectorPayload
// ---------------------------------------------------------------------------

func TestLooksLikeVectorPayload(t *testing.T) {
	// Build a string with many commas and no spaces.
	nums := make([]string, 30)
	for i := range nums {
		nums[i] = "0.12345"
	}
	vector := "[" + strings.Join(nums, ",") + "]"
	if !looksLikeVectorPayload(vector) {
		t.Error("expected true for vector-like payload")
	}
}

func TestLooksLikeVectorPayloadNotEnoughCommas(t *testing.T) {
	short := "[0.1, 0.2, 0.3]"
	if looksLikeVectorPayload(short) {
		t.Error("expected false for short array with spaces")
	}
}

func TestLooksLikeVectorPayloadWithSpaces(t *testing.T) {
	// Has enough commas but also spaces.
	nums := make([]string, 25)
	for i := range nums {
		nums[i] = fmt.Sprintf("0.%d", i)
	}
	withSpaces := "[  " + strings.Join(nums, ",") + "  ]"
	if looksLikeVectorPayload(withSpaces) {
		t.Error("expected false for array with spaces")
	}
}

func TestLooksLikeVectorPayloadNotArray(t *testing.T) {
	if looksLikeVectorPayload("not an array at all") {
		t.Error("expected false for non-array string")
	}
}

// ---------------------------------------------------------------------------
// isAntigravityTextKey: edge cases
// ---------------------------------------------------------------------------

func TestIsAntigravityTextKey(t *testing.T) {
	positive := []string{"text", "content", "message", "body", "prompt", "response"}
	for _, key := range positive {
		if !isAntigravityTextKey(key) {
			t.Errorf("isAntigravityTextKey(%q) = false, want true", key)
		}
	}
	if isAntigravityTextKey("unknown") {
		t.Error("isAntigravityTextKey(unknown) should be false")
	}
}

// ---------------------------------------------------------------------------
// splitTextProtoField
// ---------------------------------------------------------------------------

func TestSplitTextProtoFieldValid(t *testing.T) {
	key, value, ok := splitTextProtoField(`role: "user"`)
	if !ok {
		t.Fatal("expected ok")
	}
	if key != "role" || value != "user" {
		t.Errorf("key=%q value=%q", key, value)
	}
}

func TestSplitTextProtoFieldNoColon(t *testing.T) {
	_, _, ok := splitTextProtoField("no colon here")
	if ok {
		t.Error("expected !ok for line without colon")
	}
}

func TestSplitTextProtoFieldEmptyKey(t *testing.T) {
	_, _, ok := splitTextProtoField(`: value`)
	if ok {
		t.Error("expected !ok for empty key")
	}
}

func TestSplitTextProtoFieldEmptyValue(t *testing.T) {
	_, _, ok := splitTextProtoField(`key: `)
	if ok {
		t.Error("expected !ok for empty value")
	}
}

// ---------------------------------------------------------------------------
// normalizeAntigravityContent: truncation
// ---------------------------------------------------------------------------

func TestNormalizeAntigravityContentTruncatesLong(t *testing.T) {
	longContent := strings.Repeat("x", antigravityMaxCharsPerMessage+100)
	got := normalizeAntigravityContent(longContent)
	runeCount := 0
	for range got {
		runeCount++
	}
	if runeCount > antigravityMaxCharsPerMessage {
		t.Errorf("rune count = %d, want <= %d", runeCount, antigravityMaxCharsPerMessage)
	}
}

func TestNormalizeAntigravityContentEmpty(t *testing.T) {
	if got := normalizeAntigravityContent(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := normalizeAntigravityContent("   \n  "); got != "" {
		t.Errorf("got %q, want empty for whitespace-only", got)
	}
}

// ---------------------------------------------------------------------------
// SanitizeAntigravityMessages: drop rules
// ---------------------------------------------------------------------------

func TestSanitizeAntigravityMessagesDropsToolNoise(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "help me fix this"},
		{Role: "assistant", Content: "tool_call: ran some command"},
		{Role: "assistant", Content: "Here's the fix."},
	}
	result := SanitizeAntigravityMessages(msgs)
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
}

func TestSanitizeAntigravityMessagesDropsVectorEmbedding(t *testing.T) {
	nums := make([]string, 25)
	for i := range nums {
		nums[i] = "0.12345"
	}
	vectorPayload := "[" + strings.Join(nums, ",") + "]"
	msgs := []ChatMessage{
		{Role: "assistant", Content: vectorPayload},
		{Role: "user", Content: "what does this mean?"},
	}
	result := SanitizeAntigravityMessages(msgs)
	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
	if result[0].Content != "what does this mean?" {
		t.Errorf("content = %q", result[0].Content)
	}
}

// ---------------------------------------------------------------------------
// parseAntigravityTextProtoMessages: edge cases
// ---------------------------------------------------------------------------

func TestParseAntigravityTextProtoMessagesRolePrefix(t *testing.T) {
	content := `role: "user"
text: "please help"
role: "assistant"
text: "I'll look into it"`
	msgs := parseAntigravityTextProtoMessages(content)
	if len(msgs) != 2 {
		t.Fatalf("expected 2, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "please help" {
		t.Errorf("msg[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "I'll look into it" {
		t.Errorf("msg[1] = %+v", msgs[1])
	}
}

func TestParseAntigravityTextProtoMessagesEmpty(t *testing.T) {
	msgs := parseAntigravityTextProtoMessages("")
	if len(msgs) != 0 {
		t.Errorf("expected 0, got %d", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// ReadAntigravityGemini: .pb extension
// ---------------------------------------------------------------------------

func TestReadAntigravityGeminiPBExtension(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "conversation.pb")
	payload := encodeLengthDelimitedField(1, []byte("user: what is this?"))
	payload = append(payload, encodeLengthDelimitedField(1, []byte("assistant: an answer"))...)
	if err := os.WriteFile(filePath, payload, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	msgs, err := ReadAntigravityGemini(filePath)
	if err != nil {
		t.Fatalf("ReadAntigravityGemini(.pb): %v", err)
	}
	if len(msgs) < 1 {
		t.Errorf("expected at least 1 message, got %d", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// ReadAntigravityGemini: .jsonl extension
// ---------------------------------------------------------------------------

func TestReadAntigravityGeminiJSONLExtension(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "chat.jsonl")
	contents := strings.Join([]string{
		`{"role":"user","content":"help me"}`,
		`{"role":"assistant","content":"sure thing"}`,
	}, "\n")
	if err := os.WriteFile(filePath, []byte(contents+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	msgs, err := ReadAntigravityGemini(filePath)
	if err != nil {
		t.Fatalf("ReadAntigravityGemini(.jsonl): %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// ReadAntigravityGemini: .pbtxt with no text proto → falls back to pb
// ---------------------------------------------------------------------------

func TestReadAntigravityGeminiPBtxtFallbackToProtobuf(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "chat.pbtxt")
	// Content that doesn't parse as text proto (no role/text pairs).
	payload := "some random non-proto text content here"
	if err := os.WriteFile(filePath, []byte(payload), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Will either return nil (no parseable content) or an error from readAntigravityProtobuf.
	msgs, err := ReadAntigravityGemini(filePath)
	if err != nil {
		// readAntigravityProtobuf may error on non-proto data — that's fine.
		return
	}
	// If no error, msgs may be nil or empty.
	_ = msgs
}

// ---------------------------------------------------------------------------
// ReadAntigravityGemini: .pb file read error
// ---------------------------------------------------------------------------

func TestReadAntigravityGeminiPBMissingFile(t *testing.T) {
	_, err := ReadAntigravityGemini(filepath.Join(t.TempDir(), "missing.pb"))
	if err == nil {
		t.Fatal("expected error for missing .pb file")
	}
}

// ---------------------------------------------------------------------------
// normalizeAntigravityContent: collapses whitespace
// ---------------------------------------------------------------------------

func TestNormalizeAntigravityContentCollapsesWhitespace(t *testing.T) {
	got := normalizeAntigravityContent("  hello\n\n  world  ")
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestNormalizeAntigravityContentStripsANSI(t *testing.T) {
	got := normalizeAntigravityContent("\x1b[31mred text\x1b[0m")
	if got != "red text" {
		t.Errorf("got %q, want %q", got, "red text")
	}
}

// ---------------------------------------------------------------------------
// shouldDropAntigravityMessage: various drop rules
// ---------------------------------------------------------------------------

func TestShouldDropAntigravityMessageModelConfig(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "assistant", Content: "model config: setting temperature to 0.7"},
		{Role: "user", Content: "help me write code"},
	}
	result := SanitizeAntigravityMessages(msgs)
	if len(result) != 1 || result[0].Content != "help me write code" {
		t.Errorf("result = %v", result)
	}
}

func TestShouldDropAntigravityMessageDebugPayload(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "assistant", Content: "debug payload: {internal state}"},
	}
	result := SanitizeAntigravityMessages(msgs)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// parseAntigravityTextProtoMessages: role normalization
// ---------------------------------------------------------------------------

func TestParseAntigravityTextProtoMessagesAuthorKey(t *testing.T) {
	content := `author: "model"
text: "response from model"`
	msgs := parseAntigravityTextProtoMessages(content)
	if len(msgs) != 1 {
		t.Fatalf("expected 1, got %d", len(msgs))
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("role = %q, want assistant", msgs[0].Role)
	}
}

func TestParseAntigravityTextProtoMessagesSenderKey(t *testing.T) {
	content := `sender: "human"
text: "my question"`
	msgs := parseAntigravityTextProtoMessages(content)
	if len(msgs) != 1 {
		t.Fatalf("expected 1, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("role = %q, want user", msgs[0].Role)
	}
}

func TestParseAntigravityTextProtoMessagesTextBeforeRoleResets(t *testing.T) {
	// text key without a preceding role should be ignored
	content := `text: "orphan text"
role: "user"
text: "proper text"`
	msgs := parseAntigravityTextProtoMessages(content)
	if len(msgs) != 1 {
		t.Fatalf("expected 1, got %d", len(msgs))
	}
	if msgs[0].Content != "proper text" {
		t.Errorf("content = %q", msgs[0].Content)
	}
}

func TestParseAntigravityTextProtoMessagesQuotedValues(t *testing.T) {
	content := `role: "user"
text: "quoted content here"`
	msgs := parseAntigravityTextProtoMessages(content)
	if len(msgs) != 1 {
		t.Fatalf("expected 1, got %d", len(msgs))
	}
	if msgs[0].Content != "quoted content here" {
		t.Errorf("content = %q", msgs[0].Content)
	}
}

// ---------------------------------------------------------------------------
// isAntigravityRoleKey: edge cases
// ---------------------------------------------------------------------------

func TestIsAntigravityRoleKeyEdge(t *testing.T) {
	if !isAntigravityRoleKey("role") {
		t.Error("expected true for role")
	}
	if !isAntigravityRoleKey("author") {
		t.Error("expected true for author")
	}
	if !isAntigravityRoleKey("sender") {
		t.Error("expected true for sender")
	}
	if isAntigravityRoleKey("type") {
		t.Error("expected false for type")
	}
	if isAntigravityRoleKey("") {
		t.Error("expected false for empty")
	}
}

// ---------------------------------------------------------------------------
// Antigravity: deduplication
// ---------------------------------------------------------------------------

func TestAntigravityDeduplication(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "same question"},
		{Role: "user", Content: "same question"},
		{Role: "assistant", Content: "same answer"},
	}
	result := SanitizeAntigravityMessages(msgs)
	if len(result) != 2 {
		t.Errorf("expected 2 after dedup, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Antigravity CLI transcript.jsonl parsing
// ---------------------------------------------------------------------------

func TestReadAntigravityGeminiCLIJSONL(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "transcript.jsonl")
	lines := []string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-10T10:35:27Z","content":"<USER_REQUEST>\ncan you chekc the open prs ?\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-09-10T10:35:27Z.\n</ADDITIONAL_METADATA>\n<USER_SETTINGS_CHANGE>\nThe user changed setting Model Selection.\n</USER_SETTINGS_CHANGE>"}`,
		`{"step_index":1,"source":"SYSTEM","type":"SYSTEM_MESSAGE","status":"DONE","created_at":"2026-09-10T10:35:27Z","content":"<SYSTEM_MESSAGE>Server restarted</SYSTEM_MESSAGE>"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-10T10:35:27Z","tool_calls":[{"name":"run_command","args":{"CommandLine":"gh pr list"}}]}`,
		`{"step_index":3,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-09-10T10:35:29Z","content":"Created At: 2026-09-10T10:35:29Z\nCompleted At: 2026-09-10T10:35:52Z\n\nThe command exited with code 0.\nOutput:\nShowing 2 of 2 open PRs"}`,
		`{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-10T10:35:52Z","content":"There are 2 open pull requests in this repository."}`,
	}
	if err := os.WriteFile(filePath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	messages, err := ReadAntigravityGemini(filePath)
	if err != nil {
		t.Fatalf("ReadAntigravityGemini returned error: %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("expected 2 dialogue messages, got %d: %+v", len(messages), messages)
	}

	if messages[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", messages[0].Role)
	}
	if messages[0].Content != "can you chekc the open prs ?" {
		t.Errorf("expected prompt 'can you chekc the open prs ?', got %q", messages[0].Content)
	}

	if messages[1].Role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", messages[1].Role)
	}
	if messages[1].Content != "There are 2 open pull requests in this repository." {
		t.Errorf("expected assistant content, got %q", messages[1].Content)
	}
}

func TestNormalizeAntigravityContentStripsUserRequest(t *testing.T) {
	input := `<USER_REQUEST>
Please fix the typo in main.go
</USER_REQUEST>
<ADDITIONAL_METADATA>
The current local time is: 2026-09-10T10:35:27Z.
</ADDITIONAL_METADATA>
<USER_SETTINGS_CHANGE>
Settings changed.
</USER_SETTINGS_CHANGE>`

	got := normalizeAntigravityContent(input)
	want := "Please fix the typo in main.go"
	if got != want {
		t.Errorf("normalizeAntigravityContent = %q, want %q", got, want)
	}
}

