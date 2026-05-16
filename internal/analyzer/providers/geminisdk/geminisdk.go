package geminisdk

import (
	"context"
	"errors"

	"dreamer/internal/analyzer"
)

const ID = "gemini-sdk"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderGeminiSDK, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		return &provider{apiKeyEnv: cfg.APIKeyEnv, model: cfg.Model}, nil
	})
}

type provider struct {
	apiKeyEnv string
	model     string
}

func (p *provider) ID() string                      { return ID }
func (p *provider) Start(ctx context.Context) error { return errNotImplemented }
func (p *provider) NewSession(ctx context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	return nil, errNotImplemented
}
func (p *provider) Close() error { return nil }

var errNotImplemented = errors.New("gemini-sdk provider is not yet implemented; install the official Google Generative AI Go SDK and wire it here")
