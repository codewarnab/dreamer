package state

import (
	"context"
	"errors"
	"strings"
	"time"
)

// RecordProviderSuccess increments Runs and TotalTokens, sets
// LastSuccessUTC to now, and clears LastError. Caller must persist state.
func (s *State) RecordProviderSuccess(providerID string, tokens int64) {
	if s == nil || strings.TrimSpace(providerID) == "" {
		return
	}
	if s.ProviderUsage == nil {
		s.ProviderUsage = map[string]ProviderUsage{}
	}
	usage := s.ProviderUsage[providerID]
	usage.Runs++
	if tokens > 0 {
		usage.TotalTokens += tokens
	}
	usage.LastSuccessUTC = time.Now().UTC()
	usage.LastError = ""
	s.ProviderUsage[providerID] = usage
}

// RecordProviderFailure increments Timeouts on DeadlineExceeded, else
// Failures, and stores the truncated error message. Caller must persist state.
func (s *State) RecordProviderFailure(providerID string, err error) {
	if s == nil || strings.TrimSpace(providerID) == "" || err == nil {
		return
	}
	if s.ProviderUsage == nil {
		s.ProviderUsage = map[string]ProviderUsage{}
	}
	usage := s.ProviderUsage[providerID]
	if errors.Is(err, context.DeadlineExceeded) {
		usage.Timeouts++
	} else {
		usage.Failures++
	}
	usage.LastError = TruncateError(err.Error())
	s.ProviderUsage[providerID] = usage
}
