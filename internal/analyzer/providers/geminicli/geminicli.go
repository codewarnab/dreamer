package geminicli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/transport"
	"dreamer/internal/errs"
)

const ID = "gemini-cli"

// Options is the per-provider configuration carried over from the YAML config.
type Options struct {
	Command      []string
	Env          map[string]string
	Model        string
	DefaultModel string
}

func init() {
	analyzer.RegisterProvider(analyzer.ProviderGeminiCLI, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(Options{
			Command:      cfg.Command,
			Env:          cfg.Env,
			Model:        cfg.Model,
			DefaultModel: cfg.DefaultModel,
		})
	})
}

// New returns a gemini-cli Provider that shells out to the `gemini` binary
// using the stream-json output format in headless mode.
func New(options Options) (analyzer.Provider, error) {
	command := append([]string(nil), options.Command...)
	if len(command) == 0 {
		command = []string{"gemini", "-p", "--output-format=stream-json", "--approval-mode=plan"}
	}
	return &provider{options: options, command: command}, nil
}

type provider struct {
	options Options
	command []string
}

func (p *provider) ID() string { return ID }

// Start performs a lightweight executable-presence check; it does not spawn
// gemini. Authentication is checked the first time Run is called.
func (p *provider) Start(ctx context.Context) error {
	if _, err := exec.LookPath(p.command[0]); err != nil {
		return errs.NotInstalled("gemini-cli", "start",
			"Install Gemini CLI (`npm i -g @anthropic-ai/gemini-cli` or `brew install gemini-cli`) and run `gemini auth login`.", err)
	}
	return nil
}

func (p *provider) NewSession(ctx context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	wd := strings.TrimSpace(cfg.WorkingDirectory)
	if wd == "" {
		return nil, errors.New("gemini-cli: SessionConfig.WorkingDirectory is required")
	}
	command := append([]string(nil), p.command...)
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = strings.TrimSpace(p.options.Model)
	}
	if model == "" {
		model = strings.TrimSpace(p.options.DefaultModel)
	}
	if model != "" {
		command = append(command, "--model", model)
	}
	return &session{
		command:    command,
		env:        p.options.Env,
		workingDir: wd,
		systemMsg:  strings.TrimSpace(cfg.SystemMessage),
	}, nil
}

func (p *provider) Close() error { return nil }

type session struct {
	command    []string
	env        map[string]string
	workingDir string
	systemMsg  string
}

func (s *session) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, s.command[0], s.command[1:]...)
	cmd.Dir = s.workingDir
	cmd.Env = transport.MergeWithProcessEnv(s.env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("gemini-cli: open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return "", fmt.Errorf("gemini-cli: open stdout: %w", err)
	}
	stderrBuf := new(strings.Builder)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("gemini-cli: start gemini: %w", err)
	}

	go func() {
		defer stdin.Close()
		body := prompt
		if s.systemMsg != "" {
			body = s.systemMsg + "\n\n" + prompt
		}
		_, _ = io.WriteString(stdin, body)
	}()

	final, parseErr := readStreamJSON(stdout)

	waitErr := cmd.Wait()
	if parseErr != nil {
		err := fmt.Errorf("gemini-cli: %w (stderr: %s)", parseErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(parseErr.Error()) || transport.IsRateLimitMessage(stderrBuf.String()) {
			return "", errs.RateLimit(ID, "session.run", 0, err)
		}
		return "", err
	}
	if waitErr != nil {
		err := fmt.Errorf("gemini-cli: process exited: %w (stderr: %s)", waitErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(stderrBuf.String()) {
			return "", errs.RateLimit(ID, "session.run", 0, err)
		}
		return "", err
	}
	if final == "" {
		return "", fmt.Errorf("gemini-cli: no assistant content emitted (stderr: %s)", strings.TrimSpace(stderrBuf.String()))
	}
	return final, nil
}

func (s *session) Close() error { return nil }

// readStreamJSON consumes the stream-json output from gemini --output-format=stream-json
// and returns the final text. It prefers the `response` field from a result event,
// falling back to concatenated message text. Error events are surfaced as Go errors.
func readStreamJSON(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<16), 1<<24)

	var assistantText strings.Builder
	var resultText string
	var resultErr string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event geminiStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			log.Printf("gemini-cli: malformed stream-json line: %v", err)
			continue
		}
		switch event.Type {
		case "message":
			if event.Role == "assistant" || event.Role == "" {
				assistantText.WriteString(event.Content)
			}
		case "result":
			resultText = event.Response
		case "error":
			msg := event.Message
			if msg == "" {
				msg = "unknown error"
			}
			resultErr = msg
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read gemini stream-json: %w", err)
	}
	if resultErr != "" {
		return "", fmt.Errorf("gemini-cli: %s", resultErr)
	}
	if resultText != "" {
		return strings.TrimSpace(resultText), nil
	}
	return strings.TrimSpace(assistantText.String()), nil
}

// geminiStreamEvent is the shape of a single NDJSON line emitted by
// gemini --output-format=stream-json in headless mode.
type geminiStreamEvent struct {
	Type     string `json:"type"`
	Role     string `json:"role"`
	Content  string `json:"content"`
	Response string `json:"response"`
	Message  string `json:"message"`
}
