// Package opencodehttp implements an analyzer.Provider that communicates
// with an OpenCode HTTP server (opencode serve) via REST API.
package opencodehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/chat"
	"dreamer/internal/errs"
)

const ID = "opencode-server"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderOpenCodeServer, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		model := providerConfig.Model
		if strings.TrimSpace(model) == "" {
			model = providerConfig.DefaultModel
		}
		return New(Options{
			BaseURL:  providerConfig.BaseURL,
			Command:  providerConfig.Command,
			Env:      providerConfig.Env,
			Model:    model,
			Password: providerConfig.Password,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderOpenCodeServer,
		DisplayName: "OpenCode Server (HTTP)",
		Order:       120,
	})
}

// Options carries per-provider configuration.
type Options struct {
	// BaseURL is the HTTP server URL (e.g. "http://127.0.0.1:4096").
	// If empty, the provider auto-starts `opencode serve`.
	BaseURL string

	// Command is the argv to auto-start the server. Default: ["opencode", "serve"]
	Command []string

	// Env adds environment variables on top of the parent process env.
	Env map[string]string

	// Model is the default model for analysis sessions.
	Model string

	// Password for HTTP basic auth (OPENCODE_SERVER_PASSWORD).
	Password string
}

// New builds an opencode-server Provider.
func New(options Options) (analyzer.Provider, error) {
	cmd := append([]string(nil), options.Command...)
	if len(cmd) == 0 {
		cmd = []string{"opencode", "serve"}
	}
	model := strings.TrimSpace(options.Model)
	return &provider{
		baseURL:  strings.TrimRight(strings.TrimSpace(options.BaseURL), "/"),
		command:  cmd,
		env:      options.Env,
		model:    model,
		password: options.Password,
	}, nil
}

type provider struct {
	baseURL  string
	command  []string
	env      map[string]string
	model    string
	password string

	mu        sync.Mutex
	started   bool
	closed    bool
	autoStart bool      // true if we spawned the server ourselves
	cmd       *exec.Cmd // non-nil when auto-started
}

func (p *provider) ID() string { return ID }

// Start either health-checks an existing server or spawns one.
func (p *provider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return nil
	}

	if p.baseURL != "" {
		// Connect to existing server.
		if err := p.healthCheck(ctx); err != nil {
			return errs.NotInstalled("opencode-server", "start",
				fmt.Sprintf("Cannot reach OpenCode server at %s. Start it with `opencode serve` or check the URL.", p.baseURL), err)
		}
		p.started = true
		return nil
	}

	// Auto-start: spawn `opencode serve` on a random port.
	if _, err := exec.LookPath(p.command[0]); err != nil {
		return errs.NotInstalled("opencode-server", "start",
			"Install OpenCode (see https://opencode.ai) and ensure `opencode` is in PATH.", err)
	}

	args := append([]string(nil), p.command[1:]...)
	args = append(args, "--port", "0") // random port
	// Detach from Start ctx: pipeline cancels startCtx after Start returns,
	// which would SIGKILL the server before any session runs.
	cmd := exec.Command(p.command[0], args...)
	cmd.Env = p.buildEnv()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("opencode-server: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opencode-server: start: %w", err)
	}
	p.cmd = cmd
	p.autoStart = true

	// Read stderr to find the port.
	port, err := detectPort(stderr, 10*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("opencode-server: detect port: %w", err)
	}
	defer stderr.Close() // pipe no longer needed after port detected
	p.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	// Health-check the newly started server.
	if err := p.healthCheck(ctx); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("opencode-server: health check after start: %w", err)
	}
	p.started = true
	return nil
}

func (p *provider) NewSession(ctx context.Context, sessionConfig analyzer.SessionConfig) (analyzer.Session, error) {
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if !started {
		return nil, errors.New("opencode-server: provider not started")
	}

	model := strings.TrimSpace(sessionConfig.Model)
	if model == "" {
		model = p.model
	}

	return &session{
		provider:   p,
		workingDir: sessionConfig.WorkingDirectory,
		model:      model,
		sysMessage: sessionConfig.SystemMessage,
		runID:      sessionConfig.RunID,
	}, nil
}

func (p *provider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true

	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
	return nil
}

func (p *provider) healthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/global/health", nil)
	if err != nil {
		return err
	}
	p.setBasicAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	return nil
}

func (p *provider) setBasicAuthHeader(req *http.Request) {
	if p.password != "" {
		req.SetBasicAuth("opencode", p.password)
	}
}

