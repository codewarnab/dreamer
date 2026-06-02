package copilotsdk

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"dreamer/internal/analyzer"
)

// MEDIUM #15: SetSDKClientFactory mutates a process-global variable. Adding
// t.Parallel() to any test in this package causes a data race on the global
// factory. Thread the factory through Options to fix, or keep tests serial.

type fakeSDKClient struct {
	startErr   error
	sessionErr error
}

func (f *fakeSDKClient) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("fake client should never receive nil ctx")
	}
	return f.startErr
}

func (f *fakeSDKClient) CreateSession(ctx context.Context, _ *copilot.SessionConfig) (sdkSession, error) {
	if ctx == nil {
		return nil, errors.New("fake client should never receive nil ctx")
	}
	if f.sessionErr != nil {
		return nil, f.sessionErr
	}
	return &fakeSDKSession{}, nil
}

func (f *fakeSDKClient) Stop() error { return nil }

type fakeSDKSession struct{}

func (s *fakeSDKSession) SendAndWait(ctx context.Context, _ copilot.MessageOptions) (*copilot.SessionEvent, error) {
	if ctx == nil {
		return nil, errors.New("fake session should never receive nil ctx")
	}
	return &copilot.SessionEvent{Data: &copilot.AssistantMessageData{Content: "ok"}}, nil
}
func (s *fakeSDKSession) Disconnect() error { return nil }

// B16: provider must surface nil contexts as errors rather than silently
// replacing them with context.Background, which detaches subsequent calls
// from the caller's cancellation tree.
func TestStartReturnsErrorOnNilContext(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	err = p.Start(nil)
	if !errors.Is(err, analyzer.ErrNilContext) {
		t.Fatalf("Start(nil) err = %v, want errors.Is analyzer.ErrNilContext", err)
	}
}

func TestNewSessionReturnsErrorOnNilContext(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	_, err = p.NewSession(nil, analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if !errors.Is(err, analyzer.ErrNilContext) {
		t.Fatalf("NewSession(nil, …) err = %v, want errors.Is analyzer.ErrNilContext", err)
	}
}

func TestSessionRunReturnsErrorOnNilContext(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()
	_, err = sess.Run(nil, "hi", time.Second)
	if !errors.Is(err, analyzer.ErrNilContext) {
		t.Fatalf("Run(nil, …) err = %v, want errors.Is analyzer.ErrNilContext", err)
	}
}

// --- Additional edge-case tests ---

func TestProviderID(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.ID() != ID {
		t.Fatalf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestProviderSupportsParallelSessions(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	if !analyzer.ProviderSupportsParallel(p) {
		t.Fatal("copilot-sdk should report SupportsParallelSessions = true")
	}
}

func TestStartIdempotent(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
}

func TestStartError(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClient{startErr: errors.New("auth failed")}
	})
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = p.Start(context.Background())
	if err == nil {
		t.Fatal("expected start error")
	}
}

func TestNewSessionStartError(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClient{startErr: errors.New("auth failed")}
	})
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err == nil {
		t.Fatal("expected error when start fails")
	}
}

func TestNewSessionCreateError(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClient{sessionErr: errors.New("create failed")}
	})
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err == nil {
		t.Fatal("expected error when create session fails")
	}
}

func TestNewSessionWithModel(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Model:            "gpt-5",
	})
	if err != nil {
		t.Fatalf("NewSession with model: %v", err)
	}
	defer sess.Close()
}

