package openclaudecli

import (
	"context"
	"reflect"
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
	// --strict-mcp-config is a boolean flag; passing "{}" causes it to be
	// consumed as the positional prompt argument. Guard against regression.
	if strings.Contains(got, "--strict-mcp-config {}") {
		t.Errorf("default command must not pass a value to boolean flag --strict-mcp-config\ngot: %s", got)
	}
}

func TestCustomCommandOverridesDefaults(t *testing.T) {
	custom := []string{"my-openclaude", "--safe"}
	p, err := New(cliharness.Options{Command: custom})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).p.Command
	if !reflect.DeepEqual(cmd, custom) {
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

func TestProviderID(t *testing.T) {
	p, err := New(cliharness.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	if p.ID() != "openclaude-cli" {
		t.Errorf("expected %q, got %q", "openclaude-cli", p.ID())
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
	custom := []string{"custom-openclaude", "--flag"}
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
	if cmd[0] != "custom-openclaude" {
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
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello from openclaude"}]}}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello from openclaude" {
		t.Errorf("expected %q, got %q", "Hello from openclaude", text)
	}
}

func TestReadStreamJSON_ResultSuccessEvent(t *testing.T) {
	line := `{"type":"result","subtype":"success","result":"Final answer"}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Final answer" {
		t.Errorf("expected %q, got %q", "Final answer", text)
	}
}

func TestReadStreamJSON_ResultPreferredOverAssistant(t *testing.T) {
	events := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Assistant text"}]}}`,
		`{"type":"result","subtype":"success","result":"Result text"}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Result text" {
		t.Errorf("expected %q, got %q", "Result text", text)
	}
}

func TestReadStreamJSON_ResultError(t *testing.T) {
	line := `{"type":"result","subtype":"error_during_execution","errors":["something crashed"]}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for error result")
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
	if !strings.Contains(err.Error(), "something crashed") {
		t.Errorf("error missing message: %v", err)
	}
}

func TestReadStreamJSON_ResultError_DefaultMessage(t *testing.T) {
	line := `{"type":"result","subtype":"error_max_turns"}`
	input := strings.NewReader(line + "\n")
	_, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for error result with no errors array")
	}
	if !strings.Contains(err.Error(), "unknown error (error_max_turns)") {
		t.Errorf("expected default error message, got: %v", err)
	}
}

func TestReadStreamJSON_AssistantAPIError(t *testing.T) {
	line := `{"type":"assistant","error":"rate_limit_exceeded","message":{"content":[]}}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for assistant API error")
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
	if !strings.Contains(err.Error(), "rate_limit_exceeded") {
		t.Errorf("error missing api error: %v", err)
	}
}

func TestReadStreamJSON_MultipleAssistantMessages(t *testing.T) {
	events := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Part one "}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Part two"}]}}`,
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

func TestReadStreamJSON_MultipleErrorsInArray(t *testing.T) {
	line := `{"type":"result","subtype":"error_during_execution","errors":["err1","err2"]}`
	input := strings.NewReader(line + "\n")
	_, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "err1; err2") {
		t.Errorf("expected semicolon-joined errors, got: %v", err)
	}
}

func TestReadStreamJSON_ErrorResultBeatsAssistant(t *testing.T) {
	events := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Assistant text"}]}}`,
		`{"type":"result","subtype":"error_during_execution","errors":["fatal"]}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	_, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for error result overriding assistant text")
	}
	if !strings.Contains(err.Error(), "fatal") {
		t.Errorf("expected error message, got: %v", err)
	}
}
