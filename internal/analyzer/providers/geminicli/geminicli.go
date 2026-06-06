package geminicli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/cliharness"
	"dreamer/internal/analyzer/transport"
	"dreamer/internal/sandbox"
)

const ID = "gemini-cli"

var providerSpec = &cliharness.Spec{
	ID:             ID,
	ErrPrefix:      "gemini-cli",
	DefaultCommand: defaultCommand,
	StartErr:       cliharness.StartErrNotInstalled("Install Gemini CLI (`npm i -g @google/gemini-cli` or `brew install gemini-cli`) and run `gemini` to authenticate (browser OAuth on first launch)."),
	CmdStartErr:    cliharness.CmdStartErrPlain("gemini-cli"),
	WorkingDirFlag: "",
	ConfigDir:      cliharness.ConfigDirFromEnv("GEMINI_HOME", ".gemini"),
	ParseErrFirst:  false,
	ReadStreamJSON: readStreamJSON,
	InjectPhase2:   cliharness.InjectCLITools,
	ResolveModel:   cliharness.ResolveModel3Tier,
}

func init() {
	analyzer.RegisterProvider(analyzer.ProviderGeminiCLI, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(cliharness.Options{
			Command:             providerConfig.Command,
			Env:                 providerConfig.Env,
			Model:               providerConfig.Model,
			DefaultModel:        providerConfig.DefaultModel,
			MaxTurns:            providerConfig.MaxTurns,
			SandboxProjectWrite: providerConfig.SandboxProjectWrite,
			SandboxWritableDirs: providerConfig.SandboxWritableDirs,
			SandboxNetwork:      providerConfig.SandboxNetwork,
			SandboxSeccomp:      providerConfig.SandboxSeccomp,
			SandboxResources: sandbox.ResourceLimits{
				MemoryMB:  providerConfig.SandboxResources.MemoryMB,
				Processes: providerConfig.SandboxResources.Processes,
				FDs:       providerConfig.SandboxResources.FDs,
			},
			Background: providerConfig.Background,
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderGeminiCLI,
		DisplayName: "Google Gemini CLI",
		Order:       60,
		Phase2Mode:  analyzer.Phase2ModeCLI,
		Capabilities: analyzer.ProviderCapabilities{
			BackgroundSafe:           true,
			RequiresNetwork:          true,
			SupportsBackgroundWrites: true,
			NeedsNativeSandbox:       true,
			AllowsCustomCommand:      true,
		},
	})
}

type provider struct {
	p *cliharness.Provider
}

func New(options cliharness.Options) (analyzer.Provider, error) {
	return &provider{p: cliharness.NewProvider(options, providerSpec)}, nil
}

func (prov *provider) ID() string { return ID }

func (prov *provider) Start(ctx context.Context) error {
	if err := cliharness.LookPath(prov.p.Command); err != nil {
		return prov.p.Spec.StartErr(prov.p.Command[0], err)
	}
	return nil
}

func (prov *provider) NewSession(ctx context.Context, sessionConfig analyzer.SessionConfig) (analyzer.Session, error) {
	return cliharness.NewSession(prov.p, sessionConfig)
}

func (prov *provider) Close() error { return nil }

// defaultCommand returns the generated Gemini command for the current safety
// boundary. --yolo is used only when the native sandbox is active; otherwise
// Gemini CLI's approval plan mode remains the write-protection layer.
func defaultCommand(useNativeSandbox bool) []string {
	if useNativeSandbox {
		return []string{"gemini", "-p", "--output-format=stream-json", "--yolo"}
	}
	return []string{"gemini", "-p", "--output-format=stream-json", "--approval-mode=plan"}
}

// readStreamJSON consumes the stream-json output from gemini --output-format=stream-json
// and returns the final text. It prefers the `response` field from a result event,
// falling back to concatenated message text. Error events are surfaced as Go errors.
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
		var event geminiStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			parseErrors++
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
		return strings.TrimSpace(assistantText.String()), fmt.Errorf("gemini-cli: %s", resultErr)
	}
	if assistantText.Len() == 0 && resultText == "" && totalLines > 0 && parseErrors == totalLines {
		return "", fmt.Errorf("gemini-cli: all %d output lines failed to parse (provider schema change?)", totalLines)
	}
	if resultText != "" {
		if assistantText.Len() > 0 {
			return strings.TrimSpace(assistantText.String() + "\n\n" + resultText), nil
		}
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
