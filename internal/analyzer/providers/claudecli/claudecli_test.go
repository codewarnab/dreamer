package claudecli

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

func TestDefaultCommandIncludesUnrestrictedFlags(t *testing.T) {
	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).p.Command
	got := strings.Join(cmd, " ")

	if !sandbox.Available() {
		if strings.Contains(got, "--dangerously-skip-permissions") {
			t.Errorf("default command must not include unrestricted flags without native sandbox\ngot: %s", got)
		}
		if !strings.Contains(got, "--permission-mode plan") {
			t.Errorf("default command missing policy fallback\ngot: %s", got)
		}
		return
	}

	requiredFlags := []string{
		"--dangerously-skip-permissions",
		"--bare",
	}
	for _, flag := range requiredFlags {
		if !strings.Contains(got, flag) {
			t.Errorf("default command missing %q\ngot: %s", flag, got)
		}
	}
	// Policy-only flags must NOT be present (sandbox replaces them).
	forbiddenFlags := []string{
		"--permission-mode",
		"--tools Read,Grep,Glob",
	}
	for _, flag := range forbiddenFlags {
		if strings.Contains(got, flag) {
			t.Errorf("default command must not include %q (sandbox replaces policy flags)\ngot: %s", flag, got)
		}
	}
	// --strict-mcp-config is a boolean flag on Claude Code; passing it with
	// "{}" causes the value to be consumed as the positional prompt argument.
	if strings.Contains(got, "--strict-mcp-config {}") {
		t.Errorf("default command must not pass a value to boolean flag --strict-mcp-config\ngot: %s", got)
	}
}

func TestCustomCommandOverridesDefaults(t *testing.T) {
	custom := []string{"my-claude", "--safe"}
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
	if strings.Contains(got, "--dangerously-skip-permissions") {
		t.Fatalf("sandbox=false should use policy flags, got: %s", got)
	}
	if !strings.Contains(got, "--permission-mode plan") {
		t.Fatalf("sandbox=false command missing policy mode, got: %s", got)
	}
}

func TestWritableDirsHonorClaudeConfigDir(t *testing.T) {
	claudeConfigDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)

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
		if dir == claudeConfigDir {
			return
		}
	}
	t.Fatalf("CLAUDE_CONFIG_DIR %q not found in writable dirs: %v", claudeConfigDir, writable)
}

