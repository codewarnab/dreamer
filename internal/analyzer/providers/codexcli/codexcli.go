// Package codexcli implements the codex-cli analyzer provider (v1.1).
// It shells out to `codex exec --json` for non-interactive analysis and
// parses the JSON-line event stream to extract the final assistant text.
package codexcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	analyzer "dreamer/internal/analyzer"
	"dreamer/internal/analyzer/transport"
)

const ID = "codex-cli"

// Options carries per-provider configuration from the YAML config.
type Options struct {
	Command []string          // override argv; default: ["codex", "exec", "--json", "--sandbox", "read-only"]
	Env     map[string]string // extra environment for the subprocess
	Model   string            // optional --model override; empty = codex default
}

func init() {
	analyzer.RegisterProvider(analyzer.ProviderCodexCLI, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(Options{
			Command: cfg.Command,
			Env:     cfg.Env,
			Model:   cfg.Model,
		})
	})
}

// New returns a codex-cli Provider.
func New(options Options) (analyzer.Provider, error) {
	command := append([]string(nil), options.Command...)
	if len(command) == 0 {
		command = []string{"codex", "exec", "--json", "--sandbox", "read-only"}
	}
	return &provider{options: options, command: command}, nil
}

type provider struct {
	options Options
	command []string
}

func (p *provider) ID() string { return ID }

// Start performs a lightweight executable-presence check; it does not spawn
// codex. Authentication is verified the first time Run is called.
func (p *provider) Start(ctx context.Context) error {
	if _, err := exec.LookPath(p.command[0]); err != nil {
		return fmt.Errorf("codex binary %q not found in PATH; install the OpenAI Codex CLI and run `codex login`", p.command[0])
	}
	return nil
}

func (p *provider) NewSession(ctx context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	wd := strings.TrimSpace(cfg.WorkingDirectory)
	if wd == "" {
		return nil, errors.New("codex-cli: SessionConfig.WorkingDirectory is required")
	}
	command := append([]string(nil), p.command...)
	command = append(command, "--cd", wd)
	if model := strings.TrimSpace(cfg.Model); model != "" {
		command = append(command, "--model", model)
	} else if model := strings.TrimSpace(p.options.Model); model != "" {
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

// codex exec has a 1048576-char hard cap on stdin, but the underlying model's
// context window is the binding constraint. 400 KB ≈ 100k tokens leaves room
// for system prompt + reasoning + completion within typical GPT-5 budgets.
const codexMaxInputBytes = 400_000

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
		return "", fmt.Errorf("codex-cli: open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return "", fmt.Errorf("codex-cli: open stdout: %w", err)
	}
	stderrBuf := new(strings.Builder)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("codex-cli: start codex: %w", err)
	}

	go func() {
		defer stdin.Close()
		body := prompt
		if s.systemMsg != "" {
			body = s.systemMsg + "\n\n" + prompt
		}
		body = transport.CapInputBytes(body, codexMaxInputBytes, "\n\n[transcript truncated to fit codex input cap]\n")
		if dump := os.Getenv("DREAMER_DUMP_CODEX_PROMPT"); dump != "" {
			_ = os.WriteFile(dump, []byte(body), 0o644)
		}
		_, _ = io.WriteString(stdin, body)
	}()

	final, parseErr := readStreamJSON(stdout)

	waitErr := cmd.Wait()
	// Prefer parseErr when set: it carries the JSON-event-level error
	// (usage limit, context window, turn.failed) that explains *why* codex
	// exited non-zero. waitErr alone gives only "exit status 1".
	if parseErr != nil {
		err := fmt.Errorf("codex-cli: %w (stderr: %s)", parseErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(parseErr.Error()) {
			err = errors.Join(analyzer.ErrRateLimited, err)
		}
		return "", err
	}
	if waitErr != nil {
		err := fmt.Errorf("codex-cli: process exited: %w (stderr: %s)", waitErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(stderrBuf.String()) {
			err = errors.Join(analyzer.ErrRateLimited, err)
		}
		return "", err
	}
	if final == "" {
		return "", fmt.Errorf("codex-cli: no assistant content emitted (stderr: %s)", strings.TrimSpace(stderrBuf.String()))
	}
	return final, nil
}

func (s *session) Close() error { return nil }

// readStreamJSON consumes `codex exec --json` output. Real codex-rs emits
// top-level events: thread.started, turn.started, item.completed (with
// item.type=agent_message + item.text), turn.completed, error, turn.failed.
// We collect text from agent_message item.completed events and surface
// turn.failed/error messages instead of swallowing them.
func readStreamJSON(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<16), 1<<24)
	var assembled strings.Builder
	var streamErr string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		switch event.Type {
		case "item.completed":
			if event.Item.Type == "agent_message" && event.Item.Text != "" {
				if assembled.Len() > 0 {
					assembled.WriteString("\n")
				}
				assembled.WriteString(event.Item.Text)
			}
		case "error":
			if event.Message != "" {
				streamErr = event.Message
			}
		case "turn.failed":
			if event.Error.Message != "" {
				streamErr = event.Error.Message
			} else if event.Message != "" {
				streamErr = event.Message
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if streamErr != "" && assembled.Len() == 0 {
		return "", fmt.Errorf("codex stream error: %s", streamErr)
	}
	return strings.TrimSpace(assembled.String()), nil
}
