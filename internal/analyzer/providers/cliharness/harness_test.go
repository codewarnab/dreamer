package cliharness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/errs"
	"dreamer/internal/sandbox"
)

// testSpec returns a minimal Spec for unit tests.
func testSpec() *Spec {
	return &Spec{
		ID:             "test-cli",
		ErrPrefix:      "test-cli",
		DefaultCommand: func(bool) []string { return []string{"echo"} },
		StartErr:       func(binary string, err error) error { return fmt.Errorf("start: %w", err) },
		CmdStartErr:    func(err error) error { return fmt.Errorf("cmd-start: %w", err) },
		ConfigDir: func(env map[string]string) (string, error) {
			return filepath.Join(os.TempDir(), "test-cli-config"), nil
		},
		ParseErrFirst: false,
		ReadStreamJSON: func(r io.Reader) (string, error) {
			data, err := io.ReadAll(r)
			return string(data), err
		},
		ResolveModel: ResolveModel2Tier,
	}
}

func TestNewProvider_DefaultCommand(t *testing.T) {
	spec := testSpec()
	p := NewProvider(Options{}, spec)
	if !p.UsesDefaultCommand {
		t.Error("expected UsesDefaultCommand=true when no custom command")
	}
	if len(p.Command) == 0 {
		t.Error("expected non-empty default command")
	}
}

func TestNewProvider_CustomCommand(t *testing.T) {
	spec := testSpec()
	custom := []string{"my-cli", "--flag"}
	p := NewProvider(Options{Command: custom}, spec)
	if p.UsesDefaultCommand {
		t.Error("expected UsesDefaultCommand=false with custom command")
	}
	if p.Command[0] != "my-cli" {
		t.Errorf("expected custom command, got %v", p.Command)
	}
}

func TestNewProvider_DoesNotMutateInput(t *testing.T) {
	spec := testSpec()
	orig := []string{"my-cli"}
	p := NewProvider(Options{Command: orig}, spec)
	p.Command[0] = "mutated"
	if orig[0] == "mutated" {
		t.Error("NewProvider should not mutate the input command slice")
	}
}

func TestCommandForMode_UsesDefaultWhenDefaultCommand(t *testing.T) {
	spec := testSpec()
	spec.DefaultCommand = func(native bool) []string {
		if native {
			return []string{"cli", "--native"}
		}
		return []string{"cli", "--no-native"}
	}
	p := NewProvider(Options{}, spec)

	got := CommandForMode(p, true)
	if got[1] != "--native" {
		t.Errorf("expected --native for sandbox=true, got %v", got)
	}

	got = CommandForMode(p, false)
	if got[1] != "--no-native" {
		t.Errorf("expected --no-native for sandbox=false, got %v", got)
	}
}

func TestCommandForMode_PreservesCustomCommand(t *testing.T) {
	spec := testSpec()
	p := NewProvider(Options{Command: []string{"custom", "--x"}}, spec)

	got := CommandForMode(p, true)
	if got[0] != "custom" {
		t.Errorf("expected custom command preserved, got %v", got)
	}
}

func TestNewSession_EmptyWorkingDirectory(t *testing.T) {
	spec := testSpec()
	p := NewProvider(Options{}, spec)
	_, err := NewSession(p, analyzer.SessionConfig{WorkingDirectory: ""})
	if err == nil {
		t.Fatal("expected error for empty working directory")
	}
	if !strings.Contains(err.Error(), "WorkingDirectory is required") {
		t.Errorf("error missing required message: %v", err)
	}
}

