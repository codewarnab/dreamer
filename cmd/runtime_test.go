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

func TestReadMessagesFromSourceRejectsUnsupportedExtension(t *testing.T) {
	_, err := readMessagesFromSource(chat.ChatSource{
		Path: "chat.json",
		Tool: chat.SourceTypeVSCodeChatSession,
	})
	if err == nil {
		t.Fatalf("readMessagesFromSource expected unsupported extension error")
	}
	if !strings.Contains(err.Error(), "supported: .jsonl") {
		t.Fatalf("error = %q, want supported extension message", err)
	}
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

	options := analyzerClientOptionsFromConfig(cfg)
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
