package claudecli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/cliharness"
	"dreamer/internal/analyzer/transport"
)

const ID = "claude-cli"

var providerSpec = &cliharness.Spec{
	ID:              ID,
	ErrPrefix:       "claude-cli",
	DefaultCommand:  defaultCommand,
	StartErr:        cliharness.StartErrNotInstalled("Install Claude Code (`npm i -g @anthropic-ai/claude-code`) and run `claude` to authenticate."),
	CmdStartErr:     cliharness.CmdStartErrPlain("claude-cli"),
	WorkingDirFlag:  "--add-dir",
	ConfigDir:       cliharness.ConfigDirFromEnv("CLAUDE_CONFIG_DIR", ".claude"),
	ParseErrFirst:   false,
	ReadStreamJSON:  readStreamJSON,
	InjectPhase2:    cliharness.InjectMCPFlags,
	ResolveModel:    cliharness.ResolveModel2Tier,
}

func init() {
	analyzer.RegisterProvider(analyzer.ProviderClaudeCLI, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(cliharness.Options{
			Command:             providerConfig.Command,
			Env:                 providerConfig.Env,
			Model:               providerConfig.Model,
			DefaultModel:        providerConfig.DefaultModel,
			SandboxProjectWrite: providerConfig.SandboxProjectWrite,
			SandboxNetwork:      providerConfig.SandboxNetwork,
			SandboxSeccomp:      providerConfig.SandboxSeccomp,
			SandboxResources: cliharness.SandboxResourceLimits{
				MemoryMB:  providerConfig.SandboxResources.MemoryMB,
				Processes: providerConfig.SandboxResources.Processes,
				FDs:       providerConfig.SandboxResources.FDs,
			},
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderClaudeCLI,
		DisplayName: "Anthropic Claude (stream-json)",
		Order:       40,
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
			"claude", "-p", "--verbose", "--output-format=stream-json",
			"--dangerously-skip-permissions", "--bare", "--no-session-persistence",
		}
	}
	return []string{
		"claude", "-p", "--verbose", "--output-format=stream-json",
		"--permission-mode", "plan", "--bare", "--no-session-persistence",
	}
}

func readStreamJSON(r io.Reader) (string, error) {
	scanner := transport.NewScanner(r)
	var assistantText strings.Builder
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
	if assistantText.Len() == 0 && totalLines > 0 && parseErrors == totalLines {
		return "", fmt.Errorf("claude-cli: all %d output lines failed to parse (provider schema change?)", totalLines)
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
