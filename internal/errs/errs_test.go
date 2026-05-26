package errs_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"dreamer/internal/errs"
)

func TestConstructors_Unwrap(t *testing.T) {
	sentinel := errors.New("underlying error")

	cases := []struct {
		name string
		err  *errs.Error
	}{
		{"NotInstalled", errs.NotInstalled("copilot", "start", "run `copilot auth login`", sentinel)},
		{"RateLimit", errs.RateLimit("copilot", "session/prompt", 30*time.Second, sentinel)},
		{"ProviderUnavailable", errs.ProviderUnavailable("codex", "start", sentinel)},
		{"ConfigInvalid", errs.ConfigInvalid("providers.copilot.cli_url", "bad-url", sentinel)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, sentinel) {
				t.Errorf("errors.Is(err, sentinel) = false; want true (Unwrap must expose cause)")
			}
			if tc.err.Unwrap() != sentinel {
				t.Errorf("Unwrap() = %v; want sentinel", tc.err.Unwrap())
			}
		})
	}
}

func TestKindOf(t *testing.T) {
	inner := errs.RateLimit("copilot", "session/prompt", 0, errors.New("quota"))
	wrapped := fmt.Errorf("orchestrator: %w", inner)

	if got := errs.KindOf(inner); got != errs.KindRateLimit {
		t.Errorf("KindOf(direct) = %q; want %q", got, errs.KindRateLimit)
	}
	if got := errs.KindOf(wrapped); got != errs.KindRateLimit {
		t.Errorf("KindOf(wrapped with fmt.Errorf) = %q; want %q", got, errs.KindRateLimit)
	}
	if got := errs.KindOf(errors.New("plain")); got != "" {
		t.Errorf("KindOf(plain error) = %q; want empty", got)
	}
	if got := errs.KindOf(nil); got != "" {
		t.Errorf("KindOf(nil) = %q; want empty", got)
	}
}

func TestIs(t *testing.T) {
	rl := errs.RateLimit("copilot", "session/prompt", 0, errors.New("q"))
	if !errs.Is(rl, errs.KindRateLimit) {
		t.Error("Is(rateLimitErr, KindRateLimit) = false; want true")
	}
	if errs.Is(rl, errs.KindNotInstalled) {
		t.Error("Is(rateLimitErr, KindNotInstalled) = true; want false")
	}
	if errs.Is(nil, errs.KindRateLimit) {
		t.Error("Is(nil, KindRateLimit) = true; want false")
	}
}

func TestProviderOf(t *testing.T) {
	err := errs.NotInstalled("copilot", "start", "", nil)
	if got := errs.ProviderOf(err); got != "copilot" {
		t.Errorf("ProviderOf = %q; want %q", got, "copilot")
	}
	wrapped := fmt.Errorf("outer: %w", err)
	if got := errs.ProviderOf(wrapped); got != "copilot" {
		t.Errorf("ProviderOf(fmt.Errorf wrapped) = %q; want %q", got, "copilot")
	}
	if got := errs.ProviderOf(errors.New("plain")); got != "" {
		t.Errorf("ProviderOf(plain) = %q; want empty", got)
	}
}

func TestDetail_RateLimit(t *testing.T) {
	const want = 45 * time.Second
	err := errs.RateLimit("copilot", "session/prompt", want, errors.New("q"))
	v, ok := errs.Detail(err, "retry_after")
	if !ok {
		t.Fatal("Detail(err, \"retry_after\") ok = false; want true")
	}
	got, ok := v.(time.Duration)
	if !ok {
		t.Fatalf("retry_after type = %T; want time.Duration", v)
	}
	if got != want {
		t.Errorf("retry_after = %v; want %v", got, want)
	}
}

func TestDetail_NotFound(t *testing.T) {
	err := errs.RateLimit("copilot", "session/prompt", 0, errors.New("q"))
	_, ok := errs.Detail(err, "nonexistent_key")
	if ok {
		t.Error("Detail returned ok=true for unknown key; want false")
	}
}

func TestDoubleWrap_PreservesInnerKind(t *testing.T) {
	inner := errs.RateLimit("copilot", "session/prompt", 10*time.Second, errors.New("quota exceeded"))

	// Wrapping a *errs.Error with another constructor should preserve the inner Kind.
	outer := errs.ProviderUnavailable("copilot", "start", inner)

	if outer.Kind != errs.KindRateLimit {
		t.Errorf("outer.Kind = %q; want %q (inner kind preserved)", outer.Kind, errs.KindRateLimit)
	}
	if !errs.Is(outer, errs.KindRateLimit) {
		t.Error("Is(outer, KindRateLimit) = false; want true")
	}

	// retry_after detail from inner should be accessible on outer.
	v, ok := errs.Detail(outer, "retry_after")
	if !ok {
		t.Fatal("Detail(outer, \"retry_after\") ok = false after double-wrap; want true")
	}
	if d, _ := v.(time.Duration); d != 10*time.Second {
		t.Errorf("retry_after = %v; want 10s", d)
	}
}

func TestDoubleWrap_MergesDetails_OuterWins(t *testing.T) {
	inner := errs.RateLimit("copilot", "session/prompt", 5*time.Second, errors.New("q"))

	// Outer adds its own retry_after: it should override inner's.
	outer := errs.RateLimit("copilot", "start", 30*time.Second, inner)

	v, ok := errs.Detail(outer, "retry_after")
	if !ok {
		t.Fatal("Detail(outer, retry_after) = false after double-wrap; want true")
	}
	if d, _ := v.(time.Duration); d != 30*time.Second {
		t.Errorf("retry_after = %v; want 30s (outer should win)", d)
	}
}

func TestError_String(t *testing.T) {
	err := errs.RateLimit("copilot", "session/prompt", 0, errors.New("quota"))
	s := err.Error()
	for _, want := range []string{"copilot", "session/prompt", "rate_limit", "quota"} {
		if !contains(s, want) {
			t.Errorf("Error() = %q; want it to contain %q", s, want)
		}
	}
}

func TestConfigInvalid_Details(t *testing.T) {
	err := errs.ConfigInvalid("providers.copilot.cli_url", "bad://url", nil)
	if err.Kind != errs.KindConfigInvalid {
		t.Errorf("Kind = %q; want KindConfigInvalid", err.Kind)
	}
	v, ok := errs.Detail(err, "field")
	if !ok || v != "providers.copilot.cli_url" {
		t.Errorf("Detail field = %v ok=%v; want providers.copilot.cli_url true", v, ok)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
