package backgroundjobs

import (
	"testing"
)

func TestSanitizeScheduleName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"clean", "my-job", "my-job"},
		{"newline", "my\njob", "my job"},
		{"carriage return", "my\rjob", "my job"},
		{"null byte", "my\x00job", "my job"},
		{"tab", "my\tjob", "my job"},
		{"control", "my\x01job", "my job"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeScheduleName(tt.in)
			if got != tt.want {
				t.Errorf("SanitizeScheduleName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExecutionTimeLimit_Hourly(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleInterval}
	got := executionTimeLimit(spec)
	// 1h interval * 80% = 48m.
	if got != "PT48M" {
		t.Errorf("executionTimeLimit(hourly) = %q, want PT48M", got)
	}
}

func TestExecutionTimeLimit_Daily(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleDaily}
	got := executionTimeLimit(spec)
	if got != "PT2H" {
		t.Errorf("executionTimeLimit(daily) = %q, want PT2H", got)
	}
}
