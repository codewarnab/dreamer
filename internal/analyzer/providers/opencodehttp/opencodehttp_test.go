package opencodehttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
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
	// Bind a port, then close it — guaranteed free at that moment.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	p, err := New(Options{BaseURL: "http://" + addr})
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
				Parts: []messagePart{{Type: "text", Text: "analysis result here"}},
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

	responseText, err := sess.Run(context.Background(), "analyze this", 30*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if responseText != "analysis result here" {
		t.Fatalf("responseText = %q, want %q", responseText, "analysis result here")
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

// --- detectPort tests ---

func TestDetectPortImmediateSuccess(t *testing.T) {
	r := io.NopCloser(strings.NewReader("Listening on http://127.0.0.1:4096\n"))
	port, err := detectPort(r, 5*time.Second)
	if err != nil {
		t.Fatalf("detectPort: %v", err)
	}
	if port != 4096 {
		t.Fatalf("port = %d, want 4096", port)
	}
}

func TestDetectPortTimeout(t *testing.T) {
	// Reader that blocks forever (no data, no EOF).
	r, w := io.Pipe()
	defer w.Close()
	defer r.Close()

	_, err := detectPort(r, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %q, want timeout", err.Error())
	}
}

func TestDetectPortReaderEOF(t *testing.T) {
	r := io.NopCloser(strings.NewReader("some log line without port\n"))
	_, err := detectPort(r, 5*time.Second)
	if err == nil {
		t.Fatal("expected error on EOF without port")
	}
	if !strings.Contains(err.Error(), "server exited without reporting port") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDetectPortPartialWrite(t *testing.T) {
	// Simulate incremental writes: first a partial line, then the port line.
	r, w := io.Pipe()
	go func() {
		defer w.Close()
		fmt.Fprintf(w, "Starting server...\n")
		time.Sleep(20 * time.Millisecond)
		fmt.Fprintf(w, "Listening on http://127.0.0.1:9090\n")
	}()

	port, err := detectPort(r, 5*time.Second)
	if err != nil {
		t.Fatalf("detectPort: %v", err)
	}
	if port != 9090 {
		t.Fatalf("port = %d, want 9090", port)
	}
}

func TestDetectPortIPv6(t *testing.T) {
	r := io.NopCloser(strings.NewReader("listening on http://[::]:8080\n"))
	port, err := detectPort(r, 5*time.Second)
	if err != nil {
		t.Fatalf("detectPort: %v", err)
	}
	if port != 8080 {
		t.Fatalf("port = %d, want 8080", port)
	}
}

func TestDetectPortEmptyReader(t *testing.T) {
	r := io.NopCloser(strings.NewReader(""))
	_, err := detectPort(r, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected error on empty reader")
	}
}

// --- buildEnv tests ---

func TestBuildEnvIncludesParent(t *testing.T) {
	t.Setenv("DREAMER_TEST_FOO", "bar")
	p := &provider{env: map[string]string{"CUSTOM_KEY": "custom_val"}, password: "pw123"}
	env := p.buildEnv()

	var foundParent, foundCustom, foundPassword bool
	for _, kv := range env {
		if kv == "DREAMER_TEST_FOO=bar" {
			foundParent = true
		}
		if kv == "CUSTOM_KEY=custom_val" {
			foundCustom = true
		}
		if kv == "OPENCODE_SERVER_PASSWORD=pw123" {
			foundPassword = true
		}
	}
	if !foundParent {
		t.Fatal("parent env var not found")
	}
	if !foundCustom {
		t.Fatal("custom env var not found")
	}
	if !foundPassword {
		t.Fatal("password env var not found")
	}
}

func TestBuildEnvOverridesParent(t *testing.T) {
	t.Setenv("DREAMER_TEST_OVERRIDE", "original")
	p := &provider{env: map[string]string{"DREAMER_TEST_OVERRIDE": "overridden"}}
	env := p.buildEnv()

	var found bool
	for _, kv := range env {
		if kv == "DREAMER_TEST_OVERRIDE=overridden" {
			found = true
		}
		if kv == "DREAMER_TEST_OVERRIDE=original" {
			t.Fatal("original env should be overridden")
		}
	}
	if !found {
		t.Fatal("overridden env var not found")
	}
}

func TestBuildEnvNoPassword(t *testing.T) {
	p := &provider{}
	env := p.buildEnv()
	for _, kv := range env {
		if strings.HasPrefix(kv, "OPENCODE_SERVER_PASSWORD=") {
			t.Fatal("password should not be set when empty")
		}
	}
}

// --- session.Close test ---

func TestSessionClose(t *testing.T) {
	s := &session{}
	if err := s.Close(); err != nil {
		t.Fatalf("session.Close: %v", err)
	}
}

// --- provider.Close double-close ---

func TestProviderCloseIdempotent(t *testing.T) {
	p := &provider{}
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// --- NewSession before Start ---

func TestNewSessionBeforeStart(t *testing.T) {
	p := &provider{}
	_, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp",
	})
	if err == nil {
		t.Fatal("expected error when calling NewSession before Start")
	}
}

// --- Start idempotent ---

func TestStartIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer p.Close()

	// Second Start should be a no-op.
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
}

// --- Start with LookPath failure (auto-start path) ---

func TestStartAutoStartLookPathFailure(t *testing.T) {
	p := &provider{
		command: []string{"nonexistent_binary_xyz_12345", "serve"},
	}
	err := p.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
	if !strings.Contains(err.Error(), "not_installed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- healthCheck edge cases ---

func TestHealthCheckNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = p.Start(context.Background())
	if err == nil {
		t.Fatal("expected health check to fail with 503")
	}
}

func TestHealthCheckWithAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/global/health" {
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, Password: "mypassword"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	if gotAuth == "" {
		t.Fatal("expected Authorization header to be set during health check")
	}
}

// --- session createSession error paths ---

func TestCreateSessionNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "internal error")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = sess.Run(context.Background(), "test", 30*time.Second)
	if err == nil {
		t.Fatal("expected error on create session failure")
	}
	if !strings.Contains(err.Error(), "create session") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateSessionEmptyID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]string{"id": ""})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = sess.Run(context.Background(), "test", 30*time.Second)
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
	if !strings.Contains(err.Error(), "empty session id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateSessionInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, "not-json")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = sess.Run(context.Background(), "test", 30*time.Second)
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

// --- sendMessage error paths ---

func TestSendMessageNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]string{"id": "sess-err"})
		case r.URL.Path == "/session/sess-err/message" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "bad request")
		case r.URL.Path == "/session/sess-err" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = sess.Run(context.Background(), "test prompt", 30*time.Second)
	if err == nil {
		t.Fatal("expected error on send message failure")
	}
	if !strings.Contains(err.Error(), "send message") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendMessageNoTextParts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]string{"id": "sess-notext"})
		case r.URL.Path == "/session/sess-notext/message" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(messageResponse{
				Parts: []messagePart{{Type: "tool_use", Text: ""}},
			})
		case r.URL.Path == "/session/sess-notext" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = sess.Run(context.Background(), "test prompt", 30*time.Second)
	if err == nil {
		t.Fatal("expected error when response has no text parts")
	}
	if !strings.Contains(err.Error(), "no text parts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendMessageMultipleTextParts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]string{"id": "sess-multi"})
		case r.URL.Path == "/session/sess-multi/message" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(messageResponse{
				Parts: []messagePart{
					{Type: "text", Text: "first part"},
					{Type: "tool_use", Text: "ignored"},
					{Type: "text", Text: "second part"},
				},
			})
		case r.URL.Path == "/session/sess-multi" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	resp, err := sess.Run(context.Background(), "test prompt", 30*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp != "first part\nsecond part" {
		t.Fatalf("response = %q, want %q", resp, "first part\nsecond part")
	}
}

// --- session.Run with system message ---

func TestSessionRunSystemMessage(t *testing.T) {
	var receivedText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]string{"id": "sess-sys"})
		case r.URL.Path == "/session/sess-sys/message" && r.Method == http.MethodPost:
			var req messageRequest
			json.NewDecoder(r.Body).Decode(&req)
			if len(req.Parts) > 0 {
				receivedText = req.Parts[0].Text
			}
			json.NewEncoder(w).Encode(messageResponse{
				Parts: []messagePart{{Type: "text", Text: "ok"}},
			})
		case r.URL.Path == "/session/sess-sys" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
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

	_, err = sess.Run(context.Background(), "analyze this", 30*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(receivedText, "you are a test\n\n") {
		t.Fatalf("expected system message prefix, got: %q", receivedText)
	}
}

