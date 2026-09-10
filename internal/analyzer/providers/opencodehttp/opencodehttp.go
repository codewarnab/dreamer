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
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/chat"
	"dreamer/internal/errs"
	"dreamer/internal/procutil"
)

const ID = "opencode-server"

const (
	// portDetectTimeout is how long to wait for the auto-started server
	// to log its listen address before giving up and killing the process.
	portDetectTimeout = 10 * time.Second

	// maxCapturedServerOutput bounds how much auto-start server output
	// (stdout + stderr) is retained while detecting the listen port. The
	// retained tail is quoted in detection-failure errors for diagnostics.
	maxCapturedServerOutput = 4096

	// modelListTimeout caps the GET /api/providers request used to enumerate
	// available models. Short because this is called in the UI hot path.
	modelListTimeout = 3 * time.Second

	// sessionCleanupTimeout caps the DELETE /session/{id} call in the
	// deferred cleanup after each Run. Generous enough for a graceful
	// server-side delete without blocking the caller for long.
	sessionCleanupTimeout = 5 * time.Second

	// modelListBodyCap bounds the body read on GET /api/providers to prevent
	// an unexpectedly large payload from exhausting memory. Orders of
	// magnitude above any real model list.
	modelListBodyCap = 512 * 1024
)

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
		Capabilities: analyzer.ProviderCapabilities{
			RequiresNetwork: true,
		},
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

	mu          sync.Mutex
	started     bool
	closed      bool
	cmd         *exec.Cmd // non-nil when auto-started
	stopStreams func()    // non-nil when auto-started; closes output pipes, reaps drainers
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
	exePath, err := exec.LookPath(p.command[0])
	if err != nil {
		return errs.NotInstalled("opencode-server", "start",
			"Install OpenCode (see https://opencode.ai) and ensure `opencode` is in PATH.", err)
	}

	args := append([]string(nil), p.command[1:]...)
	args = append(args, "--port", "0") // random port
	// On Windows, npm installs put .cmd/.ps1 shims on PATH instead of a
	// native exe; resolve those to the real binary before spawning.
	spawnExe := exePath
	if runtime.GOOS == "windows" {
		if real, ok := shimReplacement(exePath); ok {
			spawnExe = real
		}
	}
	// Detach from Start ctx: pipeline cancels startCtx after Start returns,
	// which would SIGKILL the server before any session runs.
	cmd := exec.Command(spawnExe, args...)
	cmd.Env = p.buildEnv()
	// Match acpcore/cliharness: avoid a console flash when the server is
	// auto-started from a detached daemon or background job on Windows.
	procutil.SetNoWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("opencode-server: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return fmt.Errorf("opencode-server: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opencode-server: start: %w", err)
	}
	p.cmd = cmd

	// Scan stdout and stderr for the listen address. Current OpenCode
	// versions log it on stdout; older builds used stderr.
	port, stopStreams, err := detectPort(stdout, stderr, portDetectTimeout)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("opencode-server: detect port: %w", err)
	}
	// stopStreams keeps draining both streams for the server's lifetime.
	// The server keeps running after detection; closing the pipes now
	// would cause EPIPE on the next write, killing it. The drainers run
	// until provider.Close() calls stopStreams after killing the process.
	p.stopStreams = stopStreams
	p.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	// Health-check the newly started server.
	if err := p.healthCheck(ctx); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		stopStreams() // clean up drainer goroutines
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
	// Close the output pipes and wait for the drainer goroutines to exit.
	if p.stopStreams != nil {
		p.stopStreams()
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

// serverOutput keeps a bounded tail of combined child-process output so
// startup failures can quote what the server actually printed. Safe for
// concurrent use.
type serverOutput struct {
	mu  sync.Mutex
	buf []byte
}

// write appends p, discarding oldest bytes beyond maxCapturedServerOutput.
func (o *serverOutput) write(p []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf = append(o.buf, p...)
	if excess := len(o.buf) - maxCapturedServerOutput; excess > 0 {
		o.buf = append(o.buf[:0], o.buf[excess:]...)
	}
}

// tail returns everything currently captured.
func (o *serverOutput) tail() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.buf)
}

