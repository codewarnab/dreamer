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
		CopilotHome:     `C:\Users\User\.copilot`,
		UseLoggedInUser: true,
		AutoStart:       true,
		Model:           "gpt-5",
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

	client, err := NewClient(ClientOptions{Model: "gpt-5"})
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
	if fakeClient.sessionConfig == nil || fakeClient.sessionConfig.Model != "gpt-5" {
		t.Fatalf("session model = %q, want %q", fakeClient.sessionConfig.Model, "gpt-5")
	}
	if fakeClient.sessionConfig.OnPermissionRequest == nil {
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
	startCalls    int
	startErr      error
	session       sdkSession
	sessionConfig *copilot.SessionConfig
	createErr     error
	stopErr       error
}

func (f *fakeSDKClient) Start(ctx context.Context) error {
	f.startCalls++
	return f.startErr
}

func (f *fakeSDKClient) CreateSession(ctx context.Context, config *copilot.SessionConfig) (sdkSession, error) {
	f.sessionConfig = config
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
