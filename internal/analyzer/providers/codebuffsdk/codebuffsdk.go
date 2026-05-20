// Package codebuffsdk implements an analyzer.Provider that calls the
// Codebuff API (codebuff.com/api/v1/chat/completions) using the
// OpenAI-compatible chat completions format.
package codebuffsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/transport"
	"dreamer/internal/errs"
)

const (
	id             = "codebuff-sdk"
	defaultModel   = "claude-opus-4-7"
	defaultBaseURL = "https://codebuff.com/api/v1"
)

func init() {
	analyzer.RegisterProvider(analyzer.ProviderCodebuffSDK, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		model := cfg.Model
		if strings.TrimSpace(model) == "" {
			model = cfg.DefaultModel
		}
		return New(Options{
			BaseURL: cfg.BaseURL,
			Model:   model,
			APIKey:  resolveAPIKey(cfg),
		})
	})
}

// Options carries per-provider configuration.
type Options struct {
	// BaseURL is the Codebuff API endpoint. Default: "https://codebuff.com/api/v1".
	BaseURL string

	// Model is the default model for analysis sessions.
	Model string

	// APIKey is the Codebuff API key for Bearer auth.
	APIKey string
}

// resolveAPIKey extracts the API key from ProviderConfig.
// It checks the APIKeyEnv environment variable first, then Password.
func resolveAPIKey(cfg analyzer.ProviderConfig) string {
	if envName := strings.TrimSpace(cfg.APIKeyEnv); envName != "" {
		if key := strings.TrimSpace(os.Getenv(envName)); key != "" {
			return key
		}
	}
	if key := strings.TrimSpace(cfg.Password); key != "" {
		return key
	}
	return ""
}

// New builds a codebuff-sdk Provider.
func New(options Options) (analyzer.Provider, error) {
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey == "" {
		return nil, errors.New("codebuff-sdk: API key is required (set providers.codebuff-sdk.password or providers.codebuff-sdk.api_key_env)")
	}

	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = defaultModel
	}

	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return &provider{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
	}, nil
}

type provider struct {
	baseURL string
	model   string
	apiKey  string

	mu      sync.Mutex
	started bool
	closed  bool
}

func (p *provider) ID() string { return id }

func (p *provider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return nil
	}

	if err := p.healthCheck(ctx); err != nil {
		return errs.ProviderUnavailable(id, "start", fmt.Errorf(
			"cannot reach Codebuff API at %s: %w. Check your API key and network connectivity.", p.baseURL, err))
	}
	p.started = true
	return nil
}

func (p *provider) NewSession(_ context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if !started {
		return nil, errors.New("codebuff-sdk: provider not started")
	}

	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = p.model
	}

	return &session{
		provider:   p,
		workingDir: cfg.WorkingDirectory,
		model:      model,
		sysMessage: cfg.SystemMessage,
	}, nil
}

func (p *provider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func (p *provider) healthCheck(ctx context.Context) error {
	// Reachability check only. Codebuff may route 405 before auth, so a 200/405
	// here doesn't prove the API key works — Run() surfaces the auth failure
	// on first use with a clearer error. We still fail-fast on explicit
	// 401/403 in case the server's auth path runs ahead of routing.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/chat/completions", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("authentication failed (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// --- Session ---

type session struct {
	provider   *provider
	workingDir string
	model      string
	sysMessage string
}

func (s *session) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	messages := s.buildMessages(prompt)

	body, err := json.Marshal(chatCompletionRequest{
		Model:    s.model,
		Messages: messages,
	})
	if err != nil {
		return "", fmt.Errorf("codebuff-sdk: marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	url := s.provider.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("codebuff-sdk: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.provider.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("codebuff-sdk: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		msg := string(b)
		if transport.IsRateLimitMessage(msg) {
			return "", errs.RateLimit(id, "session/prompt", 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg))
		}
		return "", fmt.Errorf("codebuff-sdk: HTTP %d: %s", resp.StatusCode, msg)
	}

	var result chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("codebuff-sdk: decode response: %w", err)
	}

	return extractResponseText(result)
}

func (s *session) Close() error { return nil }

// buildMessages constructs the OpenAI-format messages array.
func (s *session) buildMessages(prompt string) []chatMessage {
	messages := make([]chatMessage, 0, 3)

	if s.sysMessage != "" {
		messages = append(messages, chatMessage{
			Role:    "system",
			Content: s.sysMessage,
		})
	}

	messages = append(messages, chatMessage{
		Role:    "user",
		Content: prompt,
	})

	return messages
}

// extractResponseText pulls text content from the first choice.
func extractResponseText(result chatCompletionResponse) (string, error) {
	if len(result.Choices) == 0 {
		return "", errors.New("codebuff-sdk: response contained no choices")
	}
	choice := result.Choices[0]
	if choice.Message.Content == "" {
		return "", errors.New("codebuff-sdk: response choice had empty content")
	}
	return choice.Message.Content, nil
}

// --- API types ---

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatCompletionResponse struct {
	Choices []choice `json:"choices"`
}

type choice struct {
	Message chatMessage `json:"message"`
}
