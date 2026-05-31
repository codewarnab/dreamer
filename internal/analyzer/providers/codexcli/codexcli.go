// Package codexcli implements the codex-cli analyzer provider (v1.1).
// It shells out to `codex exec --json` for non-interactive analysis and
// parses the JSON-line event stream to extract the final assistant text.
package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	analyzer "dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/cliharness"
	"dreamer/internal/analyzer/transport"
	"dreamer/internal/sandbox"
)

const ID = "codex-cli"

// codex exec has a 1048576-char hard cap on stdin, but the underlying model's
// context window is the binding constraint. 400 KB ≈ 100k tokens leaves room
// for system prompt + reasoning + completion within typical GPT-5 budgets.
const codexMaxInputBytes = 400_000

var providerSpec = &cliharness.Spec{
	ID:              ID,
	ErrPrefix:       "codex-cli",
	DefaultCommand:  defaultCommand,
	StartErr:        cliharness.StartErrNotInstalled("Install the OpenAI Codex CLI (`npm i -g @openai/codex`) and run `codex login`."),
	CmdStartErr:     cliharness.CmdStartErrPlain("codex-cli"),
	WorkingDirFlag:  "--cd",
	ConfigDir:       cliharness.ConfigDirHardcoded(".codex"),
	ParseErrFirst:   true,
	ReadStreamJSON:  readStreamJSON,
	PreStdinWrite:   capInput,
	ResolveModel:    cliharness.ResolveModel3Tier,
	SkipPhase2Validation: true,
}

func init() {
	analyzer.RegisterProvider(analyzer.ProviderCodexCLI, func(providerConfig analyzer.ProviderConfig) (analyzer.Provider, error) {
		return New(cliharness.Options{
			Command:             providerConfig.Command,
			Env:                 providerConfig.Env,
			Model:               providerConfig.Model,
			DefaultModel:        providerConfig.DefaultModel,
			SandboxProjectWrite: providerConfig.SandboxProjectWrite,
			SandboxWritableDirs: providerConfig.SandboxWritableDirs,
			SandboxNetwork:      providerConfig.SandboxNetwork,
			SandboxSeccomp:      providerConfig.SandboxSeccomp,
			SandboxResources: sandbox.ResourceLimits{
				MemoryMB:  providerConfig.SandboxResources.MemoryMB,
				Processes: providerConfig.SandboxResources.Processes,
				FDs:       providerConfig.SandboxResources.FDs,
			},
		})
	})
	analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
		ID:          analyzer.ProviderCodexCLI,
		DisplayName: "OpenAI Codex CLI",
		Order:       90,
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
		return []string{"codex", "exec", "--json", "--yolo"}
	}
	return []string{"codex", "exec", "--json", "--sandbox", "read-only"}
}

func capInput(body string) string {
	body = transport.CapInputBytes(body, codexMaxInputBytes, "\n\n[transcript truncated to fit codex input cap]\n")
	if dump := os.Getenv("DREAMER_DUMP_CODEX_PROMPT"); dump != "" {
		_ = os.WriteFile(dump, []byte(body), 0o644)
	}
	return body
}

func readStreamJSON(r io.Reader) (string, error) {
	scanner := transport.NewScanner(r)
	var assembled strings.Builder
	var streamErr string
	var totalLines, parseErrors int
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		totalLines++
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
			parseErrors++
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
	if assembled.Len() == 0 && totalLines > 0 && parseErrors == totalLines {
		return "", fmt.Errorf("codex-cli: all %d output lines failed to parse (provider schema change?)", totalLines)
	}
	return strings.TrimSpace(assembled.String()), nil
}
