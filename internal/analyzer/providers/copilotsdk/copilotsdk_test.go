package copilotsdk

import (
	"context"
	"errors"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"dreamer/internal/analyzer"
)

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