// --- NewSession with model override ---

func TestNewSessionModelOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, Model: "default-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
		Model:            "override-model",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	s := sess.(*session)
	if s.model != "override-model" {
		t.Fatalf("model = %q, want %q", s.model, "override-model")
	}
}

func TestNewSessionModelFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL, Model: "default-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	s := sess.(*session)
	if s.model != "default-model" {
		t.Fatalf("model = %q, want %q", s.model, "default-model")
	}
}

// --- Start auto-start path with stderr pipe failure is hard to trigger,
// but we test the command Start failure via a command that exits immediately
// with non-zero so detectPort fails. ---

func TestAutoStartProcessExitBeforePort(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH; cannot test auto-start process exit")
	}
	// "go run -" reads from stdin, gets EOF, and exits without printing a port.
	p := &provider{
		command: []string{"go", "run", "-"},
	}
	t.Cleanup(func() { p.Close() })
	err := p.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for command that doesn't produce port")
	}
}

// --- HealthCheck connection refused ---

func TestHealthCheckConnectionRefused(t *testing.T) {
	// Listen on a free port then immediately close — guaranteed no listener.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	p, err := New(Options{BaseURL: "http://" + addr})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = p.Start(context.Background())
	if err == nil {
		p.Close()
		t.Fatal("expected connection refused error")
	}
}

