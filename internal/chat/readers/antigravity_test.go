package readers

import (
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
