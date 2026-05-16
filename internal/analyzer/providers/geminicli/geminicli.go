package geminicli

import (
	"context"
	"errors"

	"dreamer/internal/analyzer"
)

const ID = "gemini-cli"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderGeminiCLI, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		return &provider{command: append([]string(nil), cfg.Command...)}, nil
	})
}

type provider struct {
	command []string
}

func (p *provider) ID() string                      { return ID }
func (p *provider) Start(ctx context.Context) error { return errNotImplemented }
func (p *provider) NewSession(ctx context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	return nil, errNotImplemented
}
func (p *provider) Close() error { return nil }

var errNotImplemented = errors.New("gemini-cli provider is not yet implemented; use gemini-acp (or codex-cli / claude-cli / copilot-sdk) in v1.1")
