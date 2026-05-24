// Package errs defines structured, tagged error types for dreamer.
// Every error carries a Kind (machine-readable class), optional provider
// context, a human-readable Message, an operator Hint, and a Cause that is
// always preserved through the Unwrap chain.
package errs

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind identifies the class of a dreamer error.
type Kind string

const (
	KindNotInstalled        Kind = "not_installed"
	KindUnauthenticated     Kind = "unauthenticated"
	KindRateLimit           Kind = "rate_limit"
	KindProviderUnavailable Kind = "provider_unavailable"
	KindConfigInvalid       Kind = "config_invalid"
	KindCacheMiss           Kind = "cache_miss"
)

// Error is a structured dreamer error that carries a Kind, optional provider
// context, and the original wrapped cause. All constructors preserve the Cause
// via Unwrap so errors.Is / errors.As chains continue to work.
type Error struct {
	Kind     Kind
	Provider string         // provider id: "copilot", "claude", "codex", etc. (empty for non-provider errors)
	Op       string         // short operation tag: "start", "session.new", "session/prompt", etc.
	Message  string         // human-readable summary
	Hint     string         // operator-facing remediation
	Details  map[string]any // kind-specific structured context, never nil for tagged errors
	Cause    error          // wrapped underlying error; nil only for informational errors like CacheMiss
}

func (e *Error) Error() string {
	var msgBuilder strings.Builder
	if e.Provider != "" && e.Op != "" {
		fmt.Fprintf(&msgBuilder, "[%s:%s] ", e.Provider, e.Op)
	} else if e.Op != "" {
		fmt.Fprintf(&msgBuilder, "[%s] ", e.Op)
	}
	fmt.Fprintf(&msgBuilder, "%s: %s", e.Kind, e.Message)
	if e.Cause != nil {
		fmt.Fprintf(&msgBuilder, ": %v", e.Cause)
	}
	return msgBuilder.String()
}

func (e *Error) Unwrap() error { return e.Cause }

// newErr is the shared internal constructor. If cause is directly a *Error
// (double-wrap case), the inner Kind is preserved and Details are merged
// (outer wins on key collision) rather than re-tagging.
func newErr(kind Kind, provider, op, message, hint string, details map[string]any, cause error) *Error {
	if details == nil {
		details = map[string]any{}
	}
	if inner, ok := cause.(*Error); ok {
		merged := make(map[string]any, len(inner.Details)+len(details))
		for k, v := range inner.Details {
			merged[k] = v
		}
		for k, v := range details {
			merged[k] = v
		}
		return &Error{
			Kind:     inner.Kind,
			Provider: provider,
			Op:       op,
			Message:  message,
			Hint:     hint,
			Details:  merged,
			Cause:    cause,
		}
	}
	return &Error{
		Kind:     kind,
		Provider: provider,
		Op:       op,
		Message:  message,
		Hint:     hint,
		Details:  details,
		Cause:    cause,
	}
}

// NotInstalled indicates the provider binary is not installed or not on PATH.
// provider is required (e.g. "copilot", "claude", "codex").
func NotInstalled(provider, op, hint string, cause error) *Error {
	return newErr(KindNotInstalled, provider, op,
		provider+" binary not found",
		hint,
		map[string]any{"binary": provider},
		cause,
	)
}

// Unauthenticated indicates the provider CLI/SDK is installed but not
// authenticated. provider is required; hint should be the login command.
func Unauthenticated(provider, op, hint string, cause error) *Error {
	return newErr(KindUnauthenticated, provider, op,
		provider+" is not authenticated",
		hint,
		map[string]any{"login_command": hint},
		cause,
	)
}

// RateLimit indicates the provider returned a rate-limit or quota error.
// provider is required. retryAfter may be 0 when not surfaced by the SDK.
func RateLimit(provider, op string, retryAfter time.Duration, cause error) *Error {
	return newErr(KindRateLimit, provider, op,
		provider+" rate limited",
		"",
		map[string]any{"retry_after": retryAfter},
		cause,
	)
}

// ProviderUnavailable indicates a provider could not be started or reached
// (network failure, spawn failure, timeout). provider is required.
func ProviderUnavailable(provider, op string, cause error) *Error {
	return newErr(KindProviderUnavailable, provider, op,
		provider+" provider unavailable",
		"",
		map[string]any{},
		cause,
	)
}

// ConfigInvalid indicates a configuration field has an invalid value.
// field is the dotted config path (e.g. "providers.copilot.cli_url").
func ConfigInvalid(field string, value any, cause error) *Error {
	return newErr(KindConfigInvalid, "", "config.load",
		"invalid configuration: "+field,
		"",
		map[string]any{"field": field, "value": value},
		cause,
	)
}

// CacheMiss is an informational (non-fatal) error indicating a state cache miss.
// reason should be one of "hash_mismatch", "head_sha_changed", "missing_state".
func CacheMiss(reason string) *Error {
	return &Error{
		Kind:    KindCacheMiss,
		Op:      "cache.check",
		Message: "cache miss",
		Details: map[string]any{"reason": reason},
	}
}

// KindOf walks err's Unwrap chain and returns the Kind of the first *Error
// found. Returns "" if no *Error is present in the chain.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ""
}

// Is returns true when the first *Error in err's Unwrap chain has Kind k.
func Is(err error, k Kind) bool {
	return k != "" && KindOf(err) == k
}

// ProviderOf walks err's Unwrap chain and returns the Provider field of the
// first *Error found. Returns "" if none is present.
func ProviderOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Provider
	}
	return ""
}

// Detail walks err's Unwrap chain and returns the Details value for key from
// the first *Error found.
func Detail(err error, key string) (any, bool) {
	var e *Error
	if errors.As(err, &e) && e.Details != nil {
		v, ok := e.Details[key]
		return v, ok
	}
	return nil, false
}
