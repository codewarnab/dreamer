package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		LastRunUTC: lastRun,
		ChatHashes: map[string]string{"old.jsonl": "", "updated.jsonl": ""},
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

func TestLookbackAndStateFilteringApplyTogether(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	lastRun := now.Add(-2 * time.Hour)
	sources := []chat.ChatSource{
		{
			Path:         "old-never-analyzed.jsonl",
			ModifiedTime: now.Add(-25 * time.Hour),
		},
		{
			Path:         "recent-unchanged.jsonl",
			ModifiedTime: lastRun.Add(-time.Minute),
		},
		{
			Path:         "recent-changed.jsonl",
			ModifiedTime: now.Add(-time.Minute),
		},
		{
			Path:         "recent-new.jsonl",
			ModifiedTime: now.Add(-30 * time.Minute),
		},
	}

	withinLookback := filterSourcesByLookback(sources, now, 24*time.Hour, true)
	filtered := filterSourcesToAnalyze(withinLookback, &state.State{
		LastRunUTC: lastRun,
		ChatHashes: map[string]string{
			"recent-unchanged.jsonl": "",
			"recent-changed.jsonl":   "",
		},
	})

	assertStringSliceEqual(t, sourcePaths(filtered), []string{
		"recent-changed.jsonl",
		"recent-new.jsonl",
	})
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

func TestBuildAnalysisInputWithDiagnosticsFormatsAntigravityInput(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "antigravity.pbtxt")
	contents := strings.Join([]string{
		`workspacePath: "C:\\Users\\User\\code\\dreamer"`,
		`role: "user"`,
		`text: "Please fix Antigravity discovery."`,
		`role: "model"`,
		`text: "I will keep the reader cleanup localized."`,
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write antigravity fixture: %v", err)
	}

	input, sourceIDs, messageCount, _, err := buildAnalysisInputWithDiagnostics([]chat.ChatSource{
		{
			Path: sourcePath,
			Tool: chat.SourceTypeAntigravityGemini,
		},
	})
	if err != nil {
		t.Fatalf("buildAnalysisInputWithDiagnostics returned error: %v", err)
	}
	if messageCount != 2 {
		t.Fatalf("messageCount = %d, want 2", messageCount)
	}
	assertStringSliceEqual(t, sourceIDs, []string{sourcePath})

	expected := fmt.Sprintf(
		"source: %s\ntool: %s\n\nuser: Please fix Antigravity discovery.\nassistant: I will keep the reader cleanup localized.\n\n",
		sourcePath,
		chat.SourceTypeAntigravityGemini,
	)
	if input != expected {
		t.Fatalf("analysis input mismatch\n--- got ---\n%s--- want ---\n%s", input, expected)
	}
}

func TestBuildAnalysisInputWithDiagnosticsSkipsNoMessageAntigravityWithoutLeakingSourceID(t *testing.T) {
	noMessagePath := filepath.Join(t.TempDir(), "metadata-only.pb")
	payload := encodeLengthDelimitedField(1, []byte("Antigravity metadata without role context"))
	if err := os.WriteFile(noMessagePath, payload, 0o644); err != nil {
		t.Fatalf("write antigravity fixture: %v", err)
	}

	readablePath := filepath.Join(t.TempDir(), "readable.jsonl")
	if err := os.WriteFile(readablePath, []byte(`{"role":"user","content":"hello from jsonl"}`), 0o644); err != nil {
		t.Fatalf("write jsonl fixture: %v", err)
	}

	input, sourceIDs, messageCount, _, err := buildAnalysisInputWithDiagnostics([]chat.ChatSource{
		{Path: noMessagePath, Tool: chat.SourceTypeAntigravityGemini},
		{Path: readablePath, Tool: chat.SourceTypeCopilotSessionJSONL},
	})
	if err != nil {
		t.Fatalf("buildAnalysisInputWithDiagnostics returned error: %v", err)
	}
	if messageCount != 1 {
		t.Fatalf("messageCount = %d, want 1", messageCount)
	}
	assertStringSliceEqual(t, sourceIDs, []string{readablePath})
	if strings.Contains(input, noMessagePath) {
		t.Fatalf("analysis input leaked no-message Antigravity source: %q", input)
	}
}

func TestBuildAnalysisInputWithDiagnosticsPreservesNonClaudeFormat(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "session.jsonl")
	contents := strings.Join([]string{
		`{"role":"user","content":"hello","timestamp":"2026-05-08T12:00:01Z"}`,
		`{"role":"assistant","content":"world"}`,
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}

	input, sourceIDs, messageCount, diagnostics, err := buildAnalysisInputWithDiagnostics([]chat.ChatSource{
		{
			Path: sourcePath,
			Tool: chat.SourceTypeCopilotSessionJSONL,
		},
	})
	if err != nil {
		t.Fatalf("buildAnalysisInputWithDiagnostics returned error: %v", err)
	}
	if messageCount != 2 {
		t.Fatalf("messageCount = %d, want 2", messageCount)
	}
	if len(sourceIDs) != 1 || sourceIDs[0] != sourcePath {
		t.Fatalf("sourceIDs = %v, want [%q]", sourceIDs, sourcePath)
	}
	if diagnostics.TotalMessagesRead != 0 || diagnostics.MessagesKept != 0 || diagnostics.MessagesDropped != 0 || diagnostics.MessagesTruncated != 0 {
		t.Fatalf("diagnostics = %+v, want zero Claude diagnostics", diagnostics)
	}

	expected := fmt.Sprintf(
		"source: %s\ntool: %s\n\n[2026-05-08T12:00:01Z] user: hello\nassistant: world\n\n",
		sourcePath,
		chat.SourceTypeCopilotSessionJSONL,
	)
	if input != expected {
		t.Fatalf("analysis input mismatch\n--- got ---\n%s--- want ---\n%s", input, expected)
	}
}

func TestBuildAnalysisInputWithDiagnosticsSanitizesClaudeAndTracksVolume(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "claude-session.jsonl")
	contents := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"<local-command-stdout>Set model</local-command-stdout>"},"timestamp":"2026-05-08T12:00:00Z"}`,
		`{"type":"user","message":{"role":"user","content":"Need help\nwith tests"},"timestamp":"2026-05-08T12:00:01Z"}`,
		`{"type":"user","message":{"role":"user","content":"Need   help with tests"},"timestamp":"2026-05-08T12:00:02Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"Sure, share go test output."},"timestamp":"2026-05-08T12:00:03Z"}`,
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}

	input, sourceIDs, messageCount, diagnostics, err := buildAnalysisInputWithDiagnostics([]chat.ChatSource{
		{
			Path: sourcePath,
			Tool: chat.SourceTypeClaudeCodeSession,
		},
	})
	if err != nil {
		t.Fatalf("buildAnalysisInputWithDiagnostics returned error: %v", err)
	}
	if messageCount != 2 {
		t.Fatalf("messageCount = %d, want 2", messageCount)
	}
	if len(sourceIDs) != 1 || sourceIDs[0] != sourcePath {
		t.Fatalf("sourceIDs = %v, want [%q]", sourceIDs, sourcePath)
	}
	if diagnostics.TotalMessagesRead != 4 {
		t.Fatalf("diagnostics.TotalMessagesRead = %d, want 4", diagnostics.TotalMessagesRead)
	}
	if diagnostics.MessagesKept != 2 {
		t.Fatalf("diagnostics.MessagesKept = %d, want 2", diagnostics.MessagesKept)
	}
	if diagnostics.MessagesDropped != 2 {
		t.Fatalf("diagnostics.MessagesDropped = %d, want 2", diagnostics.MessagesDropped)
	}

	expected := fmt.Sprintf(
		"source: %s\ntool: %s\n\n[2026-05-08T12:00:01Z] user: Need help with tests\n[2026-05-08T12:00:03Z] assistant: Sure, share go test output.\n\n",
		sourcePath,
		chat.SourceTypeClaudeCodeSession,
	)
	if input != expected {
		t.Fatalf("analysis input mismatch\n--- got ---\n%s--- want ---\n%s", input, expected)
	}
	if strings.Contains(input, "<local-command-stdout>") {
		t.Fatalf("analysis input should not include dropped Claude wrapper content: %q", input)
	}
}

