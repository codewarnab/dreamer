package opencodehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dreamer/internal/analyzer"
)

func TestProviderRegistered(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderOpenCodeServer, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p.ID() != ID {
		t.Fatalf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestCustomBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p, err := analyzer.NewProvider(analyzer.ProviderOpenCodeServer, analyzer.ProviderConfig{
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()
}

func TestHealthCheckFailure(t *testing.T) {
	p, err := New(Options{BaseURL: "http://127.0.0.1:1"}) // unlikely to be listening
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = p.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail on unreachable server")
	}
}

func TestSessionRunEndToEnd(t *testing.T) {
	var sessionCreated, messageSent bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})

		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			sessionCreated = true
			json.NewEncoder(w).Encode(map[string]string{"id": "sess-123"})

		case r.URL.Path == "/session/sess-123/message" && r.Method == http.MethodPost:
			messageSent = true
			var req messageRequest
			json.NewDecoder(r.Body).Decode(&req)
			json.NewEncoder(w).Encode(messageResponse{
				Parts: []part{{Type: "text", Text: "analysis result here"}},
			})

		case r.URL.Path == "/session/sess-123" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, Model: "test-model"})
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

	result, err := sess.Run(context.Background(), "analyze this", 30*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result != "analysis result here" {
		t.Fatalf("result = %q, want %q", result, "analysis result here")
	}
	if !sessionCreated {
		t.Fatal("session was not created on server")
	}
	if !messageSent {
		t.Fatal("message was not sent to server")
	}
}

func TestExtractPort(t *testing.T) {
	tests := []struct {
		line string
		want int
	}{
		{"Listening on http://127.0.0.1:4096", 4096},
		{"listening on http://[::]:8080", 8080},
		{"Serving on http://127.0.0.1:3000.", 3000},
		{"some random log line", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := extractPort(tt.line)
		if got != tt.want {
			t.Errorf("extractPort(%q) = %d, want %d", tt.line, got, tt.want)
		}
	}
}

func TestBasicAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/global/health" {
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
			return
		}
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, Password: "secret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	if gotAuth == "" {
		t.Fatal("expected Authorization header to be set")
	}
}