func (p *provider) buildEnv() []string {
	parent := os.Environ()
	overrides := make(map[string]string, len(p.env)+1)
	for k, v := range p.env {
		overrides[k] = v
	}
	if p.password != "" {
		overrides["OPENCODE_SERVER_PASSWORD"] = p.password
	}
	env := make([]string, 0, len(parent)+len(overrides))
	for _, kv := range parent {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			env = append(env, kv)
			continue
		}
		if _, ok := overrides[kv[:eq]]; ok {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

// detectPort reads stderr lines looking for the server's listen address.
// OpenCode logs "Listening on http://127.0.0.1:<port>" or similar.
// r must be an io.ReadCloser (e.g. from cmd.StderrPipe); it is closed on
// timeout to unblock the reader goroutine.
func detectPort(r io.ReadCloser, timeout time.Duration) (int, error) {
	type result struct {
		port int
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, 4096)
		var accumulated string
		for {
			n, err := r.Read(buf)
			if n > 0 {
				accumulated += string(buf[:n])
				// Look for port patterns.
				lines := strings.Split(accumulated, "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if port := extractPort(line); port > 0 {
						ch <- result{port: port}
						return
					}
				}
			}
			if err != nil {
				ch <- result{err: fmt.Errorf("server exited without reporting port: %w", err)}
				return
			}
		}
	}()

	select {
	case r := <-ch:
		return r.port, r.err
	case <-time.After(timeout):
		_ = r.Close() // unblocks the Read, goroutine exits
		return 0, fmt.Errorf("timeout waiting for server port")
	}
}

// extractPort tries to find a port number from common server log patterns.
func extractPort(line string) int {
	// Pattern: "Listening on http://127.0.0.1:4096" or "http://[::]:4096"
	for _, prefix := range []string{"Listening on ", "listening on ", "Serving on "} {
		if idx := strings.Index(line, prefix); idx >= 0 {
			rest := line[idx+len(prefix):]
			// Find the port after the last colon.
			if colonIdx := strings.LastIndex(rest, ":"); colonIdx >= 0 {
				portStr := strings.TrimRight(rest[colonIdx+1:], " .")
				var port int
				if _, err := fmt.Sscanf(portStr, "%d", &port); err == nil && port > 0 && port < 65536 {
					return port
				}
			}
		}
	}
	return 0
}

// --- Session ---

type session struct {
	provider   *provider
	workingDir string
	model      string
	sysMessage string
	runID      string
}

func (s *session) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	// Create a session on the server.
	sessionID, err := s.createSession(ctx)
	if err != nil {
		return "", fmt.Errorf("opencode-server: create session: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.deleteSession(cleanupCtx, sessionID)
	}()

	// Build the message with system message prepended.
	text := chat.PrependMarker(prompt, s.runID)
	if s.sysMessage != "" {
		text = s.sysMessage + "\n\n" + text
	}

	// Send the message.
	resp, err := s.sendMessage(ctx, sessionID, text, timeout)
	if err != nil {
		return "", fmt.Errorf("opencode-server: send message: %w", err)
	}
	return resp, nil
}

func (s *session) Close() error { return nil }

// createSession creates a new session on the server and returns its ID.
func (s *session) createSession(ctx context.Context) (string, error) {
	body := strings.NewReader(`{}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.provider.baseURL+"/session", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	s.provider.setBasicAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create session: status %d: %s", resp.StatusCode, string(b))
	}

	var responseBody struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&responseBody); err != nil {
		return "", fmt.Errorf("decode session response: %w", err)
	}
	if responseBody.ID == "" {
		return "", errors.New("server returned empty session id")
	}
	return responseBody.ID, nil
}

// sendMessage sends a prompt and waits for the full response.
func (s *session) sendMessage(ctx context.Context, sessionID, text string, timeout time.Duration) (string, error) {
	msgReq := messageRequest{
		Parts: []messagePart{{Type: "text", Text: text}},
		Model: s.model,
	}
	body, err := json.Marshal(msgReq)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/session/%s/message", s.provider.baseURL, sessionID)

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	s.provider.setBasicAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("send message: status %d: %s", resp.StatusCode, string(b))
	}

	var msgResp messageResponse
	if err := json.NewDecoder(resp.Body).Decode(&msgResp); err != nil {
		return "", fmt.Errorf("decode message response: %w", err)
	}

	// Extract text from response parts.
	var texts []string
	for _, p := range msgResp.Parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	if len(texts) == 0 {
		return "", errors.New("opencode-server: response contained no text parts")
	}
	return strings.Join(texts, "\n"), nil
}

// deleteSession cleans up a session.
func (s *session) deleteSession(ctx context.Context, sessionID string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("%s/session/%s", s.provider.baseURL, sessionID), nil)
	if err != nil {
		return
	}
	s.provider.setBasicAuthHeader(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// --- API types ---

type messagePart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type messageRequest struct {
	Parts []messagePart `json:"parts"`
	Model string        `json:"model,omitempty"`
}

type messageResponse struct {
	Info  json.RawMessage `json:"info"`
	Parts []messagePart   `json:"parts"`
}