func TestSessionRunHappyPath(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()
	result, err := sess.Run(context.Background(), "hello", 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "ok" {
		t.Fatalf("Run result = %q, want %q", result, "ok")
	}
}

func TestSessionRunWithTimeout(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()
	result, err := sess.Run(context.Background(), "hello", 30*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "ok" {
		t.Fatalf("Run result = %q, want %q", result, "ok")
	}
}

func TestSessionClose(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestProviderClose(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} })
	defer restore()
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSetSDKClientFactoryRestore(t *testing.T) {
	fakeFactory := func(*copilot.ClientOptions) sdkClient { return &fakeSDKClient{} }
	restore := SetSDKClientFactory(fakeFactory)
	// The installed factory should produce a fakeSDKClient
	c := newSDKClient(nil)
	if _, ok := c.(*fakeSDKClient); !ok {
		t.Fatal("expected fakeSDKClient after SetSDKClientFactory")
	}
	restore()
	// After restore, calling restore again should be safe (no-op)
	restore2 := SetSDKClientFactory(fakeFactory)
	restore2()
}

// --- translateCopilotRequest ---

func TestTranslateCopilotRequestRead(t *testing.T) {
	path := "/foo/bar"
	req := copilot.PermissionRequest{Kind: copilot.PermissionRequestKindRead, Path: &path}
	out := translateCopilotRequest(req)
	if out.Kind != analyzer.PermissionKindRead {
		t.Fatalf("Kind = %q, want read", out.Kind)
	}
	if out.Path == nil || *out.Path != "/foo/bar" {
		t.Fatalf("Path = %v, want /foo/bar", out.Path)
	}
}

func TestTranslateCopilotRequestURL(t *testing.T) {
	req := copilot.PermissionRequest{Kind: copilot.PermissionRequestKindURL}
	out := translateCopilotRequest(req)
	if out.Kind != analyzer.PermissionKindURL {
		t.Fatalf("Kind = %q, want url", out.Kind)
	}
}

func TestTranslateCopilotRequestShell(t *testing.T) {
	req := copilot.PermissionRequest{Kind: copilot.PermissionRequestKindShell}
	out := translateCopilotRequest(req)
	if out.Kind != analyzer.PermissionKindShell {
		t.Fatalf("Kind = %q, want shell", out.Kind)
	}
}

func TestTranslateCopilotRequestMcp(t *testing.T) {
	req := copilot.PermissionRequest{Kind: copilot.PermissionRequestKindMcp}
	out := translateCopilotRequest(req)
	if out.Kind != analyzer.PermissionKindMCPTool {
		t.Fatalf("Kind = %q, want mcp", out.Kind)
	}
}

func TestTranslateCopilotRequestCustomTool(t *testing.T) {
	req := copilot.PermissionRequest{Kind: copilot.PermissionRequestKindCustomTool}
	out := translateCopilotRequest(req)
	if out.Kind != analyzer.PermissionKindCustomTool {
		t.Fatalf("Kind = %q, want custom-tool", out.Kind)
	}
}

func TestTranslateCopilotRequestUnknownKind(t *testing.T) {
	req := copilot.PermissionRequest{Kind: copilot.PermissionRequestKind("future")}
	out := translateCopilotRequest(req)
	if string(out.Kind) != "future" {
		t.Fatalf("Kind = %q, want future", out.Kind)
	}
}

func TestTranslateCopilotRequestCommands(t *testing.T) {
	req := copilot.PermissionRequest{
		Commands: []copilot.PermissionRequestShellCommand{
			{Identifier: "git", ReadOnly: true},
			{Identifier: "rm", ReadOnly: false},
		},
	}
	out := translateCopilotRequest(req)
	if len(out.Commands) != 2 {
		t.Fatalf("Commands len = %d, want 2", len(out.Commands))
	}
	if out.Commands[0].Identifier != "git" || !out.Commands[0].ReadOnly {
		t.Fatalf("Commands[0] = %+v", out.Commands[0])
	}
}

func TestTranslateCopilotRequestPossiblePaths(t *testing.T) {
	req := copilot.PermissionRequest{PossiblePaths: []string{"/a", "/b"}}
	out := translateCopilotRequest(req)
	if len(out.PossiblePaths) != 2 {
		t.Fatalf("PossiblePaths len = %d, want 2", len(out.PossiblePaths))
	}
}

// --- permissionApproved / permissionRejected ---

func TestPermissionApproved(t *testing.T) {
	r := permissionApproved()
	if r.Kind != copilot.PermissionRequestResultKindApproved {
		t.Fatalf("Kind = %q, want approved", r.Kind)
	}
}

func TestPermissionRejected(t *testing.T) {
	r := permissionRejected("outside root")
	if r.Kind != copilot.PermissionRequestResultKindRejected {
		t.Fatalf("Kind = %q, want rejected", r.Kind)
	}
	if len(r.Rules) != 1 {
		t.Fatalf("Rules len = %d, want 1", len(r.Rules))
	}
}

func TestPermissionRejectedEmptyReason(t *testing.T) {
	r := permissionRejected("")
	if len(r.Rules) != 0 {
		t.Fatalf("Rules len = %d, want 0 for empty reason", len(r.Rules))
	}
}

// --- upsertEnvVar ---

func TestUpsertEnvVarNew(t *testing.T) {
	got := upsertEnvVar([]string{"A=1"}, "B", "2")
	if len(got) != 2 || got[1] != "B=2" {
		t.Fatalf("got %v, want [A=1 B=2]", got)
	}
}

func TestUpsertEnvVarReplace(t *testing.T) {
	got := upsertEnvVar([]string{"A=1", "B=2"}, "B", "3")
	if len(got) != 2 || got[1] != "B=3" {
		t.Fatalf("got %v, want [A=1 B=3]", got)
	}
}

func TestUpsertEnvVarDuplicateReplace(t *testing.T) {
	got := upsertEnvVar([]string{"A=1", "A=2"}, "A", "3")
	if len(got) != 1 || got[0] != "A=3" {
		t.Fatalf("got %v, want [A=3]", got)
	}
}

func TestUpsertEnvVarEmpty(t *testing.T) {
	got := upsertEnvVar(nil, "K", "V")
	if len(got) != 1 || got[0] != "K=V" {
		t.Fatalf("got %v, want [K=V]", got)
	}
}

// --- buildSDKClientOptions ---

func TestBuildSDKClientOptionsCLIURLConflict(t *testing.T) {
	_, err := buildSDKClientOptions(Options{
		CLIURL:             "http://localhost:8080",
		UseLoggedInUserSet: true,
		UseLoggedInUser:    true,
	})
	if err == nil {
		t.Fatal("expected error for CLIURL + UseLoggedInUser conflict")
	}
}

func TestBuildSDKClientOptionsCopilotHome(t *testing.T) {
	opts, err := buildSDKClientOptions(Options{CopilotHome: "/custom/home"})
	if err != nil {
		t.Fatalf("buildSDKClientOptions: %v", err)
	}
	if opts.Env == nil {
		t.Fatal("expected Env to be set for CopilotHome")
	}
}

func TestBuildSDKClientOptionsCLIURLNoConflict(t *testing.T) {
	opts, err := buildSDKClientOptions(Options{
		CLIURL:             "http://localhost:8080",
		UseLoggedInUserSet: true,
		UseLoggedInUser:    false,
	})
	if err != nil {
		t.Fatalf("buildSDKClientOptions: %v", err)
	}
	if opts.CLIUrl != "http://localhost:8080" {
		t.Fatalf("CLIUrl = %q", opts.CLIUrl)
	}
}

func TestBuildSDKClientOptionsUseLoggedInUserFalse(t *testing.T) {
	opts, err := buildSDKClientOptions(Options{
		UseLoggedInUserSet: true,
		UseLoggedInUser:    false,
	})
	if err != nil {
		t.Fatalf("buildSDKClientOptions: %v", err)
	}
	if opts.UseLoggedInUser == nil || *opts.UseLoggedInUser != false {
		t.Fatalf("UseLoggedInUser = %v, want false", opts.UseLoggedInUser)
	}
}

// --- buildSessionConfig ---

func TestBuildSessionConfigDefaultSystemMessage(t *testing.T) {
	cfg := buildSessionConfig("", analyzer.SessionConfig{WorkingDirectory: "/tmp", RunID: "run1"})
	if cfg.SystemMessage == nil {
		t.Fatal("expected non-nil SystemMessage")
	}
	if cfg.SystemMessage.Mode != "append" {
		t.Fatalf("Mode = %q, want append", cfg.SystemMessage.Mode)
	}
	if !strings.Contains(cfg.SystemMessage.Content, "run1") {
		t.Fatal("expected run ID in system message")
	}
}

func TestBuildSessionConfigCustomSystemMessage(t *testing.T) {
	cfg := buildSessionConfig("gpt-5", analyzer.SessionConfig{
		WorkingDirectory: "/tmp",
		SystemMessage:    "custom message",
	})
	if cfg.Model != "gpt-5" {
		t.Fatalf("Model = %q, want gpt-5", cfg.Model)
	}
	if cfg.SystemMessage.Content != "custom message" {
		t.Fatalf("SystemMessage = %q", cfg.SystemMessage.Content)
	}
}

// --- provider.Start happy path (not yet started) ---

func TestStartHappyPath(t *testing.T) {
	var startCalled bool
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClientWithStartTracking{called: &startCalled}
	})
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !startCalled {
		t.Fatal("expected client.Start to be called")
	}
}

