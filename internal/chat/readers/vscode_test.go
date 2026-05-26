package readers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadVSCodeChatJSONReadsRequestsAndTextResponses(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "chat.json")
	contents := `{
		"requests": [
			{
				"message": {"text": "Explain the failing daemon test."},
				"response": [
					{"kind": "markdownContent", "value": "The state file is shared."},
					{"kind": "text", "text": "Use isolated project state."}
				],
				"variableData": {"files": ["large context"]}
			}
		]
	}`

	if err := os.WriteFile(filePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadVSCodeChat(filePath)
	if err != nil {
		t.Fatalf("ReadVSCodeChat returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "Explain the failing daemon test." {
		t.Fatalf("first message = %+v, want user prompt", messages[0])
	}
	if messages[1].Role != "assistant" || !strings.Contains(messages[1].Content, "state file is shared") || !strings.Contains(messages[1].Content, "isolated project state") {
		t.Fatalf("second message = %+v, want assistant response text", messages[1])
	}
}

func TestReadVSCodeChatJSONLDropsVariableDataInstructionsAndToolInvocations(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "chat.jsonl")
	contents := strings.Join([]string{
		`{"v":{"requests":[{"message":{"text":"Can this parser drop noise?"},"variableData":{"selectedText":"large file context"},"response":[]}]}}`,
		`{"p":["requests",0,"response"],"v":[{"kind":"thinking","text":"hidden reasoning"},{"kind":"markdownContent","value":"Yes, keep the useful answer."},{"kind":"toolInvocationSerialized","value":{"toolCallId":"call-1","output":"large tool output"}},{"kind":"markdownContent","value":"Instructions: internal bootstrap"}]}`,
	}, "\n")

	if err := os.WriteFile(filePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	messages, err := ReadVSCodeChat(filePath)
	if err != nil {
		t.Fatalf("ReadVSCodeChat returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if got := messages[0].Content; got != "Can this parser drop noise?" {
		t.Fatalf("user message content = %q, want prompt", got)
	}
	assistantContent := strings.ToLower(messages[1].Content)
	if !strings.Contains(assistantContent, "keep the useful answer") {
		t.Fatalf("assistant content = %q, want natural language response", messages[1].Content)
	}
	for _, unwanted := range []string{"selectedtext", "hidden reasoning", "toolcallid", "large tool output", "instructions"} {
		if strings.Contains(assistantContent, unwanted) {
			t.Fatalf("assistant content should not contain %q: %q", unwanted, messages[1].Content)
		}
	}
}

// ---------------------------------------------------------------------------
// ReadVSCodeChat: unsupported extension
// ---------------------------------------------------------------------------

func TestReadVSCodeChatUnsupportedExtension(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "chat.xml")
	if err := os.WriteFile(filePath, []byte("<root/>"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := ReadVSCodeChat(filePath)
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
}

// ---------------------------------------------------------------------------
// readVSCodeChatJSON: file read error
// ---------------------------------------------------------------------------

func TestReadVSCodeChatJSONFileReadError(t *testing.T) {
	_, err := readVSCodeChatJSON(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// ---------------------------------------------------------------------------
// readVSCodeChatJSON: parse error
// ---------------------------------------------------------------------------

func TestReadVSCodeChatJSONParseError(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(filePath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := readVSCodeChatJSON(filePath)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

// ---------------------------------------------------------------------------
// readVSCodeChatJSONL: file open error
// ---------------------------------------------------------------------------

func TestReadVSCodeChatJSONLOpenError(t *testing.T) {
	_, err := readVSCodeChatJSONL(filepath.Join(t.TempDir(), "missing.jsonl"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// ---------------------------------------------------------------------------
// intFromValue: all type branches
// ---------------------------------------------------------------------------

func TestIntFromValueFloat64(t *testing.T) {
	got, ok := intFromValue(float64(42))
	if !ok || got != 42 {
		t.Errorf("intFromValue(float64(42)) = %d, %v", got, ok)
	}
}

func TestIntFromValueInt(t *testing.T) {
	got, ok := intFromValue(int(7))
	if !ok || got != 7 {
		t.Errorf("intFromValue(int(7)) = %d, %v", got, ok)
	}
}

func TestIntFromValueString(t *testing.T) {
	got, ok := intFromValue("3")
	if !ok || got != 3 {
		t.Errorf("intFromValue(\"3\") = %d, %v", got, ok)
	}
}

func TestIntFromValueStringInvalid(t *testing.T) {
	_, ok := intFromValue("not-a-number")
	if ok {
		t.Error("expected !ok for invalid string")
	}
}

func TestIntFromValueBool(t *testing.T) {
	_, ok := intFromValue(true)
	if ok {
		t.Error("expected !ok for bool")
	}
}

// ---------------------------------------------------------------------------
// vscodeDeltaRequestIndex: path string branch
// ---------------------------------------------------------------------------

func TestVSCodeDeltaRequestIndexFromPathString(t *testing.T) {
	record := map[string]any{
		"path": "/requests/2/response",
		"value": "some delta",
	}
	idx, ok := vscodeDeltaRequestIndex(record)
	if !ok {
		t.Fatal("expected ok from path string")
	}
	if idx != 2 {
		t.Errorf("index = %d, want 2", idx)
	}
}

func TestVSCodeDeltaRequestIndexFromPathArray(t *testing.T) {
	record := map[string]any{
		"p": []any{"requests", float64(1), "response"},
		"v": "delta value",
	}
	idx, ok := vscodeDeltaRequestIndex(record)
	if !ok {
		t.Fatal("expected ok from path array")
	}
	if idx != 1 {
		t.Errorf("index = %d, want 1", idx)
	}
}

func TestVSCodeDeltaRequestIndexNoPath(t *testing.T) {
	record := map[string]any{
		"value": "delta",
	}
	_, ok := vscodeDeltaRequestIndex(record)
	if ok {
		t.Error("expected !ok for record without path")
	}
}

func TestVSCodeDeltaRequestIndexPathStringNoRequests(t *testing.T) {
	record := map[string]any{
		"path": "/other/0/response",
	}
	_, ok := vscodeDeltaRequestIndex(record)
	if ok {
		t.Error("expected !ok for path without requests segment")
	}
}

func TestVSCodeDeltaRequestIndexPathArrayNoRequests(t *testing.T) {
	record := map[string]any{
		"p": []any{"other", float64(0), "response"},
	}
	_, ok := vscodeDeltaRequestIndex(record)
	if ok {
		t.Error("expected !ok for path array without requests segment")
	}
}

// ---------------------------------------------------------------------------
// applyVSCodeResponseDelta: value key fallback
// ---------------------------------------------------------------------------

func TestApplyVSCodeResponseDeltaWithVKey(t *testing.T) {
	document := map[string]any{
		"requests": []any{
			map[string]any{},
		},
	}
	record := map[string]any{
		"p": []any{"requests", float64(0), "response"},
		"v": "delta via v key",
	}
	applyVSCodeResponseDelta(document, record)
	req := document["requests"].([]any)[0].(map[string]any)
	if req["response"] != "delta via v key" {
		t.Errorf("response = %v, want %q", req["response"], "delta via v key")
	}
}

func TestApplyVSCodeResponseDeltaWithValueKey(t *testing.T) {
	document := map[string]any{
		"requests": []any{
			map[string]any{},
		},
	}
	record := map[string]any{
		"path":  "/requests/0/response",
		"value": "delta via value key",
	}
	applyVSCodeResponseDelta(document, record)
	req := document["requests"].([]any)[0].(map[string]any)
	if req["response"] != "delta via value key" {
		t.Errorf("response = %v, want %q", req["response"], "delta via value key")
	}
}

func TestApplyVSCodeResponseDeltaInvalidIndex(t *testing.T) {
	document := map[string]any{
		"requests": []any{
			map[string]any{},
		},
	}
	record := map[string]any{
		"path":  "/requests/5/response",
		"value": "out of bounds",
	}
	applyVSCodeResponseDelta(document, record)
	// Should not panic; document unchanged.
	req := document["requests"].([]any)[0].(map[string]any)
	if _, ok := req["response"]; ok {
		t.Error("expected no response to be set for out-of-bounds index")
	}
}

func TestApplyVSCodeResponseDeltaNegativeIndex(t *testing.T) {
	document := map[string]any{
		"requests": []any{
			map[string]any{},
		},
	}
	record := map[string]any{
		"path":  "/requests/-1/response",
		"value": "negative",
	}
	applyVSCodeResponseDelta(document, record)
	req := document["requests"].([]any)[0].(map[string]any)
	if _, ok := req["response"]; ok {
		t.Error("expected no response for negative index")
	}
}

func TestApplyVSCodeResponseDeltaNoRequestsInDoc(t *testing.T) {
	document := map[string]any{}
	record := map[string]any{
		"path":  "/requests/0/response",
		"value": "no requests",
	}
	applyVSCodeResponseDelta(document, record)
	// Should not panic; no requests to update.
	if _, ok := document["response"]; ok {
		t.Error("expected no change to empty document")
	}
}

// ---------------------------------------------------------------------------
// applyVSCodeChatJSONLRecord: snapshot with requests
// ---------------------------------------------------------------------------

func TestApplyVSCodeChatJSONLRecordSnapshot(t *testing.T) {
	document := map[string]any{}
	record := map[string]any{
		"v": map[string]any{
			"requests": []any{
				map[string]any{"message": map[string]any{"text": "hi"}},
			},
		},
	}
	applyVSCodeChatJSONLRecord(document, record)
	reqs, ok := document["requests"].([]any)
	if !ok || len(reqs) != 1 {
		t.Fatalf("expected 1 request from snapshot, got %v", document["requests"])
	}
}

func TestApplyVSCodeChatJSONLRecordRequestsDirect(t *testing.T) {
	document := map[string]any{}
	record := map[string]any{
		"requests": []any{
			map[string]any{"message": map[string]any{"text": "direct"}},
		},
	}
	applyVSCodeChatJSONLRecord(document, record)
	reqs, ok := document["requests"].([]any)
	if !ok || len(reqs) != 1 {
		t.Fatalf("expected 1 request from direct key, got %v", document["requests"])
	}
}

// ---------------------------------------------------------------------------
// isNoisyVSCodeChatKey
// ---------------------------------------------------------------------------

func TestIsNoisyVSCodeChatKey(t *testing.T) {
	noisyKeys := []string{
		"variableData", "inputState", "model", "editor",
		"workspace", "metadata", "instructions", "skills",
		"agents", "thinking", "toolInvocationSerialized",
		"toolCallId", "toolCallIds", "toolUseId", "mcp", "server", "tool",
	}
	for _, key := range noisyKeys {
		if !isNoisyVSCodeChatKey(key) {
			t.Errorf("isNoisyVSCodeChatKey(%q) = false, want true", key)
		}
	}
	if isNoisyVSCodeChatKey("markdown") {
		t.Error("isNoisyVSCodeChatKey(markdown) should be false")
	}
}

// ---------------------------------------------------------------------------
// shouldSkipVSCodeResponseMap: type field
// ---------------------------------------------------------------------------

func TestShouldSkipVSCodeResponseMapWithTypeField(t *testing.T) {
	record := map[string]any{"type": "thinking"}
	if !shouldSkipVSCodeResponseMap(record) {
		t.Error("expected skip for type=thinking")
	}
}

func TestShouldSkipVSCodeResponseMapWithKindField(t *testing.T) {
	record := map[string]any{"kind": "tool"}
	if !shouldSkipVSCodeResponseMap(record) {
		t.Error("expected skip for kind=tool")
	}
}

func TestShouldSkipVSCodeResponseMapClean(t *testing.T) {
	record := map[string]any{"kind": "markdownContent", "value": "text"}
	if shouldSkipVSCodeResponseMap(record) {
		t.Error("expected no skip for kind=markdownContent")
	}
}

// ---------------------------------------------------------------------------
// normalizeVSCodeChatText
// ---------------------------------------------------------------------------

func TestNormalizeVSCodeChatTextEmpty(t *testing.T) {
	if got := normalizeVSCodeChatText(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := normalizeVSCodeChatText("   "); got != "" {
		t.Errorf("got %q, want empty for whitespace", got)
	}
}

func TestNormalizeVSCodeChatTextCollapsesWhitespace(t *testing.T) {
	got := normalizeVSCodeChatText("  hello\n\n  world  ")
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

// ---------------------------------------------------------------------------
// vscodeMessagesFromDocument: no requests key
// ---------------------------------------------------------------------------

func TestVSCodeMessagesFromDocumentNoRequests(t *testing.T) {
	got := vscodeMessagesFromDocument(map[string]any{})
	if got != nil {
		t.Errorf("expected nil for document without requests, got %v", got)
	}
}

func TestVSCodeMessagesFromDocumentNonArrayRequests(t *testing.T) {
	got := vscodeMessagesFromDocument(map[string]any{"requests": "not-an-array"})
	if got != nil {
		t.Errorf("expected nil for non-array requests, got %v", got)
	}
}

func TestVSCodeMessagesFromDocumentNonMapRequestElements(t *testing.T) {
	got := vscodeMessagesFromDocument(map[string]any{"requests": []any{"string", 42}})
	if len(got) != 0 {
		t.Errorf("expected 0 messages for non-map request elements, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// collectVSCodeResponseText: max depth
// ---------------------------------------------------------------------------

func TestCollectVSCodeResponseTextMaxDepth(t *testing.T) {
	// Build deeply nested structure beyond MaxDepth
	var nested any = "deep text"
	for i := 0; i <= MaxDepth+2; i++ {
		nested = map[string]any{"nested": nested}
	}
	var parts []string
	collectVSCodeResponseText(nested, &parts, 0)
	// Should not find text because it's beyond max depth.
	if len(parts) != 0 {
		t.Errorf("expected 0 parts from deeply nested value, got %d", len(parts))
	}
}

// ---------------------------------------------------------------------------
// collectVSCodeResponseText: nil value
// ---------------------------------------------------------------------------

func TestCollectVSCodeResponseTextNil(t *testing.T) {
	var parts []string
	collectVSCodeResponseText(nil, &parts, 0)
	if len(parts) != 0 {
		t.Errorf("expected 0 parts for nil, got %d", len(parts))
	}
}

// ---------------------------------------------------------------------------
// collectVSCodeResponseText: nested map with noisy key
// ---------------------------------------------------------------------------

func TestCollectVSCodeResponseTextSkipsNoisyKeys(t *testing.T) {
	value := map[string]any{
		"markdown": "useful text",
		"metadata": "should be skipped",
	}
	var parts []string
	collectVSCodeResponseText(value, &parts, 0)
	if len(parts) != 1 || parts[0] != "useful text" {
		t.Errorf("parts = %v, want [useful text]", parts)
	}
}

// ---------------------------------------------------------------------------
// collectVSCodeResponseText: noise content in map stops exploration
// ---------------------------------------------------------------------------

func TestCollectVSCodeResponseTextNoisyMapContentStops(t *testing.T) {
	value := map[string]any{
		"markdown": "Instructions: internal bootstrap text",
	}
	var parts []string
	collectVSCodeResponseText(value, &parts, 0)
	// Content starts with "instructions:" so it's noisy — should be dropped.
	if len(parts) != 0 {
		t.Errorf("expected 0 parts for noisy content, got %d: %v", len(parts), parts)
	}
}

// ---------------------------------------------------------------------------
// ReadVSCodeChatJSONL: malformed lines skipped
// ---------------------------------------------------------------------------

func TestReadVSCodeChatJSONLSkipsMalformedLines(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "malformed.jsonl")
	contents := strings.Join([]string{
		`{not valid json}`,
		`{"v":{"requests":[{"message":{"text":"hello"},"response":[]}]}}`,
		``, // empty line
	}, "\n")
	if err := os.WriteFile(filePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	messages, err := readVSCodeChatJSONL(filePath)
	if err != nil {
		t.Fatalf("readVSCodeChatJSONL: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "hello" {
		t.Errorf("messages = %v, want [hello]", messages)
	}
}

// ---------------------------------------------------------------------------
// ReadVSCodeChat: JSONL extension
// ---------------------------------------------------------------------------

func TestReadVSCodeChatJSONLExtension(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "chat.JSONL") // uppercase
	contents := `{"v":{"requests":[{"message":{"text":"test"},"response":[]}]}}`
	if err := os.WriteFile(filePath, []byte(contents+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	messages, err := ReadVSCodeChat(filePath)
	if err != nil {
		t.Fatalf("ReadVSCodeChat: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "test" {
		t.Errorf("messages = %v", messages)
	}
}

// ---------------------------------------------------------------------------
// isNoisyVSCodeChatContent
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// collectVSCodeResponseText: map fallback to generic key iteration
// ---------------------------------------------------------------------------

func TestCollectVSCodeResponseTextMapFallbackToGenericKeys(t *testing.T) {
	// Map where recognized keys (markdown, text, value, content) don't exist,
	// so it falls through to iterating all non-noisy keys.
	value := map[string]any{
		"foo": "non-noisy value",
	}
	var parts []string
	collectVSCodeResponseText(value, &parts, 0)
	if len(parts) != 1 || parts[0] != "non-noisy value" {
		t.Errorf("parts = %v, want [non-noisy value]", parts)
	}
}

func TestCollectVSCodeResponseTextMapNoisyKeyValue(t *testing.T) {
	// Map with recognized key that contains noisy content.
	value := map[string]any{
		"markdown": "Instructions: do not show this",
	}
	var parts []string
	collectVSCodeResponseText(value, &parts, 0)
	if len(parts) != 0 {
		t.Errorf("expected 0 parts for noisy recognized key content, got %d: %v", len(parts), parts)
	}
}

// ---------------------------------------------------------------------------
// roleFromRecord: plain "role" key
// ---------------------------------------------------------------------------

func TestRoleFromRecordRoleKey(t *testing.T) {
	record := map[string]any{"role": "user"}
	got := roleFromRecord(record)
	if got != "user" {
		t.Errorf("roleFromRecord(role=user) = %q, want user", got)
	}
}

func TestIsNoisyVSCodeChatContentEdgeCases(t *testing.T) {
	noisy := []string{
		"INSTRUCTIONS: do this",
		"Skills: some skill list",
		"Agents: agent config",
		"Contains mcp server connection",
		"tool call id: abc123",
	}
	for _, content := range noisy {
		if !isNoisyVSCodeChatContent(content) {
			t.Errorf("isNoisyVSCodeChatContent(%q) = false, want true", content)
		}
	}
	clean := []string{
		"The bug is in the parser.",
		"Fix the nil check.",
	}
	for _, content := range clean {
		if isNoisyVSCodeChatContent(content) {
			t.Errorf("isNoisyVSCodeChatContent(%q) = true, want false", content)
		}
	}
}
