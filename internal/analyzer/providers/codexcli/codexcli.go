// Package codexcli implements the codex-cli analyzer provider (v1.1).
// It shells out to `codex exec --json` for non-interactive analysis and
// parses the JSON-line event stream to extract the final assistant text.
package codexcli

import (
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
	"dreamer/internal/chat"
	"dreamer/internal/errs"
)

const ID = "codex-cli"

// Options carries per-provider configuration from the YAML config.
type Options struct {
	Command      []string          // override argv; default: ["codex", "exec", "--json", "--sandbox", "read-only"]
	Env          map[string]string // extra environment for the subprocess
	Model        string            // optional --model override; empty = codex default
	DefaultModel string            // per-provider default; applied when Model is empty
}

func init() {
	analyzer.RegisterProvider(analyzer.ProviderCodexCLI, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(Options{
			Command:      providerConfig.Command,
			Env:          providerConfig.Env,
			Model:        providerConfig.Model,
			DefaultModel: providerConfig.DefaultModel,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderCodexCLI,
		DisplayName: "OpenAI Codex CLI",
		Order:       90,
	})
}

// New returns a codex-cli Provider.
func New(options Options) (analyzer.Provider, error) {
	command := append([]string(nil), options.Command...)
	if len(command) == 0 {
		// Sandbox hardening:
		// --sandbox read-only: codex's built-in policy that rejects file writes
		// and network access for model-generated shell commands. `codex exec` is
		// already non-interactive (no approval prompts), so no --ask-for-approval
		// flag is needed; that flag belongs to the interactive `codex` command
		// and is rejected by `codex exec`.
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

func (p *provider) NewSession(ctx context.Context, sessionConfig analyzer.SessionConfig) (analyzer.Session, error) {
	wd := strings.TrimSpace(sessionConfig.WorkingDirectory)
	if wd == "" {
		return nil, errors.New("codex-cli: SessionConfig.WorkingDirectory is required")
	}
	command := append([]string(nil), p.command...)
	command = append(command, "--cd", wd)
	model := strings.TrimSpace(sessionConfig.Model)
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
		systemMsg:  strings.TrimSpace(sessionConfig.SystemMessage),
		runID:      sessionConfig.RunID,
	}, nil
}

func (p *provider) Close() error { return nil }

type session struct {
	command    []string
	env        map[string]string
	workingDir string
	systemMsg  string
	runID      string
}

// codex exec has a 1048576-char hard cap on stdin, but the underlying model's
// context window is the binding constraint. 400 KB ≈ 100k tokens leaves room
// for system prompt + reasoning + completion within typical GPT-5 budgets.
const codexMaxInputBytes = 400_000

func (s *session) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	if ctx == nil {
		return "", analyzer.ErrNilContext
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
		body := chat.PrependMarker(prompt, s.runID)
		if s.systemMsg != "" {
			body = s.systemMsg + "\n\n" + body
		}
		body = transport.CapInputBytes(body, codexMaxInputBytes, "\n\n[transcript truncated to fit codex input cap]\n")
		if dump := os.Getenv("DREAMER_DUMP_CODEX_PROMPT"); dump != "" {
			_ = os.WriteFile(dump, []byte(body), 0o644)
		}
		if _, writeErr := io.WriteString(stdin, body); writeErr != nil {
			stderrBuf.WriteString(fmt.Sprintf("[stdin write failed: %v]", writeErr))
		}
	}()

	final, parseErr := readStreamJSON(stdout)

	waitErr := cmd.Wait()
	// Prefer parseErr when set: it carries the JSON-event-level error
	// (usage limit, context window, turn.failed) that explains *why* codex
	// exited non-zero. waitErr alone gives only "exit status 1".
	if parseErr != nil {
		err := fmt.Errorf("codex-cli: %w (stderr: %s)", parseErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(parseErr.Error()) {
			return "", errs.RateLimit(ID, "session.run", 0, err)
		}
		return "", err
	}
	if waitErr != nil {
		err := fmt.Errorf("codex-cli: process exited: %w (stderr: %s)", waitErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(stderrBuf.String()) {
			return "", errs.RateLimit(ID, "session.run", 0, err)
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
	scanner := transport.NewScanner(r)
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
	if streamErr != "" {
		return "", fmt.Errorf("codex stream error: %s", streamErr)
	}
	return strings.TrimSpace(assembled.String()), nil
}
