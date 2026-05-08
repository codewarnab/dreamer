package analyzer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

func TestNewClientAppliesOptionsAndAutoStarts(t *testing.T) {
	fakeClient := &fakeSDKClient{}
	var capturedOptions *copilot.ClientOptions

	restore := setSDKClientFactory(func(options *copilot.ClientOptions) sdkClient {
		captured := *options
		if len(options.Env) > 0 {
			captured.Env = append([]string{}, options.Env...)
		}
		capturedOptions = &captured
		return fakeClient
	})
	defer restore()

	client, err := NewClient(ClientOptions{
		CopilotHome:      `C:\Users\User\.copilot`,
		UseLoggedInUser:  true,
		AutoStart:        true,
		Model:            "gpt-5.3-codex",
		WorkingDirectory: `C:\Users\User\code\dreamer`,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if client == nil {
		t.Fatalf("NewClient returned nil client")
	}

	if fakeClient.startCalls != 1 {
		t.Fatalf("startCalls = %d, want 1", fakeClient.startCalls)
	}
	if capturedOptions == nil {
		t.Fatalf("expected SDK client options to be captured")
	}
	if capturedOptions.AutoStart == nil || !*capturedOptions.AutoStart {
		t.Fatalf("AutoStart should be true")
	}
	if capturedOptions.UseLoggedInUser == nil || !*capturedOptions.UseLoggedInUser {
		t.Fatalf("UseLoggedInUser should be true")
	}
	if !hasEnvValue(capturedOptions.Env, "COPILOT_HOME", `C:\Users\User\.copilot`) {
		t.Fatalf("COPILOT_HOME was not set in client env")
	}
}

func TestNewClientRejectsUseLoggedInUserWithCLIURL(t *testing.T) {
	_, err := NewClient(ClientOptions{
		CLIURL:          "localhost:4242",
		UseLoggedInUser: true,
	})
	if err == nil {
		t.Fatalf("expected NewClient error")
	}
	if !strings.Contains(err.Error(), "UseLoggedInUser cannot be enabled when CLIURL is set") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientSessionRunUsesSendAndWaitAndTimeout(t *testing.T) {
	fakeSession := &fakeSDKSession{
		sendAndWaitEvent: &copilot.SessionEvent{
			Data: &copilot.AssistantMessageData{Content: "final response"},
		},
	}
	fakeClient := &fakeSDKClient{
		session: fakeSession,
	}

	restore := setSDKClientFactory(func(options *copilot.ClientOptions) sdkClient {
		return fakeClient
	})
	defer restore()

	client, err := NewClient(ClientOptions{Model: "gpt-5.3-codex"})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	session, err := client.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession returned error: %v", err)
	}

	output, err := session.Run(context.Background(), "analyze this", 2*time.Second)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if output != "final response" {
		t.Fatalf("Run output = %q, want %q", output, "final response")
	}
	if fakeSession.prompt != "analyze this" {
		t.Fatalf("prompt = %q, want %q", fakeSession.prompt, "analyze this")
	}
	if !fakeSession.hadDeadline {
		t.Fatalf("expected Run context to include deadline when timeout is provided")
	}
	if fakeClient.sessionConfig == nil || fakeClient.sessionConfig.Model != "gpt-5.3-codex" {
		t.Fatalf("session model = %q, want %q", fakeClient.sessionConfig.Model, "gpt-5.3-codex")
	}
	if fakeClient.sessionConfig.OnPermissionRequest == nil {
		t.Fatalf("OnPermissionRequest should be configured")
	}
}

func TestClientNewSessionFallsBackToAutoModelWhenRequestedModelFails(t *testing.T) {
	fakeClient := &fakeSDKClient{
		createErrs: []error{
			errors.New("requested model unavailable"),
			nil,
		},
	}

	restore := setSDKClientFactory(func(options *copilot.ClientOptions) sdkClient {
		return fakeClient
	})
	defer restore()

	client, err := NewClient(ClientOptions{Model: "gpt-5.3-codex"})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	session, err := client.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession returned error: %v", err)
	}
	if session == nil {
		t.Fatalf("NewSession returned nil session")
	}

	if got, want := len(fakeClient.sessionConfigs), 2; got != want {
		t.Fatalf("CreateSession call count = %d, want %d", got, want)
	}
	if got, want := fakeClient.sessionConfigs[0].Model, "gpt-5.3-codex"; got != want {
		t.Fatalf("first session model = %q, want %q", got, want)
	}
	if got, want := fakeClient.sessionConfigs[1].Model, ""; got != want {
		t.Fatalf("fallback session model = %q, want empty model", got)
	}
}

func TestReadOnlyPermissionHandlerApprovesAndRejectsExpectedRequests(t *testing.T) {
	handler := buildReadOnlyPermissionHandler(`C:\repo`)

	approvedCases := []copilot.PermissionRequest{
		{Kind: copilot.PermissionRequestKindRead, Path: stringPointer(`C:\repo\README.md`)},
		{Kind: copilot.PermissionRequestKindURL},
		{
			Kind:          copilot.PermissionRequestKindShell,
			Commands:      []copilot.PermissionRequestShellCommand{{Identifier: "git", ReadOnly: true}},
			PossiblePaths: []string{`C:\repo`},
		},
		{
			Kind:     copilot.PermissionRequestKindMcp,
			ReadOnly: copilot.Bool(true),
		},
	}

	for _, request := range approvedCases {
		result, err := handler(request, copilot.PermissionInvocation{})
		if err != nil {
			t.Fatalf("handler returned error for kind %q: %v", request.Kind, err)
		}
		if result.Kind != copilot.PermissionRequestResultKindApproved {
			t.Fatalf("permission result kind = %q, want %q for request kind %q", result.Kind, copilot.PermissionRequestResultKindApproved, request.Kind)
		}
	}

	rejectedCases := []copilot.PermissionRequest{
		{Kind: copilot.PermissionRequestKindWrite},
		{Kind: copilot.PermissionRequestKindMemory},
		{
			Kind: copilot.PermissionRequestKindShell,
			Commands: []copilot.PermissionRequestShellCommand{
				{Identifier: "git", ReadOnly: true},
				{Identifier: "rm", ReadOnly: false},
			},
		},
		{
			Kind:                    copilot.PermissionRequestKindShell,
			HasWriteFileRedirection: copilot.Bool(true),
		},
		{
			Kind: copilot.PermissionRequestKindRead,
			Path: stringPointer(`C:\other\outside.md`),
		},
		{
			Kind:          copilot.PermissionRequestKindShell,
			Commands:      []copilot.PermissionRequestShellCommand{{Identifier: "grep", ReadOnly: true}},
			PossiblePaths: []string{`C:\repo`, `C:\outside`},
		},
	}

	for _, request := range rejectedCases {
		result, err := handler(request, copilot.PermissionInvocation{})
		if err != nil {
			t.Fatalf("handler returned error for kind %q: %v", request.Kind, err)
		}
		if result.Kind != copilot.PermissionRequestResultKindRejected {
			t.Fatalf("permission result kind = %q, want %q for request kind %q", result.Kind, copilot.PermissionRequestResultKindRejected, request.Kind)
		}
	}
}

func TestBuildSessionConfigSetsWorkingDirectoryAndPrompt(t *testing.T) {
	config := buildSessionConfig("gpt-5.3-codex", `C:\repo`)
	if config.WorkingDirectory != `C:\repo` {
		t.Fatalf("WorkingDirectory = %q, want %q", config.WorkingDirectory, `C:\repo`)
	}
	if config.SystemMessage == nil || !strings.Contains(config.SystemMessage.Content, "Scope boundary") {
		t.Fatalf("expected scope boundary prompt guidance")
	}
	if config.OnPermissionRequest == nil {
		t.Fatalf("OnPermissionRequest should be configured")
	}
}

func TestClientAndSessionCloseWrapErrors(t *testing.T) {
	fakeSession := &fakeSDKSession{
		disconnectErr: errors.New("disconnect failed"),
	}
	fakeClient := &fakeSDKClient{
		stopErr:  errors.New("stop failed"),
		session:  fakeSession,
		startErr: nil,
	}

	restore := setSDKClientFactory(func(options *copilot.ClientOptions) sdkClient {
		return fakeClient
	})
	defer restore()

	client, err := NewClient(ClientOptions{})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	session, err := client.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession returned error: %v", err)
	}

	if err := session.Close(); err == nil || !strings.Contains(err.Error(), "close analyzer session") {
		t.Fatalf("session.Close error = %v, want close analyzer session context", err)
	}

	if err := client.Close(); err == nil || !strings.Contains(err.Error(), "close analyzer client") {
		t.Fatalf("client.Close error = %v, want close analyzer client context", err)
	}
}

