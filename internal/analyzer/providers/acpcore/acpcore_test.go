package acpcore

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"dreamer/internal/analyzer"
)

// --- New validation ---

func TestNewRequiresID(t *testing.T) {
	_, err := New(Options{Command: []string{"cmd"}})
	if err == nil {
		t.Fatal("empty ID should error")
	}
}

func TestNewRequiresCommand(t *testing.T) {
	_, err := New(Options{ID: "test"})
	if err == nil {
		t.Fatal("empty Command should error")
	}
}

func TestNewCopiesCommand(t *testing.T) {
	cmd := []string{"original"}
	p, err := New(Options{ID: "test", Command: cmd})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cmd[0] = "mutated"
	stored, _, ok := InspectProvider(p)
	if !ok {
		t.Fatalf("InspectProvider: not an acpcore provider")
	}
	if stored[0] != "original" {
		t.Fatalf("command not copied: got %q, want %q", stored[0], "original")
	}
}

func TestNewNilEnvProducesNilMap(t *testing.T) {
	p, err := New(Options{ID: "test", Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	prov := p.(*provider)
	if prov.env != nil {
		t.Fatalf("env = %v, want nil", prov.env)
	}
}

func TestNewID(t *testing.T) {
	p, err := New(Options{ID: "my-provider", Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.ID() != "my-provider" {
		t.Fatalf("ID = %q, want %q", p.ID(), "my-provider")
	}
}

// --- NewSession before Start ---

func TestNewSessionBeforeStart(t *testing.T) {
	p, err := New(Options{ID: "test", Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.NewSession(context.Background(), analyzer.SessionConfig{})
	if err == nil {
		t.Fatal("expected error for NewSession before Start")
	}
}

// --- Close idempotent ---

func TestCloseIdempotent(t *testing.T) {
	p, err := New(Options{ID: "test", Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// --- session.Run nil context ---

func TestSessionRunNilContext(t *testing.T) {
	p, err := New(Options{ID: "test", Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	prov := p.(*provider)
	// Manually set up a minimal transport so NewSession succeeds
	prov.started = true
	prov.transport = &transport{}
	sess, err := prov.NewSession(context.Background(), analyzer.SessionConfig{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, err = sess.Run(nil, "test", 0)
	if !errors.Is(err, analyzer.ErrNilContext) {
		t.Fatalf("err = %v, want ErrNilContext", err)
	}
}

// TestSessionRunFailsFastAfterTransportClose drives the §20.20 acceptance:
// when the ACP child exits between rules, the next session.Run must
// return ErrTransportClosed within 100 ms (not the rule timeout).
//
// We bypass the full ACP initialize handshake — we want to drive the
// transport directly, observe the readLoop reacting to stdout EOF, and
// assert session.Run's liveness gate triggers.
func TestSessionRunFailsFastAfterTransportClose(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("true binary not found in PATH")
	}
	repo := t.TempDir()

	// `true` exits 0 immediately so its stdout closes within
	// milliseconds. dialStdio spawns + starts the readLoop goroutine
	// which will observe EOF and call markClosed().
	transport, err := dialStdio(context.Background(), "test-acp", []string{truePath}, nil, "false")
	if err != nil {
		t.Fatalf("dialStdio: %v", err)
	}
	defer transport.close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !transport.isClosed() {
		time.Sleep(2 * time.Millisecond)
	}
	if !transport.isClosed() {
		t.Fatalf("transport should observe EOF and flip closed flag within 1s")
	}

	sess := &session{transport: transport, workingDir: repo, model: "sonnet"}
	start := time.Now()
	_, err = sess.Run(context.Background(), "hello", 5*time.Second)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("Run against closed transport should error")
	}
	if !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("err = %v, want errors.Is ErrTransportClosed", err)
	}
	if !errors.Is(err, analyzer.ErrUnavailable) {
		t.Fatalf("err = %v, want errors.Is analyzer.ErrUnavailable (joined sentinel)", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Run on closed transport returned in %v, want <=100ms", elapsed)
	}
}

// --- acpWritableDirs edge cases ---

func TestACPWritableDirsHonorProviderConfigEnv(t *testing.T) {
	claudeConfigDir := t.TempDir()
	dirs, err := acpWritableDirs(string(analyzer.ProviderClaudeACP), map[string]string{
		"CLAUDE_CONFIG_DIR": claudeConfigDir,
	})
	if err != nil {
		t.Fatalf("acpWritableDirs: %v", err)
	}
	for _, dir := range dirs {
		if dir == claudeConfigDir {
			return
		}
	}
	t.Fatalf("CLAUDE_CONFIG_DIR %q not found in writable dirs: %v", claudeConfigDir, dirs)
}

func TestACPWritableDirsUnknownProvider(t *testing.T) {
	dirs, err := acpWritableDirs("unknown-provider", nil)
	if err != nil {
		t.Fatalf("acpWritableDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected only temp dir for unknown provider, got %d", len(dirs))
	}
}

func TestACPWritableDirsGeminiEnv(t *testing.T) {
	dir := t.TempDir()
	dirs, err := acpWritableDirs(string(analyzer.ProviderGeminiACP), map[string]string{
		"GEMINI_HOME": dir,
	})
	if err != nil {
		t.Fatalf("acpWritableDirs: %v", err)
	}
	found := false
	for _, d := range dirs {
		if d == dir {
			found = true
		}
	}
	if !found {
		t.Fatalf("GEMINI_HOME %q not in dirs: %v", dir, dirs)
	}
}

func TestACPWritableDirsCodexEnv(t *testing.T) {
	dir := t.TempDir()
	dirs, err := acpWritableDirs(string(analyzer.ProviderCodexACP), map[string]string{
		"CODEX_HOME": dir,
	})
	if err != nil {
		t.Fatalf("acpWritableDirs: %v", err)
	}
	found := false
	for _, d := range dirs {
		if d == dir {
			found = true
		}
	}
	if !found {
		t.Fatalf("CODEX_HOME %q not in dirs: %v", dir, dirs)
	}
}

func TestACPWritableDirsCopilot(t *testing.T) {
	dirs, err := acpWritableDirs(string(analyzer.ProviderCopilotACP), nil)
	if err != nil {
		t.Fatalf("acpWritableDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("copilot should have exactly 1 dir (temp), got %d", len(dirs))
	}
}

// --- resolveACPConfigDir ---

func TestResolveACPConfigDirFromEnvMap(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveACPConfigDir(map[string]string{"MY_ENV": dir}, "MY_ENV", "fallback")
	if err != nil {
		t.Fatalf("resolveACPConfigDir: %v", err)
	}
	if got != dir {
		t.Fatalf("got %q, want %q", got, dir)
	}
}

func TestResolveACPConfigDirFallback(t *testing.T) {
	got, err := resolveACPConfigDir(nil, "NONEXISTENT_ENV_VAR_12345", ".fallback")
	if err != nil {
		t.Fatalf("resolveACPConfigDir: %v", err)
	}
	if !strings.HasSuffix(got, ".fallback") {
		t.Fatalf("got %q, expected suffix .fallback", got)
	}
}

// --- copyStringMap ---

func TestCopyStringMapNil(t *testing.T) {
	if got := copyStringMap(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := copyStringMap(map[string]string{}); got != nil {
		t.Fatalf("empty: got %v, want nil", got)
	}
}

func TestCopyStringMapCopies(t *testing.T) {
	in := map[string]string{"a": "1", "b": "2"}
	out := copyStringMap(in)
	in["a"] = "mutated"
	if out["a"] != "1" {
		t.Fatalf("map was not copied")
	}
}

