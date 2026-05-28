package pipeline

import (
	"testing"
	"time"

	"dreamer/internal/chat"
)

func TestParseLookbackWindow(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		want        time.Duration
		wantEnabled bool
		wantErr     bool
	}{
		{name: "hour", value: "1h", want: time.Hour, wantEnabled: true},
		{name: "day", value: "1d", want: 24 * time.Hour, wantEnabled: true},
		{name: "week", value: "1w", want: 7 * 24 * time.Hour, wantEnabled: true},
		{name: "month", value: "1mo", want: 30 * 24 * time.Hour, wantEnabled: true},
		{name: "minutes", value: "30m", want: 30 * time.Minute, wantEnabled: true},
		{name: "empty disables filter", value: "", wantEnabled: false},
		{name: "zero rejected", value: "0h", wantErr: true},
		{name: "negative rejected", value: "-1h", wantErr: true},
		{name: "signed rejected", value: "+1h", wantErr: true},
		{name: "decimal rejected", value: "1.5h", wantErr: true},
		{name: "unknown unit rejected", value: "1x", wantErr: true},
		{name: "spaced value rejected", value: "1 h", wantErr: true},
		{name: "word rejected", value: "hour", wantErr: true},
		{name: "lifetime lowercase disables filter", value: "lifetime", wantEnabled: false},
		{name: "lifetime mixed case disables filter", value: "Lifetime", wantEnabled: false},
		{name: "lifetime upper disables filter", value: "LIFETIME", wantEnabled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotEnabled, err := parseLookbackWindow(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseLookbackWindow(%q) expected error", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLookbackWindow(%q) returned error: %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("duration = %s, want %s", got, tt.want)
			}
			if gotEnabled != tt.wantEnabled {
				t.Fatalf("enabled = %v, want %v", gotEnabled, tt.wantEnabled)
			}
		})
	}
}

func TestFilterSourcesByLookback(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	sources := []chat.Source{
		{Path: "old.jsonl", ModifiedTime: now.Add(-time.Hour - time.Nanosecond)},
		{Path: "cutoff.jsonl", ModifiedTime: now.Add(-time.Hour)},
		{Path: "recent.jsonl", ModifiedTime: now.Add(-time.Minute)},
		{Path: "future.jsonl", ModifiedTime: now.Add(time.Minute)},
	}

	filtered := filterSourcesByLookback(sources, now, time.Hour, true)

	assertStringSliceEqual(t, sourcePaths(filtered), []string{
		"cutoff.jsonl",
		"recent.jsonl",
		"future.jsonl",
	})
}

func TestFilterSourcesByLookbackDisabledReturnsAllSources(t *testing.T) {
	sources := []chat.Source{
		{Path: "old.jsonl", ModifiedTime: time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)},
		{Path: "recent.jsonl", ModifiedTime: time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)},
	}

	filtered := filterSourcesByLookback(sources, time.Now(), time.Hour, false)

	assertStringSliceEqual(t, sourcePaths(filtered), []string{"old.jsonl", "recent.jsonl"})
}