// --- createSession connection failure ---

func TestCreateSessionConnectionFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	// Close the server to simulate connection failure.
	srv.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = sess.Run(context.Background(), "test", 5*time.Second)
	if err == nil {
		t.Fatal("expected connection error")
	}
}

// --- Run with context cancellation ---

func TestRunContextCancelled(t *testing.T) {
	handlerDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]string{"id": "sess-cancel"})
		case r.URL.Path == "/session/sess-cancel/message" && r.Method == http.MethodPost:
			// Block until test teardown unblocks us. We can't use
			// r.Context().Done() because httptest doesn't always propagate
			// client disconnection to the request context.
			<-handlerDone
			return // client is gone; don't write response
		case r.URL.Path == "/session/sess-cancel" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer func() { close(handlerDone); srv.Close() }()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err = sess.Run(ctx, "test prompt", 10*time.Second)
	if err == nil {
		t.Fatal("expected error due to context cancellation")
	}
}

// --- deleteSession error (non-204) ---

func TestDeleteSessionNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]string{"id": "sess-del"})
		case r.URL.Path == "/session/sess-del/message" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(messageResponse{
				Parts: []messagePart{{Type: "text", Text: "ok"}},
			})
		case r.URL.Path == "/session/sess-del" && r.Method == http.MethodDelete:
			// Return non-204 - should not cause Run to fail since deleteSession is fire-and-forget.
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	resp, err := sess.Run(context.Background(), "test", 30*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("response = %q, want %q", resp, "ok")
	}
}

// --- New with empty command uses default ---

func TestNewDefaultCommand(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	impl := p.(*provider)
	if len(impl.command) != 2 || impl.command[0] != "opencode" || impl.command[1] != "serve" {
		t.Fatalf("default command = %v, want [opencode serve]", impl.command)
	}
}

// --- New trims model whitespace ---

func TestNewTrimsModel(t *testing.T) {
	p, err := New(Options{Model: "  my-model  "})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	impl := p.(*provider)
	if impl.model != "my-model" {
		t.Fatalf("model = %q, want %q", impl.model, "my-model")
	}
}

// --- New trims baseURL ---

func TestNewTrimsBaseURL(t *testing.T) {
	p, err := New(Options{BaseURL: "http://localhost:8080/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	impl := p.(*provider)
	if impl.baseURL != "http://localhost:8080" {
		t.Fatalf("baseURL = %q, want %q", impl.baseURL, "http://localhost:8080")
	}
}

// --- extractPort edge cases ---

func TestExtractPortEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		line string
		want int
	}{
		{"port_out_of_range", "Listening on http://127.0.0.1:99999", 0},
		{"port_zero", "Listening on http://127.0.0.1:0", 0},
		{"no_colon", "Listening on http://127.0.0.1", 0},
		{"empty_port", "Listening on http://127.0.0.1:", 0},
		{"serving_prefix", "Serving on http://0.0.0.0:5555.", 5555},
		{"random_text_with_port", "Something listening on http://127.0.0.1:3000 else", 3000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPort(tt.line)
			if got != tt.want {
				t.Errorf("extractPort(%q) = %d, want %d", tt.line, got, tt.want)
			}
		})
	}
}