// detectPort concurrently scans the server's stdout and stderr streams for
// its listen address ("listening on http://127.0.0.1:<port>" or similar).
// Current OpenCode versions log the address on stdout; older builds logged
// on stderr, so both streams are scanned and the first match wins.
//
// While scanning, all output is captured into a bounded buffer. On failure,
// the returned error includes a truncated tail of that output so log-format
// drift stays diagnosable from the error alone.
//
// Ownership: on success, detectPort returns a non-nil stop function owning
// both readers — the scanner goroutines keep draining their streams (which
// prevents EPIPE kills on later server writes) until stop is called, e.g.
// from provider.Close(). On failure, both streams are already closed and
// reaped; callers must not reuse them (double-Close on *os.File is
// harmless).
func detectPort(stdout, stderr io.ReadCloser, timeout time.Duration) (int, func(), error) {
	readers := []io.ReadCloser{stdout, stderr}
	found := make(chan int, len(readers)) // one slot per scanner; sends never block
	allEnded := make(chan struct{})       // closed when every stream ended without a port

	var (
		wg        sync.WaitGroup
		endMu     sync.Mutex
		remaining = len(readers)
	)
	streamEnded := func() {
		endMu.Lock()
		defer endMu.Unlock()
		remaining--
		if remaining == 0 {
			close(allEnded)
		}
	}

	capture := &serverOutput{}
	for _, r := range readers {
		wg.Add(1)
		go func(r io.ReadCloser) {
			defer wg.Done()
			buf := make([]byte, 4096)
			var pending []byte // line fragment carried over from the last read
			portSent := false  // once matched, this goroutine only drains
			for {
				n, readErr := r.Read(buf)
				if !portSent && n > 0 {
					capture.write(buf[:n])
					pending = append(pending, buf[:n]...)
					for {
						nl := bytes.IndexByte(pending, '\n')
						if nl < 0 {
							break
						}
						line := strings.TrimSpace(string(pending[:nl]))
						pending = pending[nl+1:]
						if port := extractPort(line); port > 0 {
							found <- port
							portSent = true
							break
						}
					}
				}
				if readErr != nil {
					streamEnded()
					return
				}
			}
		}(r)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case port := <-found:
		var stopOnce sync.Once
		stop := func() {
			stopOnce.Do(func() {
				for _, r := range readers {
					_ = r.Close() // unblocks pending Reads
				}
				wg.Wait()
			})
		}
		return port, stop, nil
	case <-allEnded:
		return 0, nil, fmt.Errorf("server exited without reporting port; server output: %q", capture.tail())
	case <-timer.C:
		for _, r := range readers {
			_ = r.Close() // unblocks pending Reads so scanners exit
		}
		wg.Wait()
		return 0, nil, fmt.Errorf("timeout waiting for server port after %s; server output: %q",
			timeout, capture.tail())
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
		cleanupCtx, cancel := context.WithTimeout(context.Background(), sessionCleanupTimeout)
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
		Model: parseModelSpec(s.model),
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

type modelSpec struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

type messageRequest struct {
	Parts []messagePart `json:"parts"`
	Model any           `json:"model,omitempty"`
}

func parseModelSpec(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return modelSpec{
			ProviderID: parts[0],
			ModelID:    parts[1],
		}
	}
	return modelSpec{
		ProviderID: "opencode",
		ModelID:    trimmed,
	}
}

type messageResponse struct {
	Info  json.RawMessage `json:"info"`
	Parts []messagePart   `json:"parts"`
}

// --- ModelLister ---

// opencodeProviderEntry is the wire shape of one entry in GET /api/providers.
// Only id and models are read; all other fields are ignored.
type opencodeProviderEntry struct {
	ID     string                     `json:"id"`
	Models map[string]json.RawMessage `json:"models"` // values not inspected
}

// ListModels satisfies analyzer.ModelLister. It queries the running OpenCode
// server for its advertised model list and returns the deduplicated, sorted IDs.
//
// Requires: p.Start() has been called successfully (p.started == true).
// On any error the caller (ProviderMeta handler) falls back to defaults.go AllModels;
// this method never returns a partial list alongside a non-nil error.
func (p *provider) ListModels(ctx context.Context) ([]string, error) {
	p.mu.Lock()
	started := p.started
	baseURL := p.baseURL
	p.mu.Unlock()

	if !started {
		return nil, errors.New("opencode-server: provider not started; cannot list models")
	}

	reqCtx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/api/providers", nil)
	if err != nil {
		return nil, fmt.Errorf("opencode-server: list models: build request: %w", err)
	}
	p.setBasicAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode-server: list models: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode-server: list models: status %d", resp.StatusCode)
	}

	// Cap response body to modelListBodyCap — orders of magnitude above any real model list.
	data, err := io.ReadAll(io.LimitReader(resp.Body, modelListBodyCap))
	if err != nil {
		return nil, fmt.Errorf("opencode-server: list models: read body: %w", err)
	}

	return parseProviderModels(data)
}

// parseProviderModels is a pure function that extracts model IDs from the
// OpenCode GET /api/providers JSON payload. No I/O; directly testable.
// Returns a sorted, deduplicated slice of model IDs. An empty provider list
// yields an empty slice (not an error).
func parseProviderModels(data []byte) ([]string, error) {
	var entries []opencodeProviderEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parseProviderModels: unmarshal: %w", err)
	}

	seen := make(map[string]struct{})
	for _, entry := range entries {
		for modelID := range entry.Models {
			if modelID != "" {
				seen[modelID] = struct{}{}
			}
		}
	}

	models := make([]string, 0, len(seen))
	for id := range seen {
		models = append(models, id)
	}
	sort.Strings(models)
	return models, nil
}
