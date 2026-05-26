package codexcli

import (
	"context"
	"strings"
	"testing"

	"dreamer/internal/analyzer"
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
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).command
	got := strings.Join(cmd, " ")

	if !sandbox.Available() {
		if strings.Contains(got, "--yolo") {
			t.Errorf("default command must not include --yolo without native sandbox\ngot: %s", got)
		}
		if !strings.Contains(got, "--sandbox read-only") {
			t.Errorf("default command missing read-only sandbox fallback\ngot: %s", got)
		}
		return
	}

	if !strings.Contains(got, "--yolo") {
		t.Errorf("default command missing --yolo\ngot: %s", got)
	}
	// --sandbox read-only must NOT be present (sandbox replaces it).
	if strings.Contains(got, "--sandbox") {
		t.Errorf("default command must not include --sandbox (OS sandbox replaces it)\ngot: %s", got)
	}
	// --ask-for-approval is not a valid `codex exec` flag.
	if strings.Contains(got, "--ask-for-approval") {
		t.Errorf("default command must not include --ask-for-approval (rejected by codex exec)\ngot: %s", got)
	}
}

func TestCustomCommandOverridesDefaults(t *testing.T) {
	custom := []string{"my-codex", "exec"}
	p, err := New(Options{Command: custom})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).command
	if len(cmd) != len(custom) {
		t.Fatalf("custom command not applied: got %v, want %v", cmd, custom)
	}
}

func TestSandboxOffDefaultCommandUsesPolicyFlags(t *testing.T) {
	p, err := New(Options{})
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
	got := strings.Join(sess.(*session).command, " ")
	if strings.Contains(got, "--yolo") {
		t.Fatalf("sandbox=false should not use --yolo, got: %s", got)
	}
	if !strings.Contains(got, "--sandbox read-only") {
		t.Fatalf("sandbox=false command missing read-only sandbox flag, got: %s", got)
	}
}

func TestProviderID(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	if p.ID() != "codex-cli" {
		t.Errorf("expected %q, got %q", "codex-cli", p.ID())
	}
}

func TestProviderClose(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestNewSession_EmptyWorkingDirectory(t *testing.T) {
	p, err := New(Options{})
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
	custom := []string{"custom-codex", "exec", "--json"}
	p, err := New(Options{Command: custom})
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
	cmd := sess.(*session).command
	if cmd[0] != "custom-codex" {
		t.Errorf("expected custom command preserved, got %v", cmd)
	}
}

func TestNewSession_ModelFlagFromSessionConfig(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Model:            "gpt-5",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	cmd := strings.Join(sess.(*session).command, " ")
	if !strings.Contains(cmd, "--model gpt-5") {
		t.Errorf("expected --model flag, got: %s", cmd)
	}
}

func TestNewSession_ModelFromDefaultModel(t *testing.T) {
	p, err := New(Options{DefaultModel: "o3"})
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
	cmd := strings.Join(sess.(*session).command, " ")
	if !strings.Contains(cmd, "--model o3") {
		t.Errorf("expected --model flag from DefaultModel, got: %s", cmd)
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

func TestReadStreamJSON_AgentMessage(t *testing.T) {
	line := `{"type":"item.completed","item":{"type":"agent_message","text":"Hello from codex"}}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello from codex" {
		t.Errorf("expected %q, got %q", "Hello from codex", text)
	}
}

func TestReadStreamJSON_ErrorEvent(t *testing.T) {
	line := `{"type":"error","message":"rate limit exceeded"}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for error event")
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("error missing message: %v", err)
	}
}

func TestReadStreamJSON_TurnFailedEvent(t *testing.T) {
	line := `{"type":"turn.failed","error":{"message":"context window exceeded"}}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for turn.failed event")
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
	if !strings.Contains(err.Error(), "context window exceeded") {
		t.Errorf("error missing message: %v", err)
	}
}

func TestReadStreamJSON_TurnFailedEvent_FallbackMessage(t *testing.T) {
	line := `{"type":"turn.failed","message":"fallback error message"}`
	input := strings.NewReader(line + "\n")
	_, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for turn.failed event with message field")
	}
	if !strings.Contains(err.Error(), "fallback error message") {
		t.Errorf("error missing fallback message: %v", err)
	}
}

func TestReadStreamJSON_MultipleAgentMessages(t *testing.T) {
	events := []string{
		`{"type":"item.completed","item":{"type":"agent_message","text":"First part"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Second part"}}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "First part\nSecond part" {
		t.Errorf("expected %q, got %q", "First part\nSecond part", text)
	}
}

func TestReadStreamJSON_IgnoresNonAgentMessageItems(t *testing.T) {
	events := []string{
		`{"type":"item.completed","item":{"type":"command_execution","text":"ignored"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Real answer"}}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Real answer" {
		t.Errorf("expected %q, got %q", "Real answer", text)
	}
}

func TestReadStreamJSON_IgnoresOtherEventTypes(t *testing.T) {
	events := []string{
		`{"type":"thread.started"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"The answer"}}`,
		`{"type":"turn.completed"}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "The answer" {
		t.Errorf("expected %q, got %q", "The answer", text)
	}
}

func TestReadStreamJSON_ErrorOverSchemaChange(t *testing.T) {
	// When there's both an error event and parse errors, error wins.
	events := []string{
		`{"bad"}`,
		`{"type":"error","message":"something went wrong"}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	_, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("expected stream error message, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// readStreamJSON: additional edge cases
// ---------------------------------------------------------------------------

func TestReadStreamJSON_AgentMessageEmptyText(t *testing.T) {
	line := `{"type":"item.completed","item":{"type":"agent_message","text":""}}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty text is skipped (the condition requires text != "").
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestReadStreamJSON_ErrorWithEmptyMessage(t *testing.T) {
	// An error event with empty message should not set streamErr.
	line := `{"type":"error","message":""}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestReadStreamJSON_TurnFailedWithEmptyFields(t *testing.T) {
	// turn.failed with neither error.message nor message → no streamErr.
	line := `{"type":"turn.failed"}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestReadStreamJSON_ValidNoAgentMessageNoError(t *testing.T) {
	// All valid JSON, no agent_message items, no errors → empty, no error.
	events := []string{
		`{"type":"thread.started"}`,
		`{"type":"turn.started"}`,
		`{"type":"turn.completed"}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestReadStreamJSON_ItemCompletedWithMissingItem(t *testing.T) {
	// item.completed but with no item field → empty item type, skipped.
	line := `{"type":"item.completed"}`
	input := strings.NewReader(line + "\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestReadStreamJSON_ErrorThenAgentMessage(t *testing.T) {
	// Error event sets streamErr; subsequent agent message is collected but
	// streamErr takes priority so overall result is an error.
	events := []string{
		`{"type":"error","message":"rate limited"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Some answer"}}`,
	}
	input := strings.NewReader(strings.Join(events, "\n") + "\n")
	_, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error from error event")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate limited message, got: %v", err)
	}
}

func TestReadStreamJSON_BlankLinesOnly(t *testing.T) {
	input := strings.NewReader("\n\n\n")
	text, err := readStreamJSON(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}
