package analyzer

import (
	"context"
	"time"

	"dreamer/internal/logging"
)

// loggingSession wraps a Session and records every prompt/response pair to
// the dreamer log. We log the full prompt and full response so that operators
// can see exactly what was sent to the model. The wrapper is a transparent
// pass-through aside from logging.
type loggingSession struct {
	inner    Session
	logger   *logging.Logger
	provider string
	runIndex int
}

// NewLoggingSession wraps an analyzer.Session so each Run call is logged with
// prompt/response sizes, elapsed time, and the full response body.
func NewLoggingSession(inner Session, logger *logging.Logger, providerID string) Session {
	return &loggingSession{inner: inner, logger: logger, provider: providerID}
}

func (s *loggingSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	s.runIndex++
	idx := s.runIndex
	s.logger.Info("provider call #%d provider=%q timeout=%s prompt_bytes=%d",
		idx, s.provider, timeout, len(prompt))
	start := time.Now()
	out, err := s.inner.Run(ctx, prompt, timeout)
	elapsed := time.Since(start)
	if err != nil {
		s.logger.Error("provider call #%d FAILED elapsed=%s error=%v", idx, elapsed, err)
		return out, err
	}
	s.logger.Info("provider call #%d response_bytes=%d elapsed=%s", idx, len(out), elapsed)
	s.logger.Info("provider call #%d RESPONSE BEGIN >>>\n%s\n<<< provider call #%d RESPONSE END",
		idx, out, idx)
	return out, nil
}

func (s *loggingSession) Close() error { return s.inner.Close() }
