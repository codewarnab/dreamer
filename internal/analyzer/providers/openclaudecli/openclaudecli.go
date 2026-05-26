package openclaudecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/flagutil"
	"dreamer/internal/analyzer/transport"
	"dreamer/internal/chat"
	"dreamer/internal/errs"
	"dreamer/internal/sandbox"
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
	usesDefaultCommand := len(command) == 0
	if len(command) == 0 {
		command = defaultCommand(sandbox.Available())
	}
	return &provider{options: options, command: command, usesDefaultCommand: usesDefaultCommand}, nil
}

type provider struct {
	options            Options
	command            []string
	usesDefaultCommand bool
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
	// Validate Phase2 config unconditionally when set, regardless of mode,
	// so misconfiguration is caught at the boundary rather than silently
	// producing broken runs.
	if sessionConfig.Phase2 != nil {
		if err := sessionConfig.Phase2.Validate(); err != nil {
			return nil, fmt.Errorf("openclaude-cli: phase 2 config: %w", err)
		}
	}
	sbMode, err := sandbox.ParseMode(sessionConfig.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("openclaude-cli: %w", err)
	}
	useNativeSandbox := sandbox.ShouldUseNative(sbMode)
	command := p.commandForMode(useNativeSandbox)
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
		var err error
		command, err = flagutil.InjectMCPFlags(command,
			sessionConfig.Phase2.MCP.ToolNames,
			sessionConfig.Phase2.MCP.ConfigFilePath,
			useNativeSandbox,
			nil, // already validated above
		)
		if err != nil {
			return nil, fmt.Errorf("openclaude-cli: phase 2 config: %w", err)
		}
	}

	// WritableDirs: temp + openclaude config home so the CLI can write
	// session state and cached data.
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("openclaude-cli: resolve home dir for sandbox writable paths: %w", err)
	}
	writable := []string{os.TempDir(), filepath.Join(home, ".openclaude")}

	return &session{
		command:    command,
		env:        p.options.Env,
		workingDir: wd,
		systemMsg:  strings.TrimSpace(sessionConfig.SystemMessage),
		runID:      sessionConfig.RunID,
		sandboxCfg: sandbox.Config{
			ProjectDir:   wd,
			WritableDirs: writable,
			Mode:         sbMode,
		},
	}, nil
}

func (p *provider) commandForMode(useNativeSandbox bool) []string {
	if p.usesDefaultCommand ||
		flagutil.EqualArgs(p.command, defaultCommand(true)) ||
		flagutil.EqualArgs(p.command, defaultCommand(false)) {
		return defaultCommand(useNativeSandbox)
	}
	return append([]string(nil), p.command...)
}

// defaultCommand returns the generated OpenClaude command for the current
// safety boundary. Unrestricted mode is used only when the native sandbox is
// active; otherwise the CLI's policy mode remains the write-protection layer.
func defaultCommand(useNativeSandbox bool) []string {
	if useNativeSandbox {
		return []string{
			"openclaude",
			"-p",
			"--verbose",
			"--output-format=stream-json",
			"--dangerously-skip-permissions",
			"--bare",
			"--no-session-persistence",
		}
	}
	return []string{
		"openclaude",
		"-p",
		"--verbose",
		"--output-format=stream-json",
		"--permission-mode",
		"plan",
		"--bare",
		"--no-session-persistence",
	}
}

func (p *provider) Close() error { return nil }

type session struct {
	command    []string
	env        map[string]string
	workingDir string
	systemMsg  string
	runID      string
	sandboxCfg sandbox.Config
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

	// Apply OS-level sandbox before starting the process.
	// prepareCleanup closes the restricted token after cmd.Wait().
	prepareCleanup, err := sandbox.Prepare(cmd, s.sandboxCfg)
	if err != nil {
		return "", fmt.Errorf("openclaude-cli: sandbox prepare: %w", err)
	}
	defer prepareCleanup()

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

	// Apply post-start sandbox constraints (Job Object on Windows).
	// postCleanup closes the job handle after cmd.Wait().
	postCleanup, err := sandbox.PostStartOrKill(cmd, s.sandboxCfg, stdin, stdout, ID)
	if err != nil {
		return "", err
	}
	defer postCleanup()

	go func() {
		defer stdin.Close()
		body := chat.PrependMarker(prompt, s.runID)
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
	var totalLines, parseErrors int
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		totalLines++
		var event streamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			parseErrors++
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
				errorMessages := event.Errors
				if len(errorMessages) == 0 {
					errorMessages = []string{"unknown error (" + event.Subtype + ")"}
				}
				resultErr = strings.Join(errorMessages, "; ")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read openclaude stream-json: %w", err)
	}
	if resultErr != "" {
		return "", fmt.Errorf("openclaude-cli: %s", resultErr)
	}
	if assistantText.Len() == 0 && resultText == "" && totalLines > 0 && parseErrors == totalLines {
		return "", fmt.Errorf("openclaude-cli: all %d output lines failed to parse (provider schema change?)", totalLines)
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
