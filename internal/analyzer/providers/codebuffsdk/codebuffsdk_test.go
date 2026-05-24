package codebuffsdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dreamer/internal/analyzer"
)

func TestNewRequiresAPIKey(t *testing.T) {
	_, err := New(Options{})
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestNewDefaults(t *testing.T) {
	p, err := New(Options{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.ID() != ID {
		t.Errorf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestStartHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()
}

func TestStartAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, APIKey: "bad-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = p.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail on unauthorized response")
	}
}

func TestStartUnreachable(t *testing.T) {
	p, err := New(Options{BaseURL: "http://127.0.0.1:1", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = p.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail on unreachable server")
	}
}

func TestNewSessionBeforeStart(t *testing.T) {
	p, err := New(Options{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.NewSession(context.Background(), analyzer.SessionConfig{})
	if err == nil {
		t.Fatal("expected error when provider not started")
	}
}

func TestSessionRunEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/chat/completions" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/chat/completions" {
			var req chatCompletionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if req.Model == "" {
				t.Error("request model is empty")
			}
			if len(req.Messages) == 0 {
				t.Error("request has no messages")
			}
			json.NewEncoder(w).Encode(chatCompletionResponse{
				Choices: []choice{{Message: chatMessage{Role: "assistant", Content: "analysis result"}}},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, APIKey: "test-key", Model: "test-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
		SystemMessage:    "you are a test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	completionText, err := sess.Run(context.Background(), "analyze this", 30*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if completionText != "analysis result" {
		t.Errorf("completionText = %q, want %q", completionText, "analysis result")
	}
}

func TestSessionRunRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limit exceeded"))
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = sess.Run(context.Background(), "test", 30*time.Second)
	if err == nil {
		t.Fatal("expected error on rate limit")
	}
}

func TestSessionRunServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = sess.Run(context.Background(), "test", 30*time.Second)
	if err == nil {
		t.Fatal("expected error on server error")
	}
}

func TestSessionRunEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		json.NewEncoder(w).Encode(chatCompletionResponse{Choices: []choice{}})
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = sess.Run(context.Background(), "test", 30*time.Second)
	if err == nil {
		t.Fatal("expected error on empty choices")
	}
}

func TestProviderRegistered(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderCodebuffSDK, analyzer.ProviderConfig{
		Password: "test-key",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p.ID() != ID {
		t.Errorf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestAPIKeyFromEnv(t *testing.T) {
	t.Setenv("MY_CODEBUFF_KEY", "env-key-123")

	p, err := New(Options{APIKey: resolveAPIKey(analyzer.ProviderConfig{
		APIKeyEnv: "MY_CODEBUFF_KEY",
	})})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Verify it didn't error — the key was resolved from env.
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestAPIKeyPasswordFallback(t *testing.T) {
	p, err := New(Options{APIKey: resolveAPIKey(analyzer.ProviderConfig{
		Password: "password-key",
	})})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}