type fakeSDKClientWithStartTracking struct {
	called *bool
}

func (f *fakeSDKClientWithStartTracking) Start(ctx context.Context) error {
	*f.called = true
	return nil
}
func (f *fakeSDKClientWithStartTracking) CreateSession(_ context.Context, _ *copilot.SessionConfig) (sdkSession, error) {
	return &fakeSDKSession{}, nil
}
func (f *fakeSDKClientWithStartTracking) Stop() error { return nil }

// --- NewSession happy path (triggers lazy Start) ---

func TestNewSessionTriggersLazyStart(t *testing.T) {
	var startCalled bool
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClientLazyStart{startCalled: &startCalled}
	})
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	// NewSession should call Start internally.
	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	if !startCalled {
		t.Fatal("expected Start to be called lazily from NewSession")
	}
}

type fakeSDKClientLazyStart struct {
	startCalled *bool
}

func (f *fakeSDKClientLazyStart) Start(_ context.Context) error {
	*f.startCalled = true
	return nil
}
func (f *fakeSDKClientLazyStart) CreateSession(_ context.Context, _ *copilot.SessionConfig) (sdkSession, error) {
	return &fakeSDKSession{}, nil
}
func (f *fakeSDKClientLazyStart) Stop() error { return nil }

// --- NewSession with model fallback ---