type fakeSDKClient struct {
	startCalls     int
	startErr       error
	session        sdkSession
	sessionConfig  *copilot.SessionConfig
	sessionConfigs []*copilot.SessionConfig
	createErrs     []error
	createErr      error
	stopErr        error
}

func (f *fakeSDKClient) Start(ctx context.Context) error {
	f.startCalls++
	return f.startErr
}

func (f *fakeSDKClient) CreateSession(ctx context.Context, config *copilot.SessionConfig) (sdkSession, error) {
	if config != nil {
		copied := *config
		f.sessionConfig = &copied
		f.sessionConfigs = append(f.sessionConfigs, &copied)
	} else {
		f.sessionConfig = nil
		f.sessionConfigs = append(f.sessionConfigs, nil)
	}
	callIndex := len(f.sessionConfigs) - 1
	if callIndex < len(f.createErrs) && f.createErrs[callIndex] != nil {
		return nil, f.createErrs[callIndex]
	}
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.session == nil {
		f.session = &fakeSDKSession{}
	}
	return f.session, nil
}

func (f *fakeSDKClient) Stop() error {
	return f.stopErr
}

type fakeSDKSession struct {
	sendAndWaitEvent *copilot.SessionEvent
	sendAndWaitErr   error
	disconnectErr    error
	prompt           string
	hadDeadline      bool
}

func (f *fakeSDKSession) SendAndWait(ctx context.Context, options copilot.MessageOptions) (*copilot.SessionEvent, error) {
	f.prompt = options.Prompt
	_, f.hadDeadline = ctx.Deadline()
	if f.sendAndWaitErr != nil {
		return nil, f.sendAndWaitErr
	}
	return f.sendAndWaitEvent, nil
}

func (f *fakeSDKSession) Disconnect() error {
	return f.disconnectErr
}

func hasEnvValue(env []string, key string, value string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) && strings.TrimPrefix(entry, prefix) == value {
			return true
		}
	}
	return false
}

func setSDKClientFactory(factory sdkClientFactory) func() {
	previous := newSDKClient
	newSDKClient = factory
	return func() {
		newSDKClient = previous
	}
}

func stringPointer(value string) *string {
	return &value
}