func TestProviderID(t *testing.T) {
	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	if p.ID() != "claude-cli" {
		t.Errorf("expected %q, got %q", "claude-cli", p.ID())
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

func TestStart_NonExistentBinary(t *testing.T) {
	p, err := New(cliharness.Options{Command: []string{"nonexistent-claude-binary-12345"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	err = p.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for non-existent binary")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("error missing PATH hint: %v", err)
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
	custom := []string{"custom-claude", "--flag"}
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
	if cmd[0] != "custom-claude" {
		t.Errorf("expected custom command preserved, got %v", cmd)
	}
}

func TestNewSession_ModelFlagFromSessionConfig(t *testing.T) {
	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Model:            "claude-sonnet-4",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	cmd := strings.Join(sess.(*cliharness.Session).Command(), " ")
	if !strings.Contains(cmd, "--model claude-sonnet-4") {
		t.Errorf("expected --model flag, got: %s", cmd)
	}
}

func TestNewSession_ModelFromDefaultModel(t *testing.T) {
	p, err := New(cliharness.Options{DefaultModel: "claude-haiku"})
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
	cmd := strings.Join(sess.(*cliharness.Session).Command(), " ")
	if !strings.Contains(cmd, "--model claude-haiku") {
		t.Errorf("expected --model flag from DefaultModel, got: %s", cmd)
	}
}

func TestNewSession_InvalidSandboxMode(t *testing.T) {
	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	_, err = p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Sandbox:          "invalid-mode",
	})
	if err == nil {
		t.Fatal("expected error for invalid sandbox mode")
	}
	if !strings.Contains(err.Error(), "claude-cli:") {
		t.Errorf("expected claude-cli prefix: %v", err)
	}
}

func TestNewSession_ModelFlagSessionOverridesDefault(t *testing.T) {
	p, err := New(cliharness.Options{DefaultModel: "default-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Model:            "session-model",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	cmd := strings.Join(sess.(*cliharness.Session).Command(), " ")
	if strings.Contains(cmd, "default-model") {
		t.Errorf("session model should override default, got: %s", cmd)
	}
	if !strings.Contains(cmd, "session-model") {
		t.Errorf("expected session model in command, got: %s", cmd)
	}
}

func TestNewSession_NoModelFlagWhenEmpty(t *testing.T) {
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
	cmd := strings.Join(sess.(*cliharness.Session).Command(), " ")
	if strings.Contains(cmd, "--model") {
		t.Errorf("should not have --model flag when no model specified, got: %s", cmd)
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

func TestReadStreamJSON_SingleAssistantMessage(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello world" {
		t.Errorf("expected %q, got %q", "Hello world", text)
	}
}

func TestReadStreamJSON_MultipleEvents(t *testing.T) {
	events := []string{
		`{"type":"system","message":{"content":[]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"First response"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Final response"}]}}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// readStreamJSON resets on each assistant event; last one wins.
	if text != "Final response" {
		t.Errorf("expected %q, got %q", "Final response", text)
	}
}

func TestReadStreamJSON_MixOfErrorsAndValidLines(t *testing.T) {
	events := []string{
		`{"bad json`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Valid answer"}]}}`,
		`not json at all`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Valid answer" {
		t.Errorf("expected %q, got %q", "Valid answer", text)
	}
}

func TestReadStreamJSON_MultipleContentBlocks(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Part one "},{"type":"text","text":"Part two"}]}}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Part one Part two" {
		t.Errorf("expected %q, got %q", "Part one Part two", text)
	}
}

func TestReadStreamJSON_SkipsNonTextContent(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","text":"ignored"},{"type":"text","text":"Real text"}]}}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Real text" {
		t.Errorf("expected %q, got %q", "Real text", text)
	}
}

func TestReadStreamJSON_TrimsLeadingAndTrailingWhitespace(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"  spaced out  "}]}}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "spaced out" {
		t.Errorf("expected %q, got %q", "spaced out", text)
	}
}

func TestReadStreamJSON_BlankLinesIgnored(t *testing.T) {
	input := strings.NewReader("\n\n\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestResolveConfigDir_EnvMapOverride(t *testing.T) {
	got, err := cliharness.ResolveConfigDir(map[string]string{"CLAUDE_CONFIG_DIR": "/custom/path"}, "CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/custom/path" {
		t.Errorf("expected %q, got %q", "/custom/path", got)
	}
}

func TestResolveConfigDir_OsGetenvFallback(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/env/fallback")
	got, err := cliharness.ResolveConfigDir(nil, "CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/env/fallback" {
		t.Errorf("expected %q, got %q", "/env/fallback", got)
	}
}

func TestResolveConfigDir_HomeFallback(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	got, err := cliharness.ResolveConfigDir(nil, "CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".claude")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestResolveConfigDir_EnvMapTakesPrecedenceOverOsGetenv(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/from/env")
	got, err := cliharness.ResolveConfigDir(map[string]string{"CLAUDE_CONFIG_DIR": "/from/map"}, "CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/from/map" {
		t.Errorf("expected %q, got %q", "/from/map", got)
	}
}

func TestResolveConfigDir_WhitespaceIgnored(t *testing.T) {
	got, err := cliharness.ResolveConfigDir(map[string]string{"CLAUDE_CONFIG_DIR": "  "}, "CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Whitespace-only env map value falls through to os.Getenv or home.
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".claude")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

// ---------------------------------------------------------------------------
// readStreamJSON: additional edge cases
// ---------------------------------------------------------------------------

func TestReadStreamJSON_ValidJSONButNoAssistantType(t *testing.T) {
	events := []string{
		`{"type":"system","message":{"content":[]}}`,
		`{"type":"result","message":{"content":[]}}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text when no assistant events, got %q", text)
	}
}

func TestReadStreamJSON_AssistantResetsPreviousContent(t *testing.T) {
	// Second assistant event with empty content array should reset the first's text.
	events := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"First"}]}}`,
		`{"type":"assistant","message":{"content":[]}}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty after reset, got %q", text)
	}
}

func TestReadStreamJSON_AssistantWithOnlyNonTextContent(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu1","name":"Bash"}]}}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty for tool_use-only content, got %q", text)
	}
}