func TestNewSession_SandboxConfigPropagation(t *testing.T) {
	spec := testSpec()
	p := NewProvider(Options{
		SandboxProjectWrite: true,
		SandboxNetwork:      "open",
		SandboxSeccomp:      "off",
		SandboxResources: sandbox.ResourceLimits{
			MemoryMB:  512,
			Processes: 32,
			FDs:       128,
		},
	}, spec)

	wd := t.TempDir()
	sess, err := NewSession(p, analyzer.SessionConfig{
		WorkingDirectory: wd,
		Sandbox:          "false",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	sc := sess.SandboxConfig()
	if sc.ProjectDir != wd {
		t.Errorf("ProjectDir = %q, want %q", sc.ProjectDir, wd)
	}
	if !sc.ProjectWrite {
		t.Error("expected ProjectWrite=true")
	}
	if sc.Network != "open" {
		t.Errorf("Network = %q, want %q", sc.Network, "open")
	}
	if sc.Seccomp != "off" {
		t.Errorf("Seccomp = %q, want %q", sc.Seccomp, "off")
	}
	if sc.Resources.MemoryMB != 512 {
		t.Errorf("MemoryMB = %d, want 512", sc.Resources.MemoryMB)
	}
	if sc.Resources.Processes != 32 {
		t.Errorf("Processes = %d, want 32", sc.Resources.Processes)
	}
	if sc.Resources.FDs != 128 {
		t.Errorf("FDs = %d, want 128", sc.Resources.FDs)
	}
}

func TestNewSession_WritableDirsIncludesTempAndConfig(t *testing.T) {
	spec := testSpec()
	configDir := "/custom/config"
	spec.ConfigDir = func(env map[string]string) (string, error) {
		return configDir, nil
	}
	p := NewProvider(Options{}, spec)

	sess, err := NewSession(p, analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Sandbox:          "false",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	writable := sess.SandboxConfig().WritableDirs
	foundTemp := false
	foundConfig := false
	for _, d := range writable {
		if d == os.TempDir() {
			foundTemp = true
		}
		if d == configDir {
			foundConfig = true
		}
	}
	if !foundTemp {
		t.Errorf("WritableDirs missing temp dir: %v", writable)
	}
	if !foundConfig {
		t.Errorf("WritableDirs missing config dir %q: %v", configDir, writable)
	}
}

func TestNewSession_InvalidSandboxMode(t *testing.T) {
	spec := testSpec()
	p := NewProvider(Options{}, spec)
	_, err := NewSession(p, analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Sandbox:          "bogus",
	})
	if err == nil {
		t.Fatal("expected error for invalid sandbox mode")
	}
}

func TestNewSession_WorkingDirFlagAppended(t *testing.T) {
	spec := testSpec()
	spec.WorkingDirFlag = "--cd"
	p := NewProvider(Options{}, spec)

	wd := t.TempDir()
	sess, err := NewSession(p, analyzer.SessionConfig{
		WorkingDirectory: wd,
		Sandbox:          "false",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	cmd := sess.Command()
	found := false
	for i, arg := range cmd {
		if arg == "--cd" && i+1 < len(cmd) && cmd[i+1] == wd {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --cd %s in command %v", wd, cmd)
	}
}

func TestResolveModel2Tier_SessionModelFirst(t *testing.T) {
	sc := analyzer.SessionConfig{Model: "session-model"}
	got := ResolveModel2Tier(sc, "provider-model", "default-model")
	if got != "session-model" {
		t.Errorf("expected session-model, got %q", got)
	}
}

func TestResolveModel2Tier_DefaultModelFallback(t *testing.T) {
	sc := analyzer.SessionConfig{}
	got := ResolveModel2Tier(sc, "provider-model", "default-model")
	if got != "default-model" {
		t.Errorf("expected default-model, got %q", got)
	}
}

func TestResolveModel3Tier_SessionFirst(t *testing.T) {
	sc := analyzer.SessionConfig{Model: "session"}
	got := ResolveModel3Tier(sc, "provider", "default")
	if got != "session" {
		t.Errorf("expected session, got %q", got)
	}
}

func TestResolveModel3Tier_ProviderSecond(t *testing.T) {
	sc := analyzer.SessionConfig{}
	got := ResolveModel3Tier(sc, "provider", "default")
	if got != "provider" {
		t.Errorf("expected provider, got %q", got)
	}
}

func TestResolveModel3Tier_DefaultThird(t *testing.T) {
	sc := analyzer.SessionConfig{}
	got := ResolveModel3Tier(sc, "", "default")
	if got != "default" {
		t.Errorf("expected default, got %q", got)
	}
}

// --- handleErrors* tests ---

func TestHandleErrorsWaitFirst_PrefersWaitErr(t *testing.T) {
	spec := testSpec()
	spec.ParseErrFirst = false
	waitErr := errors.New("exit status 1")
	parseErr := errors.New("parse failed")

	_, err := handleErrorsWaitFirst(spec, parseErr, waitErr, "stderr", "final")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "process exited") {
		t.Errorf("expected waitErr in message, got: %v", err)
	}
}

func TestHandleErrorsWaitFirst_ParseErrWhenNoWaitErr(t *testing.T) {
	spec := testSpec()
	parseErr := errors.New("parse failed")

	_, err := handleErrorsWaitFirst(spec, parseErr, nil, "stderr", "final")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse stream-json") {
		t.Errorf("expected parseErr in message, got: %v", err)
	}
}

func TestHandleErrorsWaitFirst_EmptyFinal(t *testing.T) {
	spec := testSpec()

	_, err := handleErrorsWaitFirst(spec, nil, nil, "stderr", "")
	if err == nil {
		t.Fatal("expected error for empty final")
	}
	if !strings.Contains(err.Error(), "no assistant content") {
		t.Errorf("expected empty-content error, got: %v", err)
	}
}

func TestHandleErrorsWaitFirst_Success(t *testing.T) {
	spec := testSpec()

	got, err := handleErrorsWaitFirst(spec, nil, nil, "", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Errorf("expected %q, got %q", "hello", got)
	}
}

func TestHandleErrorsParseFirst_PrefersParseErr(t *testing.T) {
	spec := testSpec()
	spec.ParseErrFirst = true
	waitErr := errors.New("exit status 1")
	parseErr := errors.New("parse failed")

	_, err := handleErrorsParseFirst(spec, parseErr, waitErr, "stderr", "final")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse failed") {
		t.Errorf("expected parseErr in message, got: %v", err)
	}
}

func TestHandleErrorsParseFirst_WaitErrWhenNoParseErr(t *testing.T) {
	spec := testSpec()
	waitErr := errors.New("exit status 1")

	_, err := handleErrorsParseFirst(spec, nil, waitErr, "stderr", "final")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "process exited") {
		t.Errorf("expected waitErr in message, got: %v", err)
	}
}

func TestHandleErrorsRateLimit_DetectedFromStderr(t *testing.T) {
	spec := testSpec()

	_, err := handleErrorsWaitFirst(spec, nil, errors.New("exit 1"), "rate limit exceeded", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var tagged *errs.Error
	if !errors.As(err, &tagged) {
		t.Fatalf("expected errs.Error for rate limit, got %T: %v", err, err)
	}
	if tagged.Kind != errs.KindRateLimit {
		t.Errorf("expected rate limit kind, got %q", tagged.Kind)
	}
}

func TestSessionClose_NoError(t *testing.T) {
	s := &Session{}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestLookPath_Exists(t *testing.T) {
	// "echo" exists on all platforms.
	err := LookPath([]string{"echo"})
	if err != nil {
		t.Errorf("LookPath(echo): %v", err)
	}
}

func TestLookPath_NotExists(t *testing.T) {
	err := LookPath([]string{"definitely-nonexistent-binary-xyz"})
	if err == nil {
		t.Error("expected error for non-existent binary")
	}
}

// --- Run tests using a real subprocess ---

// buildEchoBinary compiles testdata/echo.go and returns the path.
// buildEchoBinary is unused; see TestRun_StdinWrite which uses "go run" directly.

func TestRun_StdinWrite(t *testing.T) {
	// Use "go run" with a tiny program that echoes stdin.
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "main.go")
	_ = os.WriteFile(src, []byte(`package main
import (
	"bufio"
	"fmt"
	"io"
	"os"
)
func main() {
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "read error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(line)
}
`), 0o644)

	spec := testSpec()
	spec.DefaultCommand = func(bool) []string {
		return []string{"go", "run", src}
	}
	spec.ReadStreamJSON = func(r io.Reader) (string, error) {
		data, err := io.ReadAll(r)
		return string(data), err
	}

	p := NewProvider(Options{}, spec)
	sess, err := NewSession(p, analyzer.SessionConfig{
		WorkingDirectory: srcDir,
		Sandbox:          "false",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := sess.Run(ctx, "hello from stdin\n", 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result, "hello from stdin") {
		t.Errorf("expected stdin content in output, got %q", result)
	}
}

func TestRun_Timeout(t *testing.T) {
	// Build a binary that sleeps forever. We build then run the binary
	// directly (instead of `go run`) so exec.CommandContext sends SIGKILL
	// to the actual sleeping process — `go run` would only kill the parent
	// go tool, leaving the compiled child alive and stdout open forever.
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "main.go")
	sleepBin := filepath.Join(srcDir, "sleeper")
	if runtime.GOOS == "windows" {
		sleepBin += ".exe"
	}
	_ = os.WriteFile(src, []byte(`package main
import "time"
func main() { time.Sleep(10 * time.Minute) }
`), 0o644)

	build := exec.Command("go", "build", "-o", sleepBin, src)
	build.Dir = srcDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sleeper: %v\n%s", err, out)
	}

	spec := testSpec()
	spec.DefaultCommand = func(bool) []string {
		return []string{sleepBin}
	}

	p := NewProvider(Options{}, spec)
	sess, err := NewSession(p, analyzer.SessionConfig{
		WorkingDirectory: srcDir,
		Sandbox:          "false",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = sess.Run(ctx, "", 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRun_NilContext(t *testing.T) {
	spec := testSpec()
	p := NewProvider(Options{}, spec)
	sess, err := NewSession(p, analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Sandbox:          "false",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = sess.Run(nil, "", 0)
	if !errors.Is(err, analyzer.ErrNilContext) {
		t.Errorf("expected ErrNilContext, got: %v", err)
	}
}

func TestStartErrPlain(t *testing.T) {
	fn := StartErrPlain("my-provider")
	err := fn("binary", errors.New("not found"))
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("expected binary name in error: %v", err)
	}
}

func TestStartErrNotInstalled(t *testing.T) {
	fn := StartErrNotInstalled("install with npm")
	err := fn("binary", errors.New("not found"))
	var tagged *errs.Error
	if !errors.As(err, &tagged) {
		t.Fatalf("expected errs.Error, got %T: %v", err, err)
	}
	if tagged.Kind != errs.KindNotInstalled {
		t.Errorf("expected KindNotInstalled, got %q", tagged.Kind)
	}
}

func TestCmdStartErrPlain(t *testing.T) {
	fn := CmdStartErrPlain("my-provider")
	err := fn(errors.New("exec format error"))
	if !strings.Contains(err.Error(), "start:") {
		t.Errorf("expected 'start:' in error: %v", err)
	}
}

func TestCmdStartErrUnavailable(t *testing.T) {
	fn := CmdStartErrUnavailable("my-provider")
	err := fn(errors.New("permission denied"))
	var tagged *errs.Error
	if !errors.As(err, &tagged) {
		t.Fatalf("expected errs.Error, got %T: %v", err, err)
	}
	if tagged.Kind != errs.KindProviderUnavailable {
		t.Errorf("expected KindProviderUnavailable, got %q", tagged.Kind)
	}
}
