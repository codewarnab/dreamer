package openclaudecli

import (
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
	"dreamer/internal/analyzer/providers/flagutil"
	"dreamer/internal/analyzer/transport"
	"dreamer/internal/chat"
	"dreamer/internal/errs"
)

const ID = "openclaude-cli"

// Options is the per-provider configuration carried over from the YAML config.
type Options struct {
	Command      []string
	Env          map[string]string
	Model        string
	DefaultModel string
}

// New returns an openclaude-cli Provider that shells out to the `openclaude`
// binary using the stream-json output format.
func New(options Options) (analyzer.Provider, error) {
	command := append([]string(nil), options.Command...)
	if len(command) == 0 {
		// Defense-in-depth (NOT a true sandbox — policy-only, no isolation):
		// --permission-mode plan: permission layer rejects write tool calls.
		// --tools "Read,Grep,Glob": narrows the top-level model's tool list to
		//   read-only built-ins. Skills/subagents (if any) keep their own grants.
		// --bare: skip hook, skill, plugin, auto-memory, CLAUDE.md, and MCP
		//   auto-discovery.
		// Note: --strict-mcp-config is a boolean flag (no value). The previous
		// "--strict-mcp-config {}" form caused '{}' to be consumed as the
		// positional prompt argument. Rely on --bare for MCP-off.
		command = []string{"openclaude", "-p", "--verbose", "--output-format=stream-json", "--permission-mode", "plan", "--tools", "Read,Grep,Glob", "--bare", "--no-session-persistence"}
	}
	return &provider{options: options, command: command}, nil
}

type provider struct {
	options Options
	command []string
}

func (p *provider) ID() string { return ID }

// Start performs a lightweight executable-presence check; it does not spawn
// openclaude. Authentication is checked the first time Run is called.
func (p *provider) Start(ctx context.Context) error {
	if _, err := exec.LookPath(p.command[0]); err != nil {
		return errs.NotInstalled("openclaude", "start",
			"Install OpenClaude (`npm i -g @gitlawb/openclaude`) and run `openclaude`, then `/provider` for guided provider setup.", err)
	}
	return nil
}

func (p *provider) NewSession(ctx context.Context, sessionConfig analyzer.SessionConfig) (analyzer.Session, error) {
	wd := strings.TrimSpace(sessionConfig.WorkingDirectory)
	if wd == "" {
		return nil, errors.New("openclaude-cli: SessionConfig.WorkingDirectory is required")
	}
	command := append([]string(nil), p.command...)
	command = append(command, "--add-dir", wd)
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

	// MCP mode: inject --mcp-config, --allowed-tools, and relax permission-mode.
	if sessionConfig.Phase2.Mode() == analyzer.Phase2ModeMCP {
		if err := sessionConfig.Phase2.Validate(); err != nil {
			return nil, fmt.Errorf("openclaude-cli: phase 2 config: %w", err)
		}
		toolList := strings.Join(sessionConfig.Phase2.MCP.ToolNames, ",")
		command = flagutil.ReplaceFlag(command, "--permission-mode", "plan", "default")
		if toolList != "" {
			command = flagutil.AppendToFlag(command, "--tools", toolList)
			command = append(command, "--allowed-tools", toolList)
		}
		command = append(command, "--mcp-config", sessionConfig.Phase2.MCP.ConfigJSON)
	}

	return &session{
		command:    command,
		env:        p.options.Env,
		workingDir: wd,
		systemMsg:  strings.TrimSpace(sessionConfig.SystemMessage),
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
		return "", fmt.Errorf("openclaude-cli: open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return "", fmt.Errorf("openclaude-cli: open stdout: %w", err)
	}
	stderrBuf := new(strings.Builder)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return "", errs.ProviderUnavailable(ID, "session.run",
			fmt.Errorf("openclaude-cli: start openclaude: %w", err))
	}

	go func() {
		defer stdin.Close()
		body := chat.PrependMarker(prompt)
		if s.systemMsg != "" {
			body = s.systemMsg + "\n\n" + body
		}
		if _, writeErr := io.WriteString(stdin, body); writeErr != nil {
			stderrBuf.WriteString(fmt.Sprintf("[stdin write failed: %v]", writeErr))
		}
	}()

	final, parseErr := readStreamJSON(stdout)

	waitErr := cmd.Wait()
	// Prefer parseErr when set: it carries the JSON-event-level error
	// (usage limit, context window, turn.failed) that explains *why*
	// openclaude exited non-zero. waitErr alone gives only "exit status 1".
	if parseErr != nil {
		err := fmt.Errorf("openclaude-cli: %w (stderr: %s)", parseErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(parseErr.Error()) || transport.IsRateLimitMessage(stderrBuf.String()) {
			return "", errs.RateLimit(ID, "session.run", 0, err)
		}
		return "", err
	}
	if waitErr != nil {
		err := fmt.Errorf("openclaude-cli: process exited: %w (stderr: %s)", waitErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(stderrBuf.String()) {
			return "", errs.RateLimit(ID, "session.run", 0, err)
		}
		return "", err
	}
	if final == "" {
		return "", fmt.Errorf("openclaude-cli: no assistant content emitted (stderr: %s)", strings.TrimSpace(stderrBuf.String()))
	}
	return final, nil
}

func (s *session) Close() error { return nil }

// readStreamJSON consumes the stream-json output and returns the final text.
// It prefers the `result` field from a success result event, falling back to
// concatenated assistant message text. Error results and assistant-level API
// errors (rate_limit, auth_failed, etc.) are surfaced as Go errors.
func readStreamJSON(r io.Reader) (string, error) {
	scanner := transport.NewScanner(r)

	var assistantText strings.Builder
	var resultText string
	var resultErr string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event streamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			log.Printf("openclaude-cli: malformed stream-json line: %v", err)
			continue
		}
		switch event.Type {
		case "assistant":
			for _, item := range event.Message.Content {
				if item.Type == "text" {
					assistantText.WriteString(item.Text)
				}
			}
			// Assistant messages may carry an API-level error (rate limit,
			// auth failure, billing, etc.). Surface it immediately.
			if event.Error != "" {
				resultErr = "api error: " + event.Error
			}
		case "result":
			switch event.Subtype {
			case "success":
				resultText = event.Result
			default:
				// error_during_execution, error_max_turns,
				// error_max_budget_usd, error_max_structured_output_retries
				msgs := event.Errors
				if len(msgs) == 0 {
					msgs = []string{"unknown error (" + event.Subtype + ")"}
				}
				resultErr = strings.Join(msgs, "; ")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read openclaude stream-json: %w", err)
	}
	if resultErr != "" {
		return "", fmt.Errorf("openclaude-cli: %s", resultErr)
	}
	// Prefer the result event's text (complete, post-processing); fall back
	// to concatenated assistant messages for providers that omit result events.
	if resultText != "" {
		return strings.TrimSpace(resultText), nil
	}
	return strings.TrimSpace(assistantText.String()), nil
}

// streamEvent is the shape of a single NDJSON line emitted by openclaude
// --output-format=stream-json --verbose.
type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"`
	Error   string `json:"error"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	Errors []string `json:"errors"`
}
