package readers

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeClaudeMessagesDropRules(t *testing.T) {
	testCases := []struct {
		name    string
		content string
		keep    bool
	}{
		{name: "drop meta", content: "<meta>trace metadata</meta>"},
		{name: "drop sidechain", content: "<sidechain>tool chatter</sidechain>"},
		{name: "drop compact summary", content: "<compact-summary>summary</compact-summary>"},
		{name: "drop local command wrapper", content: "<local-command-stdout>Set model</local-command-stdout>"},
		{name: "drop task lifecycle notification", content: "<task-notification><task-id>id</task-id><status>completed</status></task-notification>"},
		{name: "drop async chatter", content: "Async agent launched successfully. agentId: a1 output_file: C:\\temp\\x"},
		{name: "keep folded tool result", content: "[Bash] go test\n→ PASS", keep: true},
		{name: "keep user prompt", content: "Please help me debug this regression.", keep: true},
		{name: "keep assistant response", content: "Sure — share the stack trace.", keep: true},
		{name: "keep error details", content: "error: resume manifest file missing checkpoint", keep: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			messages := SanitizeClaudeMessages([]ChatMessage{{Role: "user", Content: tc.content}})
			if tc.keep && len(messages) != 1 {
				t.Fatalf("expected message to be kept, got %d", len(messages))
			}
			if !tc.keep && len(messages) != 0 {
				t.Fatalf("expected message to be dropped, got %d", len(messages))
			}
		})
	}
}

func TestSanitizeClaudeMessagesNormalizesDedupesAndTruncates(t *testing.T) {
	longContent := strings.Repeat("界", claudeMaxCharsPerMessage+16)
	messages := SanitizeClaudeMessages([]ChatMessage{
		{Role: "user", Content: "\x1b[31m<note>  Hello\n\nworld  </note>\x1b[0m"},
		{Role: "user", Content: " Hello   world "},
		{Role: "assistant", Content: "<wrapper> \n\t </wrapper>"},
		{Role: "assistant", Content: "Stable reply"},
		{Role: "user", Content: longContent},
	})

	if len(messages) != 3 {
		t.Fatalf("expected 3 messages after sanitizing, got %d", len(messages))
	}
	if got := messages[0].Content; got != "Hello world" {
		t.Fatalf("first content = %q, want %q", got, "Hello world")
	}
	if got := messages[1].Content; got != "Stable reply" {
		t.Fatalf("second content = %q, want %q", got, "Stable reply")
	}
	if got := utf8.RuneCountInString(messages[2].Content); got != claudeMaxCharsPerMessage {
		t.Fatalf("third rune length = %d, want %d", got, claudeMaxCharsPerMessage)
	}
}

func TestSanitizeClaudeMessagesCapsMessagesPerSource(t *testing.T) {
	input := make([]ChatMessage, 0, claudeMaxMessagesPerSource+5)
	for i := 0; i < claudeMaxMessagesPerSource+5; i++ {
		input = append(input, ChatMessage{
			Role:    "user",
			Content: fmt.Sprintf("msg-%03d", i),
		})
	}

	messages := SanitizeClaudeMessages(input)
	if len(messages) != claudeMaxMessagesPerSource {
		t.Fatalf("len(messages) = %d, want %d", len(messages), claudeMaxMessagesPerSource)
	}
	if got := messages[0].Content; got != "msg-000" {
		t.Fatalf("first content = %q, want %q", got, "msg-000")
	}
	lastExpected := fmt.Sprintf("msg-%03d", claudeMaxMessagesPerSource-1)
	if got := messages[len(messages)-1].Content; got != lastExpected {
		t.Fatalf("last content = %q, want %q", got, lastExpected)
	}
}
