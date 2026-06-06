package openclaudecli

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

const ID = "openclaude-cli"

var providerSpec = &cliharness.Spec{
	ID:               ID,
	ErrPrefix:        "openclaude-cli",
	DefaultCommand:   defaultCommand,
	StartErr:         cliharness.StartErrNotInstalled("Install OpenClaude (`npm i -g @gitlawb/openclaude`) and run `openclaude`, then `/provider` for guided provider setup."),
	CmdStartErr:      cliharness.CmdStartErrUnavailable(ID),
	WorkingDirFlag:   "--add-dir",
	ConfigDir:        cliharness.ConfigDirHardcoded(".openclaude"),
	ParseErrFirst:    true,
	ReadStreamJSON:   readStreamJSON,
	InjectPhase2:     cliharness.InjectMCPFlags,
	ResolveModel:     cliharness.ResolveModel3Tier,
	SupportsMaxTurns: true,
}

func init() {
	analyzer.RegisterProvider(analyzer.ProviderOpenClaudeCLI, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
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
		ID:          analyzer.ProviderOpenClaudeCLI,
		DisplayName: "OpenClaude CLI (recommended)",
		Order:       10,
		Phase2Mode:  analyzer.Phase2ModeMCP,
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

func defaultCommand(useNativeSandbox bool) []string {
	if useNativeSandbox {
		return []string{
			"openclaude", "-p", "--verbose", "--output-format=stream-json",
			"--dangerously-skip-permissions", "--bare", "--no-session-persistence",
		}
	}
	return []string{
		"openclaude", "-p", "--verbose", "--output-format=stream-json",
		"--permission-mode", "plan", "--bare", "--no-session-persistence",
	}
}

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
			if event.Error != "" {
				resultErr = "api error: " + event.Error
			}
		case "result":
			switch event.Subtype {
			case "success":
				resultText = event.Result
			default:
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
		return strings.TrimSpace(assistantText.String()), fmt.Errorf("openclaude-cli: %s", resultErr)
	}
	if assistantText.Len() == 0 && resultText == "" && totalLines > 0 && parseErrors == totalLines {
		return "", fmt.Errorf("openclaude-cli: all %d output lines failed to parse (provider schema change?)", totalLines)
	}
	if resultText != "" {
		if assistantText.Len() > 0 {
			return strings.TrimSpace(assistantText.String() + "\n\n" + resultText), nil
		}
		return strings.TrimSpace(resultText), nil
	}
	return strings.TrimSpace(assistantText.String()), nil
}

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
