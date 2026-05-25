package geminicli

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

const ID = "gemini-cli"

// Options is the per-provider configuration carried over from the YAML config.
type Options struct {
	Command      []string
	Env          map[string]string
	Model        string
	DefaultModel string
}

func init() {
	analyzer.RegisterProvider(analyzer.ProviderGeminiCLI, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(Options{
			Command:      providerConfig.Command,
			Env:          providerConfig.Env,
			Model:        providerConfig.Model,
			DefaultModel: providerConfig.DefaultModel,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderGeminiCLI,
		DisplayName: "Google Gemini CLI",
		Order:       60,
		Phase2Mode:  analyzer.Phase2ModeCLI,
	})
}

// New returns a gemini-cli Provider that shells out to the `gemini` binary
// using the stream-json output format in headless mode.
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
// gemini. Authentication is checked the first time Run is called.
func (p *provider) Start(ctx context.Context) error {
	if _, err := exec.LookPath(p.command[0]); err != nil {
		return errs.NotInstalled("gemini-cli", "start",
			"Install Gemini CLI (`npm i -g @google/gemini-cli` or `brew install gemini-cli`) and run `gemini` to authenticate (browser OAuth on first launch).", err)
	}
	return nil
}

func (p *provider) NewSession(ctx context.Context, sessionConfig analyzer.SessionConfig) (analyzer.Session, error) {
	wd := strings.TrimSpace(sessionConfig.WorkingDirectory)
	if wd == "" {
		return nil, errors.New("gemini-cli: SessionConfig.WorkingDirectory is required")
	}
	// Validate Phase2 config unconditionally when set, regardless of mode,
	// so misconfiguration is caught at the boundary.
	if sessionConfig.Phase2 != nil {
		if err := sessionConfig.Phase2.Validate(); err != nil {
			return nil, fmt.Errorf("gemini-cli: phase 2 config: %w", err)
		}
	}
	// Resolve sandbox mode.
	sbMode, err := sandbox.ParseMode(sessionConfig.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("gemini-cli: %w", err)
	}
	command := p.commandForMode(sandbox.ShouldUseNative(sbMode))
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

	// CLI tool mode: inject --tools so the model can call the dreamer
	// record-finding subcommand via Bash.
	if sessionConfig.Phase2.Mode() == analyzer.Phase2ModeCLI {
		command = append(command, "--tools", strings.Join(flagutil.Phase2MCPTools, ","))
	}

	// WritableDirs: temp + gemini config home so the CLI can write
	// session state and cached data.
	geminiHomeDir, err := resolveConfigDir(p.options.Env, "GEMINI_HOME", ".gemini")
	if err != nil {
		return nil, fmt.Errorf("gemini-cli: resolve home dir for sandbox writable paths: %w", err)
	}
	writable := []string{os.TempDir(), geminiHomeDir}

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

// defaultCommand returns the generated Gemini command for the current safety
// boundary. --yolo is used only when the native sandbox is active; otherwise
// Gemini CLI's approval plan mode remains the write-protection layer.
func defaultCommand(useNativeSandbox bool) []string {
	if useNativeSandbox {
		return []string{"gemini", "-p", "--output-format=stream-json", "--yolo"}
	}
	return []string{"gemini", "-p", "--output-format=stream-json", "--approval-mode=plan"}
}

// resolveConfigDir mirrors the config directory that the subprocess will use,
// including provider-specific environment overrides, so the Windows sandbox
// grants write access to the real auth/session state directory.
func resolveConfigDir(env map[string]string, envName, fallbackName string) (string, error) {
	if envValue := strings.TrimSpace(env[envName]); envValue != "" {
		return envValue, nil
	}
	if envValue := strings.TrimSpace(os.Getenv(envName)); envValue != "" {
		return envValue, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fallbackName), nil
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
		return "", fmt.Errorf("gemini-cli: sandbox prepare: %w", err)
	}
	defer prepareCleanup()

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
	// Check waitErr first (matches claude/codex pattern). If the process
	// crashed, its partial output should not be trusted — the exit error
	// is the authoritative signal. Trade-off: a valid partial result from
	// a crashed process is discarded, but a crashed process's output cannot
	// be trusted to be complete or consistent.
	if waitErr != nil {
		err := fmt.Errorf("gemini-cli: process exited: %w (stderr: %s)", waitErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(stderrBuf.String()) {
			return "", errs.RateLimit(ID, "session.run", 0, err)
		}
		return "", err
	}
	if parseErr != nil {
		err := fmt.Errorf("gemini-cli: parse stream-json: %w (stderr: %s)", parseErr, strings.TrimSpace(stderrBuf.String()))
		if transport.IsRateLimitMessage(parseErr.Error()) || transport.IsRateLimitMessage(stderrBuf.String()) {
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
	scanner := transport.NewScanner(r)

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
			errorMsg := event.Message
			if errorMsg == "" {
				errorMsg = "unknown error"
			}
			resultErr = errorMsg
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
