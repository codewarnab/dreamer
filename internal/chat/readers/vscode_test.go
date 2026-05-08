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