func TestBuildAnalysisInputWithDiagnosticsSanitizesCodexSession(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "codex-session.jsonl")
	contents := strings.Join([]string{
		`{"type":"session_meta","payload":{"cwd":"C:\\repo"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions instructions>internal runtime policy</permissions instructions>"}]},"timestamp":"2026-05-08T12:00:00Z"}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for C:\\repo\n\n<INSTRUCTIONS>\nPrioritize readability over cleverness.\n</INSTRUCTIONS>"}]},"timestamp":"2026-05-08T12:00:01Z"}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":{"type":"input_text","text":"Need cleanup for Codex chat analysis."}},"timestamp":"2026-05-08T12:00:02Z"}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"shell_command","arguments":"{\"command\":\"rg TODO\"}","call_id":"call-1"},"timestamp":"2026-05-08T12:00:03Z"}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"call-1","output":"Exit code: 0\nOutput:\nlarge tool output"},"timestamp":"2026-05-08T12:00:04Z"}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":{"type":"output_text","text":"Codex analysis should now skip bootstrap instructions."}},"timestamp":"2026-05-08T12:00:05Z"}`,
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}

	input, sourceIDs, messageCount, diagnostics, err := buildAnalysisInputWithDiagnostics([]chat.ChatSource{
		{
			Path: sourcePath,
			Tool: chat.SourceTypeCodexSessionJSONL,
		},
	})
	if err != nil {
		t.Fatalf("buildAnalysisInputWithDiagnostics returned error: %v", err)
	}
	if messageCount != 2 {
		t.Fatalf("messageCount = %d, want 2", messageCount)
	}
	if len(sourceIDs) != 1 || sourceIDs[0] != sourcePath {
		t.Fatalf("sourceIDs = %v, want [%q]", sourceIDs, sourcePath)
	}
	if diagnostics.TotalMessagesRead != 0 || diagnostics.MessagesKept != 0 || diagnostics.MessagesDropped != 0 || diagnostics.MessagesTruncated != 0 {
		t.Fatalf("diagnostics = %+v, want zero Claude diagnostics", diagnostics)
	}
	if strings.Contains(input, "AGENTS.md instructions") || strings.Contains(input, "Prioritize readability") {
		t.Fatalf("analysis input should not include Codex bootstrap instructions: %q", input)
	}
	if strings.Contains(input, "rg TODO") || strings.Contains(input, "large tool output") {
		t.Fatalf("analysis input should not include Codex function call records: %q", input)
	}
	if !strings.Contains(input, "Need cleanup for Codex chat analysis.") {
		t.Fatalf("analysis input missing user request: %q", input)
	}
	if !strings.Contains(input, "Codex analysis should now skip bootstrap instructions.") {
		t.Fatalf("analysis input missing assistant response: %q", input)
	}
}

