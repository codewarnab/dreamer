package claudecli

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

const ID = "claude-cli"

// Options is the per-provider configuration carried over from the YAML config.
type Options struct {
	Command      []string
	Env          map[string]string
	Model        string
	DefaultModel string
}

// New returns a claude-cli Provider that shells out to the `claude` binary
// using the stream-json output format (spec §18).
func New(options Options) (analyzer.Provider, error) {
	command := append([]string(nil), options.Command...)
	if len(command) == 0 {
		// Unrestricted flags — the OS sandbox (ACLs + Job Objects) is the
		// actual enforcement layer. Policy-only flags (--permission-mode plan,
		// --tools read-only) are removed because the kernel blocks writes to
		// the project directory regardless.
		command = []string{
			"claude",
			"-p",
			"--verbose",
			"--output-format=stream-json",
			"--dangerously-skip-permissions",
			"--bare",
			"--no-session-persistence",
		}
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
		return fmt.Errorf("claude binary %q not found in PATH; install Claude Code (`npm i -g @anthropic-ai/claude-code`) and run `claude` to authenticate", p.command[0])
	}
	return nil
}

func (p *provider) NewSession(ctx context.Context, sessionConfig analyzer.SessionConfig) (analyzer.Session, error) {
	wd := strings.TrimSpace(sessionConfig.WorkingDirectory)
	if wd == "" {
		return nil, errors.New("claude-cli: SessionConfig.WorkingDirectory is required")
	}
	// Validate Phase2 config unconditionally when set, regardless of mode,
	// so misconfiguration is caught at the boundary rather than silently
	// producing broken runs.
	if sessionConfig.Phase2 != nil {
		if err := sessionConfig.Phase2.Validate(); err != nil {
			return nil, fmt.Errorf("claude-cli: phase 2 config: %w", err)
		}
	}
	command := append([]string(nil), p.command...)
	command = append(command, "--add-dir", wd)
	model := strings.TrimSpace(sessionConfig.Model)
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
			nil, // already validated above
		)
		if err != nil {
			return nil, fmt.Errorf("claude-cli: phase 2 config: %w", err)
		}
	}

	// Resolve sandbox mode.
	sbMode, err := sandbox.ParseMode(sessionConfig.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("claude-cli: %w", err)
	}

	// WritableDirs: temp + claude config home so the CLI can write
	// session state, auth tokens, and cached data.
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("claude-cli: resolve home dir for sandbox writable paths: %w", err)
	}
	writable := []string{os.TempDir(), filepath.Join(home, ".claude")}

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
		return "", fmt.Errorf("claude-cli: sandbox prepare: %w", err)
	}
	defer prepareCleanup()

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
	scanner := transport.NewScanner(r)
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
