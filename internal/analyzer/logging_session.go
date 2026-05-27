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
//
// Not safe for concurrent Run calls — the session pool single-flights
// sessions today, but callers that bypass the pool must serialize access.
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
	s.logger.Info("provider call started", logging.Any("call", idx), logging.Any("provider", s.provider), logging.Any("timeout", timeout), logging.Any("prompt_bytes", len(prompt)))
	start := time.Now()
	out, err := s.inner.Run(ctx, prompt, timeout)
	elapsed := time.Since(start)
	if err != nil {
		s.logger.Error("provider call failed", logging.Any("call", idx), logging.Any("elapsed", elapsed), logging.Any("err", err))
		return out, err
	}
	s.logger.Info("provider call completed", logging.Any("call", idx), logging.Any("response_bytes", len(out)), logging.Any("elapsed", elapsed))
	s.logger.Info("provider response", logging.Any("call", idx), logging.Any("body", out))
	return out, nil
}

func (s *loggingSession) Close() error { return s.inner.Close() }