func TestBuildAnalysisInputWithDiagnosticsReadsVSCodeJSON(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "vscode-chat.json")
	contents := `{"requests":[{"message":{"text":"Find the runtime bug."},"response":[{"kind":"markdownContent","value":"The JSON router was too narrow."}]}]}`
	if err := os.WriteFile(sourcePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}

	input, sourceIDs, messageCount, _, err := buildAnalysisInputWithDiagnostics([]chat.ChatSource{
		{
			Path: sourcePath,
			Tool: chat.SourceTypeVSCodeChatSession,
		},
	})
	if err != nil {
		t.Fatalf("buildAnalysisInputWithDiagnostics returned error: %v", err)
	}
	if messageCount != 2 {
		t.Fatalf("messageCount = %d, want 2", messageCount)
	}
	assertStringSliceEqual(t, sourceIDs, []string{sourcePath})
	if !strings.Contains(input, "Find the runtime bug.") || !strings.Contains(input, "The JSON router was too narrow.") {
		t.Fatalf("analysis input missing vscode content: %q", input)
	}
}

func TestBuildAnalysisInputWithDiagnosticsSanitizesCopilotSession(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "copilot-session.jsonl")
	contents := strings.Join([]string{
		`{"type":"session.start","data":{"id":"s1"}}`,
		`{"type":"user.message","data":{"content":"Need help\nwith JSONL parsing."}}`,
		`{"type":"user.message","data":{"content":"Need   help with JSONL parsing."}}`,
		`{"type":"assistant.message","data":{"toolRequests":[{"id":"tool-1"}]}}`,
		`{"type":"assistant.message","data":{"content":"Handle typed event records."}}`,
		`{"type":"tool.execution_result","data":{"content":"large output"}}`,
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}

	input, _, messageCount, _, err := buildAnalysisInputWithDiagnostics([]chat.ChatSource{
		{
			Path: sourcePath,
			Tool: chat.SourceTypeCopilotSessionJSONL,
		},
	})
	if err != nil {
		t.Fatalf("buildAnalysisInputWithDiagnostics returned error: %v", err)
	}
	if messageCount != 2 {
		t.Fatalf("messageCount = %d, want 2", messageCount)
	}
	if strings.Contains(input, "tool-1") || strings.Contains(input, "large output") || strings.Contains(input, "session.start") {
		t.Fatalf("analysis input should not include Copilot noise: %q", input)
	}
	if !strings.Contains(input, "Need help with JSONL parsing.") || !strings.Contains(input, "Handle typed event records.") {
		t.Fatalf("analysis input missing Copilot signal: %q", input)
	}
}

