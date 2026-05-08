package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dreamer/internal/chat"
	"dreamer/internal/config"
	"dreamer/internal/state"
)

func TestFilterSourcesToAnalyzeReturnsNewAndUpdatedSources(t *testing.T) {
	lastRun := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	sources := []chat.ChatSource{
		{
			Path:         "old.jsonl",
			ModifiedTime: lastRun.Add(-time.Minute),
		},
		{
			Path:         "updated.jsonl",
			ModifiedTime: lastRun.Add(time.Minute),
		},
		{
			Path:         "new.jsonl",
			ModifiedTime: lastRun.Add(-time.Minute),
		},
	}

	filtered := filterSourcesToAnalyze(sources, &state.State{
		LastRun:         lastRun,
		AnalyzedChatIDs: []string{"old.jsonl", "updated.jsonl"},
	})

	if got, want := len(filtered), 2; got != want {
		t.Fatalf("len(filtered) = %d, want %d", got, want)
	}
	if got, want := filtered[0].Path, "updated.jsonl"; got != want {
		t.Fatalf("filtered[0].Path = %q, want %q", got, want)
	}
	if got, want := filtered[1].Path, "new.jsonl"; got != want {
		t.Fatalf("filtered[1].Path = %q, want %q", got, want)
	}
}

func TestMergeAnalyzedIDsDeduplicatesAndPreservesOrder(t *testing.T) {
	merged := mergeAnalyzedIDs(
		[]string{"chat-a", "chat-b", "chat-a"},
		[]string{"chat-b", "chat-c", "chat-c"},
	)

	expected := []string{"chat-a", "chat-b", "chat-c"}
	if len(merged) != len(expected) {
		t.Fatalf("len(merged) = %d, want %d", len(merged), len(expected))
	}
	for i := range expected {
		if merged[i] != expected[i] {
			t.Fatalf("merged[%d] = %q, want %q", i, merged[i], expected[i])
		}
	}
}

func TestBuildAnalysisInputReturnsErrorForUnreadableSource(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(sourcePath, []byte(`{"event":"heartbeat"}`), 0o644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}

	_, _, _, err := buildAnalysisInput([]chat.ChatSource{
		{
			Path: sourcePath,
			Tool: chat.SourceTypeCopilotSessionJSONL,
		},
	})
	if err == nil {
		t.Fatalf("buildAnalysisInput expected unreadable source error")
	}
	if !strings.Contains(err.Error(), "did not contain readable messages") {
		t.Fatalf("error = %q, want unreadable source error", err)
	}
}

func TestBuildAnalysisInputSkipsUnreadableAntigravitySource(t *testing.T) {
	pbPath := filepath.Join(t.TempDir(), "unreadable.pb")
	if err := os.WriteFile(pbPath, []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatalf("write protobuf fixture: %v", err)
	}

	jsonlPath := filepath.Join(t.TempDir(), "readable.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(`{"role":"user","content":"hello from jsonl"}`), 0o644); err != nil {
		t.Fatalf("write jsonl fixture: %v", err)
	}

	input, sourceIDs, messageCount, err := buildAnalysisInput([]chat.ChatSource{
		{Path: pbPath, Tool: chat.SourceTypeAntigravityGemini},
		{Path: jsonlPath, Tool: chat.SourceTypeCopilotSessionJSONL},
	})
	if err != nil {
		t.Fatalf("buildAnalysisInput returned error: %v", err)
	}
	if messageCount != 1 {
		t.Fatalf("messageCount = %d, want 1", messageCount)
	}
	if len(sourceIDs) != 1 || sourceIDs[0] != jsonlPath {
		t.Fatalf("sourceIDs = %v, want only readable jsonl source", sourceIDs)
	}
	if !strings.Contains(input, "hello from jsonl") {
		t.Fatalf("analysis input missing readable jsonl content: %q", input)
	}
}

func TestReadMessagesFromSourceRejectsUnsupportedExtension(t *testing.T) {
	_, err := readMessagesFromSource(chat.ChatSource{
		Path: "chat.json",
		Tool: chat.SourceTypeVSCodeChatSession,
	})
	if err == nil {
		t.Fatalf("readMessagesFromSource expected unsupported extension error")
	}
	if !strings.Contains(err.Error(), "supported: .jsonl, .pb, .pbtxt") {
		t.Fatalf("error = %q, want supported extension message", err)
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

func TestAnalyzerClientOptionsFromConfigUsesAnalyzerSettings(t *testing.T) {
	useLoggedInUser := true
	autoStart := true
	cfg := &config.Config{
		Analyzer: config.AnalyzerConfig{
			Model:           "gpt-5",
			CopilotHome:     `C:\Users\User\.copilot`,
			CLIURL:          "localhost:4321",
			UseLoggedInUser: &useLoggedInUser,
			AutoStart:       &autoStart,
		},
	}

	options := analyzerClientOptionsFromConfig(cfg, `C:\Users\User\code\dreamer`)
	if options.Model != "gpt-5" {
		t.Fatalf("options.Model = %q, want %q", options.Model, "gpt-5")
	}
	if options.CopilotHome != `C:\Users\User\.copilot` {
		t.Fatalf("options.CopilotHome = %q, want %q", options.CopilotHome, `C:\Users\User\.copilot`)
	}
	if options.CLIURL != "localhost:4321" {
		t.Fatalf("options.CLIURL = %q, want %q", options.CLIURL, "localhost:4321")
	}
	if !options.UseLoggedInUser {
		t.Fatalf("options.UseLoggedInUser should be true")
	}
	if !options.AutoStart {
		t.Fatalf("options.AutoStart should be true")
	}
	if options.WorkingDirectory != `C:\Users\User\code\dreamer` {
		t.Fatalf("options.WorkingDirectory = %q, want %q", options.WorkingDirectory, `C:\Users\User\code\dreamer`)
	}
}

func TestWrapAnalyzerIntegrationErrorIncludesAnalyzerPrefix(t *testing.T) {
	wrapped := wrapAnalyzerIntegrationError(os.ErrPermission)
	if wrapped == nil {
		t.Fatalf("wrapped error should not be nil")
	}
	if !strings.Contains(wrapped.Error(), "analyzer request failed") {
		t.Fatalf("error = %q, want analyzer error prefix", wrapped)
	}
}