func TestNewSessionModelFallback(t *testing.T) {
	var createCalls int
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClientModelFallback{calls: &createCalls}
	})
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Model:            "nonexistent-model",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	// First call fails, second (fallback) succeeds.
	if createCalls != 2 {
		t.Fatalf("CreateSession called %d times, want 2 (original + fallback)", createCalls)
	}
}

type fakeSDKClientModelFallback struct {
	calls *int
}

func (f *fakeSDKClientModelFallback) Start(_ context.Context) error { return nil }
func (f *fakeSDKClientModelFallback) CreateSession(_ context.Context, cfg *copilot.SessionConfig) (sdkSession, error) {
	*f.calls++
	if cfg.Model != "" {
		return nil, errors.New("model not available")
	}
	return &fakeSDKSession{}, nil
}
func (f *fakeSDKClientModelFallback) Stop() error { return nil }

// --- NewSession with model fallback also failing ---

func TestNewSessionModelFallbackBothFail(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClientAlwaysFail{}
	})
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	_, err = p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Model:            "bad-model",
	})
	if err == nil {
		t.Fatal("expected error when both original and fallback CreateSession fail")
	}
	if !strings.Contains(err.Error(), "requested model") {
		t.Errorf("error should mention requested model, got: %v", err)
	}
}

type fakeSDKClientAlwaysFail struct{}

func (f *fakeSDKClientAlwaysFail) Start(_ context.Context) error { return nil }
func (f *fakeSDKClientAlwaysFail) CreateSession(_ context.Context, _ *copilot.SessionConfig) (sdkSession, error) {
	return nil, errors.New("always fails")
}
func (f *fakeSDKClientAlwaysFail) Stop() error { return nil }

// --- session.Run with SendAndWait error ---

func TestSessionRunSendAndWaitError(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClientSessionErr{}
	})
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	_, err = sess.Run(context.Background(), "prompt", 0)
	if err == nil {
		t.Fatal("expected error from SendAndWait failure")
	}
	if !strings.Contains(err.Error(), "run copilot-sdk prompt") {
		t.Errorf("error should contain context prefix, got: %v", err)
	}
}

