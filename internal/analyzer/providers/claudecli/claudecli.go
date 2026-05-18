package claudecli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/transport"
	"dreamer/internal/errs"
)

const ID = "claude-cli"

// Options is the per-provider configuration carried over from the YAML config.
type Options struct {
	Command []string
	Env     map[string]string
	Model   string
}

// New returns a claude-cli Provider that shells out to the `claude` binary
// using the stream-json output format (spec §18).
func New(options Options) (analyzer.Provider, error) {
	command := append([]string(nil), options.Command...)
	if len(command) == 0 {
		command = []string{"claude", "-p", "--verbose", "--output-format=stream-json", "--permission-mode", "plan"}
	}
	return &provider{options: options, command: command}, nil
}

type provider struct {
	options Options
	command []string
}

func (p *provider) ID() string { return ID }

// Start performs a lightweight executable-presence check; it does not spawn
// claude. Authentication is checked the first time Run is called.
func (p *provider) Start(ctx context.Context) error {
	if _, err := exec.LookPath(p.command[0]); err != nil {
		return fmt.Errorf("claude binary %q not found in PATH; install the Claude CLI and run `claude auth login`", p.command[0])
	}
	return nil
}

func (p *provider) NewSession(ctx context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	wd := strings.TrimSpace(cfg.WorkingDirectory)
	if wd == "" {
		return nil, errors.New("claude-cli: SessionConfig.WorkingDirectory is required")
	}
	command := append([]string(nil), p.command...)
	command = append(command, "--add-dir", wd)
	if model := strings.TrimSpace(cfg.Model); model != "" {
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
		return "", fmt.Errorf("claude-cli: open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return "", fmt.Errorf("claude-cli: open stdout: %w", err)
	}
	stderrBuf := new(strings.Builder)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("claude-cli: start claude: %w", err)
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
	if waitErr != nil {
		err := fmt.Errorf("claude-cli: process exited: %w (stderr: %s)", waitErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(stderrBuf.String()) {
			return "", errs.RateLimit(ID, "session.run", 0, err)
		}
		return "", err
	}
	if parseErr != nil {
		err := fmt.Errorf("claude-cli: parse stream-json: %w (stderr: %s)", parseErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(parseErr.Error()) || transport.IsRateLimitMessage(stderrBuf.String()) {
			return "", errs.RateLimit(ID, "session.run", 0, err)
		}
		return "", err
	}
	if final == "" {
		return "", fmt.Errorf("claude-cli: no assistant content emitted (stderr: %s)", strings.TrimSpace(stderrBuf.String()))
	}
	return final, nil
}

func (s *session) Close() error { return nil }

// readStreamJSON consumes the stream-json output and returns the concatenated
// text of the final assistant message.
func readStreamJSON(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<16), 1<<24)
	var assistantText strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event streamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Type == "assistant" {
			assistantText.Reset()
			for _, item := range event.Message.Content {
				if item.Type == "text" {
					assistantText.WriteString(item.Text)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.TrimSpace(assistantText.String()), nil
}

type streamEvent struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}