func TestApplyClaudeProcessingDiagnosticsAddsCounters(t *testing.T) {
	usageStats := map[string]int64{
		"messages_analyzed": 9,
	}

	applyClaudeProcessingDiagnostics(usageStats, claudeProcessingDiagnostics{
		TotalMessagesRead: 10,
		MessagesKept:      4,
		MessagesDropped:   6,
	})

	if got := usageStats["claude_messages_total"]; got != 10 {
		t.Fatalf("claude_messages_total = %d, want 10", got)
	}
	if got := usageStats["claude_messages_kept"]; got != 4 {
		t.Fatalf("claude_messages_kept = %d, want 4", got)
	}
	if got := usageStats["claude_messages_dropped"]; got != 6 {
		t.Fatalf("claude_messages_dropped = %d, want 6", got)
	}
	if _, ok := usageStats["claude_messages_truncated"]; ok {
		t.Fatalf("claude_messages_truncated should not be set when truncation count is zero")
	}
	if got := usageStats["messages_analyzed"]; got != 9 {
		t.Fatalf("messages_analyzed = %d, want 9", got)
	}
}

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

func TestAnalyzerProviderConfigFromConfigUsesAnalyzerSettings(t *testing.T) {
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

	options := analyzerProviderConfigFromConfig(cfg)
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

func TestBuildAnalysisInputWithDiagnosticsIsolatesClaudeSourcesByProjectRoot(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", t.TempDir())

	projectADir := filepath.Join(t.TempDir(), "project-a")
	projectBDir := filepath.Join(t.TempDir(), "project-b")
	claudeAPath := filepath.Join(homeDir, ".claude", "projects", "alpha", "session.jsonl")
	claudeBPath := filepath.Join(homeDir, ".claude", "projects", "beta", "session.jsonl")

	contentsA := strings.Join([]string{
		fmt.Sprintf(`{"cwd":%q}`, filepath.Join(projectADir, "workspace")),
		`{"type":"user","message":{"role":"user","content":"Project A: flaky daemon cycle on isolated root"},"timestamp":"2026-05-08T12:00:01Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"Project A: checking discovery boundaries."},"timestamp":"2026-05-08T12:00:02Z"}`,
	}, "\n")
	contentsB := strings.Join([]string{
		fmt.Sprintf(`{"cwd":%q}`, filepath.Join(projectBDir, "workspace")),
		`{"type":"user","message":{"role":"user","content":"Project B: sanitize Claude payload noise"},"timestamp":"2026-05-08T12:00:01Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"Project B: preserving signal text."},"timestamp":"2026-05-08T12:00:02Z"}`,
	}, "\n")

	for path, content := range map[string]string{
		claudeAPath: contentsA,
		claudeBPath: contentsB,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
	}

	overlapping := time.Date(2026, 5, 8, 12, 30, 0, 0, time.UTC)
	if err := os.Chtimes(claudeAPath, overlapping, overlapping); err != nil {
		t.Fatalf("Chtimes for project A returned error: %v", err)
	}
	if err := os.Chtimes(claudeBPath, overlapping, overlapping); err != nil {
		t.Fatalf("Chtimes for project B returned error: %v", err)
	}

	sourcesA, err := chat.DiscoverChats(projectADir)
	if err != nil {
		t.Fatalf("DiscoverChats for project A returned error: %v", err)
	}
	if len(sourcesA) != 1 || sourcesA[0].Path != claudeAPath {
		t.Fatalf("project A sources = %+v, want only %q", sourcesA, claudeAPath)
	}

	inputA, sourceIDsA, messageCountA, _, err := buildAnalysisInputWithDiagnostics(sourcesA)
	if err != nil {
		t.Fatalf("buildAnalysisInputWithDiagnostics for project A returned error: %v", err)
	}
	if messageCountA != 2 {
		t.Fatalf("project A messageCount = %d, want 2", messageCountA)
	}
	assertStringSliceEqual(t, sourceIDsA, []string{claudeAPath})
	if !strings.Contains(inputA, "Project A: flaky daemon cycle on isolated root") || strings.Contains(inputA, "Project B:") {
		t.Fatalf("project A analysis input should include only project A content: %q", inputA)
	}

	sourcesB, err := chat.DiscoverChats(projectBDir)
	if err != nil {
		t.Fatalf("DiscoverChats for project B returned error: %v", err)
	}
	if len(sourcesB) != 1 || sourcesB[0].Path != claudeBPath {
		t.Fatalf("project B sources = %+v, want only %q", sourcesB, claudeBPath)
	}

	inputB, sourceIDsB, messageCountB, _, err := buildAnalysisInputWithDiagnostics(sourcesB)
	if err != nil {
		t.Fatalf("buildAnalysisInputWithDiagnostics for project B returned error: %v", err)
	}
	if messageCountB != 2 {
		t.Fatalf("project B messageCount = %d, want 2", messageCountB)
	}
	assertStringSliceEqual(t, sourceIDsB, []string{claudeBPath})
	if !strings.Contains(inputB, "Project B: sanitize Claude payload noise") || strings.Contains(inputB, "Project A:") {
		t.Fatalf("project B analysis input should include only project B content: %q", inputB)
	}
}

func TestAlternatingProjectCyclesKeepAnalyzedIDsIsolated(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)

	overlapping := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	projectA1 := filepath.Join(t.TempDir(), "project-a", "claude-a-1.jsonl")
	projectA2 := filepath.Join(t.TempDir(), "project-a", "claude-a-2.jsonl")
	projectB1 := filepath.Join(t.TempDir(), "project-b", "claude-b-1.jsonl")
	projectB2 := filepath.Join(t.TempDir(), "project-b", "claude-b-2.jsonl")

	cycles := []struct {
		projectName       string
		discovered        []chat.ChatSource
		expectedFiltered  []string
		unexpectedInScope string
	}{
		{
			projectName: "project-a",
			discovered: []chat.ChatSource{
				{Path: projectA1, Tool: chat.SourceTypeClaudeCodeSession, ModifiedTime: overlapping},
			},
			expectedFiltered:  []string{projectA1},
			unexpectedInScope: "project-b",
		},
		{
			projectName: "project-b",
			discovered: []chat.ChatSource{
				{Path: projectB1, Tool: chat.SourceTypeClaudeCodeSession, ModifiedTime: overlapping},
			},
			expectedFiltered:  []string{projectB1},
			unexpectedInScope: "project-a",
		},
		{
			projectName: "project-a",
			discovered: []chat.ChatSource{
				{Path: projectA1, Tool: chat.SourceTypeClaudeCodeSession, ModifiedTime: overlapping},
				{Path: projectA2, Tool: chat.SourceTypeClaudeCodeSession, ModifiedTime: overlapping},
			},
			expectedFiltered:  []string{projectA2},
			unexpectedInScope: "project-b",
		},
		{
			projectName: "project-b",
			discovered: []chat.ChatSource{
				{Path: projectB1, Tool: chat.SourceTypeClaudeCodeSession, ModifiedTime: overlapping},
				{Path: projectB2, Tool: chat.SourceTypeClaudeCodeSession, ModifiedTime: overlapping},
			},
			expectedFiltered:  []string{projectB2},
			unexpectedInScope: "project-a",
		},
	}

	baseRun := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	for cycleIndex, cycle := range cycles {
		currentState, err := state.LoadState(cycle.projectName)
		if err != nil {
			t.Fatalf("LoadState for %q returned error: %v", cycle.projectName, err)
		}

		filtered := filterSourcesToAnalyze(cycle.discovered, currentState)
		filteredIDs := sourcePaths(filtered)
		assertStringSliceEqual(t, filteredIDs, cycle.expectedFiltered)
		for _, id := range filteredIDs {
			if strings.Contains(id, cycle.unexpectedInScope) {
				t.Fatalf("cycle %d for %q leaked source from other project: %q", cycleIndex, cycle.projectName, id)
			}
		}

		for _, id := range filteredIDs {
			if _, exists := currentState.ChatHashes[id]; !exists {
				currentState.ChatHashes[id] = ""
			}
		}
		currentState.LastRunUTC = baseRun.Add(time.Duration(cycleIndex+1) * time.Minute)
		if err := state.SaveState(cycle.projectName, currentState); err != nil {
			t.Fatalf("SaveState for %q returned error: %v", cycle.projectName, err)
		}
	}

	finalA, err := state.LoadState("project-a")
	if err != nil {
		t.Fatalf("LoadState final project-a returned error: %v", err)
	}
	finalB, err := state.LoadState("project-b")
	if err != nil {
		t.Fatalf("LoadState final project-b returned error: %v", err)
	}

	assertStringSliceEqual(t, sortedKeys(finalA.ChatHashes), []string{projectA1, projectA2})
	assertStringSliceEqual(t, sortedKeys(finalB.ChatHashes), []string{projectB1, projectB2})
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