type fakeSDKClientSessionErr struct{}

func (f *fakeSDKClientSessionErr) Start(_ context.Context) error { return nil }
func (f *fakeSDKClientSessionErr) CreateSession(_ context.Context, _ *copilot.SessionConfig) (sdkSession, error) {
	return &fakeSDKSessionErr{}, nil
}
func (f *fakeSDKClientSessionErr) Stop() error { return nil }

type fakeSDKSessionErr struct{}

func (s *fakeSDKSessionErr) SendAndWait(_ context.Context, _ copilot.MessageOptions) (*copilot.SessionEvent, error) {
	return nil, errors.New("send failed")
}
func (s *fakeSDKSessionErr) Disconnect() error { return nil }

// --- session.Run with nil event data ---

func TestSessionRunNilEventData(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClientNilEvent{}
	})
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	_, err = sess.Run(context.Background(), "prompt", 0)
	if err == nil {
		t.Fatal("expected error for nil event data")
	}
	if !strings.Contains(err.Error(), "without assistant output") {
		t.Errorf("error should mention missing output, got: %v", err)
	}
}

type fakeSDKClientNilEvent struct{}

func (f *fakeSDKClientNilEvent) Start(_ context.Context) error { return nil }
func (f *fakeSDKClientNilEvent) CreateSession(_ context.Context, _ *copilot.SessionConfig) (sdkSession, error) {
	return &fakeSDKSessionNilEvent{}, nil
}
func (f *fakeSDKClientNilEvent) Stop() error { return nil }

type fakeSDKSessionNilEvent struct{}

func (s *fakeSDKSessionNilEvent) SendAndWait(_ context.Context, _ copilot.MessageOptions) (*copilot.SessionEvent, error) {
	return &copilot.SessionEvent{Data: nil}, nil
}
func (s *fakeSDKSessionNilEvent) Disconnect() error { return nil }

// --- session.Run with unexpected response type ---

func TestSessionRunUnexpectedResponseType(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClientBadType{}
	})
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	_, err = sess.Run(context.Background(), "prompt", 0)
	if err == nil {
		t.Fatal("expected error for unexpected response type")
	}
	if !strings.Contains(err.Error(), "unexpected response type") {
		t.Errorf("error should mention unexpected type, got: %v", err)
	}
}

type fakeSDKClientBadType struct{}

func (f *fakeSDKClientBadType) Start(_ context.Context) error { return nil }
func (f *fakeSDKClientBadType) CreateSession(_ context.Context, _ *copilot.SessionConfig) (sdkSession, error) {
	return &fakeSDKSessionBadType{}, nil
}
func (f *fakeSDKClientBadType) Stop() error { return nil }

type fakeSDKSessionBadType struct{}

func (s *fakeSDKSessionBadType) SendAndWait(_ context.Context, _ copilot.MessageOptions) (*copilot.SessionEvent, error) {
	// Return a non-nil SessionEventData that is NOT *AssistantMessageData
	// to trigger the "unexpected response type" error path.
	return &copilot.SessionEvent{Data: &copilot.SessionCompactionStartData{}}, nil
}
func (s *fakeSDKSessionBadType) Disconnect() error { return nil }

// --- session.Run with timeout that expires ---

