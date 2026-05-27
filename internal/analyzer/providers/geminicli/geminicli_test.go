package geminicli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/cliharness"
	"dreamer/internal/sandbox"
)

func TestReadStreamJSON_SchemaChange(t *testing.T) {
	input := strings.NewReader("{\"bad\"}\nnope\n")
	text, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for all lines failing to parse")
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
	if !strings.Contains(err.Error(), "all 2 output lines failed to parse") {
		t.Errorf("error missing line count: %v", err)
	}
	if !strings.Contains(err.Error(), "provider schema change?") {
		t.Errorf("error missing schema change hint: %v", err)
	}
}

func TestDefaultCommandIncludesYoloFlag(t *testing.T) {
	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).p.Command
	got := strings.Join(cmd, " ")

	if !sandbox.Available() {
		if strings.Contains(got, "--yolo") {
			t.Errorf("default command must not include --yolo without native sandbox\ngot: %s", got)
		}
		if !strings.Contains(got, "--approval-mode=plan") {
			t.Errorf("default command missing approval-mode fallback\ngot: %s", got)
		}
		return
	}

	if !strings.Contains(got, "--yolo") {
		t.Errorf("default command missing --yolo\ngot: %s", got)
	}
	// --approval-mode=plan must NOT be present (sandbox replaces it).
	if strings.Contains(got, "--approval-mode") {
		t.Errorf("default command must not include --approval-mode (sandbox replaces policy flags)\ngot: %s", got)
	}
}

func TestCustomCommandOverridesDefaults(t *testing.T) {
	custom := []string{"my-gemini", "-p"}
	p, err := New(cliharness.Options{Command: custom})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).p.Command
	if len(cmd) != len(custom) {
		t.Fatalf("custom command not applied: got %v, want %v", cmd, custom)
	}
}

func TestSandboxOffDefaultCommandUsesPolicyFlags(t *testing.T) {
	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Sandbox:          "false",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	got := strings.Join(sess.(*cliharness.Session).Command(), " ")
	if strings.Contains(got, "--yolo") {
		t.Fatalf("sandbox=false should not use --yolo, got: %s", got)
	}
	if !strings.Contains(got, "--approval-mode=plan") {
		t.Fatalf("sandbox=false command missing approval-mode fallback, got: %s", got)
	}
}

func TestWritableDirsHonorGeminiHome(t *testing.T) {
	geminiHomeDir := t.TempDir()
	t.Setenv("GEMINI_HOME", geminiHomeDir)

	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	writable := sess.(*cliharness.Session).SandboxConfig().WritableDirs
	for _, dir := range writable {
		if dir == geminiHomeDir {
			return
		}
	}
	t.Fatalf("GEMINI_HOME %q not found in writable dirs: %v", geminiHomeDir, writable)
}

func TestProviderID(t *testing.T) {
	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	if p.ID() != "gemini-cli" {
		t.Errorf("expected %q, got %q", "gemini-cli", p.ID())
	}
}

func TestProviderClose(t *testing.T) {
	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestNewSession_EmptyWorkingDirectory(t *testing.T) {
	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	_, err = p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: ""})
	if err == nil {
		t.Fatal("expected error for empty working directory")
	}
	if !strings.Contains(err.Error(), "WorkingDirectory is required") {
		t.Errorf("error missing required message: %v", err)
	}
}

func TestNewSession_CustomCommandPreserved(t *testing.T) {
	custom := []string{"custom-gemini", "-p", "--yolo"}
	p, err := New(cliharness.Options{Command: custom})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	cmd := sess.(*cliharness.Session).Command()
	if cmd[0] != "custom-gemini" {
		t.Errorf("expected custom command preserved, got %v", cmd)
	}
}

func TestReadStreamJSON_EmptyInput(t *testing.T) {
	input := strings.NewReader("")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestReadStreamJSON_AssistantMessage(t *testing.T) {
	line := `{"type":"message","role":"assistant","content":"Hello from gemini"}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello from gemini" {
		t.Errorf("expected %q, got %q", "Hello from gemini", text)
	}
}

func TestReadStreamJSON_ResultEvent(t *testing.T) {
	line := `{"type":"result","response":"Final answer from gemini"}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Final answer from gemini" {
		t.Errorf("expected %q, got %q", "Final answer from gemini", text)
	}
}

func TestReadStreamJSON_ResultPreferredOverMessage(t *testing.T) {
	events := []string{
		`{"type":"message","role":"assistant","content":"Partial"}`,
		`{"type":"result","response":"Complete result"}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Complete result" {
		t.Errorf("expected %q, got %q", "Complete result", text)
	}
}

func TestReadStreamJSON_ErrorEvent(t *testing.T) {
	line := `{"type":"error","message":"API key invalid"}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for error event")
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
	if !strings.Contains(err.Error(), "API key invalid") {
		t.Errorf("error missing message: %v", err)
	}
}

func TestReadStreamJSON_ErrorEvent_DefaultMessage(t *testing.T) {
	line := `{"type":"error"}`
	input := strings.NewReader(line + "\n")
	_, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for error event")
	}
	if !strings.Contains(err.Error(), "unknown error") {
		t.Errorf("expected default error message, got: %v", err)
	}
}

func TestReadStreamJSON_NonAssistantRoleIgnored(t *testing.T) {
	events := []string{
		`{"type":"message","role":"user","content":"Question"}`,
		`{"type":"message","role":"assistant","content":"Answer"}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Answer" {
		t.Errorf("expected %q, got %q", "Answer", text)
	}
}

func TestReadStreamJSON_EmptyRoleTreatedAsAssistant(t *testing.T) {
	line := `{"type":"message","content":"No role field"}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "No role field" {
		t.Errorf("expected %q, got %q", "No role field", text)
	}
}

func TestReadStreamJSON_MultipleAssistantMessages(t *testing.T) {
	events := []string{
		`{"type":"message","role":"assistant","content":"Part one "}`,
		`{"type":"message","role":"assistant","content":"Part two"}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Part one Part two" {
		t.Errorf("expected %q, got %q", "Part one Part two", text)
	}
}

func TestResolveConfigDir_EnvMapOverride(t *testing.T) {
	got, err := cliharness.ResolveConfigDir(map[string]string{"GEMINI_HOME": "/custom/gemini"}, "GEMINI_HOME", ".gemini")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/custom/gemini" {
		t.Errorf("expected %q, got %q", "/custom/gemini", got)
	}
}

func TestResolveConfigDir_OsGetenvFallback(t *testing.T) {
	t.Setenv("GEMINI_HOME", "/env/gemini")
	got, err := cliharness.ResolveConfigDir(nil, "GEMINI_HOME", ".gemini")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/env/gemini" {
		t.Errorf("expected %q, got %q", "/env/gemini", got)
	}
}

func TestResolveConfigDir_HomeFallback(t *testing.T) {
	t.Setenv("GEMINI_HOME", "")
	got, err := cliharness.ResolveConfigDir(nil, "GEMINI_HOME", ".gemini")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".gemini")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestResolveConfigDir_EnvMapTakesPrecedenceOverOsGetenv(t *testing.T) {
	t.Setenv("GEMINI_HOME", "/from/env")
	got, err := cliharness.ResolveConfigDir(map[string]string{"GEMINI_HOME": "/from/map"}, "GEMINI_HOME", ".gemini")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/from/map" {
		t.Errorf("expected %q, got %q", "/from/map", got)
	}
}