func TestSessionRunTimeoutExpires(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClientSlow{}
	})
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	_, err = sess.Run(context.Background(), "prompt", 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

type fakeSDKClientSlow struct{}

func (f *fakeSDKClientSlow) Start(_ context.Context) error { return nil }
func (f *fakeSDKClientSlow) CreateSession(_ context.Context, _ *copilot.SessionConfig) (sdkSession, error) {
	return &fakeSDKSessionSlow{}, nil
}
func (f *fakeSDKClientSlow) Stop() error { return nil }

type fakeSDKSessionSlow struct{}

func (s *fakeSDKSessionSlow) SendAndWait(ctx context.Context, _ copilot.MessageOptions) (*copilot.SessionEvent, error) {
	// Block until context expires.
	<-ctx.Done()
	return nil, ctx.Err()
}
func (s *fakeSDKSessionSlow) Disconnect() error { return nil }

// --- buildPermissionHandler ---

func TestBuildPermissionHandlerApproved(t *testing.T) {
	dir := t.TempDir()
	handler := buildPermissionHandler(dir)

	path := dir + "/somefile.txt"
	readOnly := true
	result, err := handler(copilot.PermissionRequest{
		Kind:     copilot.PermissionRequestKindRead,
		Path:     &path,
		ReadOnly: &readOnly,
	}, copilot.PermissionInvocation{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.Kind != copilot.PermissionRequestResultKindApproved {
		t.Fatalf("expected approved, got %q", result.Kind)
	}
}

func TestBuildPermissionHandlerRejected(t *testing.T) {
	dir := t.TempDir()
	handler := buildPermissionHandler(dir)

	outsidePath := "/outside/project/file.txt"
	result, err := handler(copilot.PermissionRequest{
		Kind: copilot.PermissionRequestKindRead,
		Path: &outsidePath,
	}, copilot.PermissionInvocation{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.Kind != copilot.PermissionRequestResultKindRejected {
		t.Fatalf("expected rejected, got %q", result.Kind)
	}
	if len(result.Rules) == 0 {
		t.Fatal("expected rejection rules to be set")
	}
}

func TestBuildPermissionHandlerInvalidRoot(t *testing.T) {
	// Empty string causes NormalizeRootPath error.
	handler := buildPermissionHandler("")

	result, err := handler(copilot.PermissionRequest{}, copilot.PermissionInvocation{})
	if err != nil {
		t.Fatalf("handler should not return error, got: %v", err)
	}
	if result.Kind != copilot.PermissionRequestResultKindRejected {
		t.Fatalf("expected rejected for invalid root, got %q", result.Kind)
	}
}

// --- Provider.Close with Stop error ---

func TestProviderCloseStopError(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClientStopErr{}
	})
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = p.Close()
	if err == nil {
		t.Fatal("expected error from Close when Stop fails")
	}
	if !strings.Contains(err.Error(), "close copilot-sdk provider") {
		t.Errorf("error should have context prefix, got: %v", err)
	}
}

type fakeSDKClientStopErr struct{}

func (f *fakeSDKClientStopErr) Start(_ context.Context) error { return nil }
func (f *fakeSDKClientStopErr) CreateSession(_ context.Context, _ *copilot.SessionConfig) (sdkSession, error) {
	return &fakeSDKSession{}, nil
}
func (f *fakeSDKClientStopErr) Stop() error { return errors.New("stop failed") }

// --- Session.Close with Disconnect error ---

func TestSessionCloseDisconnectError(t *testing.T) {
	restore := SetSDKClientFactory(func(*copilot.ClientOptions) sdkClient {
		return &fakeSDKClientDisconnectErr{}
	})
	defer restore()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	err = sess.Close()
	if err == nil {
		t.Fatal("expected error from Close when Disconnect fails")
	}
	if !strings.Contains(err.Error(), "close copilot-sdk session") {
		t.Errorf("error should have context prefix, got: %v", err)
	}
}

type fakeSDKClientDisconnectErr struct{}

func (f *fakeSDKClientDisconnectErr) Start(_ context.Context) error { return nil }
func (f *fakeSDKClientDisconnectErr) CreateSession(_ context.Context, _ *copilot.SessionConfig) (sdkSession, error) {
	return &fakeSDKSessionDisconnectErr{}, nil
}
func (f *fakeSDKClientDisconnectErr) Stop() error { return nil }

type fakeSDKSessionDisconnectErr struct{}

func (s *fakeSDKSessionDisconnectErr) SendAndWait(_ context.Context, _ copilot.MessageOptions) (*copilot.SessionEvent, error) {
	return &copilot.SessionEvent{Data: &copilot.AssistantMessageData{Content: "ok"}}, nil
}
func (s *fakeSDKSessionDisconnectErr) Disconnect() error { return errors.New("disconnect failed") }
