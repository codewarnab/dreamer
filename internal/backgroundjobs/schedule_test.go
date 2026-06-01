package backgroundjobs

import (
	"testing"
	"time"
)

func TestValidateSchedule_Hourly(t *testing.T) {
	s := ScheduleSpec{
		Kind:     ScheduleInterval,
		Timezone: "UTC",
	}
	if err := ValidateSchedule(s); err != nil {
		t.Errorf("ValidateSchedule(hourly) error: %v", err)
	}
}

func TestValidateSchedule_HourlyEvery(t *testing.T) {
	tests := []struct {
		name    string
		every   string
		wantErr bool
	}{
		{"5m", "5m", false},
		{"15m", "15m", false},
		{"45m", "45m", false},
		{"2h", "2h", false},
		{"90m", "90m", false},
		{"23h", "23h", false},
		{"empty", "", false},
		{"30s", "30s", true},       // too small
		{"24h", "24h", true},       // too large
		{"bad", "abc", true},       // malformed
		{"1d", "1d", true},         // unsupported unit
		{"5", "5", true},           // missing unit
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ScheduleSpec{
				Kind:     ScheduleInterval,
				Every:    tt.every,
				Timezone: "UTC",
			}
			err := ValidateSchedule(s)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSchedule(hourly, every=%q) error = %v, wantErr %v", tt.every, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSchedule_EveryOnlyWithHourly(t *testing.T) {
	tests := []struct {
		name string
		kind ScheduleKind
		spec ScheduleSpec
	}{
		{"daily", ScheduleDaily, ScheduleSpec{Kind: ScheduleDaily, Every: "15m", TimeOfDay: "09:00", Timezone: "UTC"}},
		{"weekly", ScheduleWeekly, ScheduleSpec{Kind: ScheduleWeekly, Every: "15m", DayOfWeek: "Monday", TimeOfDay: "09:00", Timezone: "UTC"}},
		{"cron", ScheduleCron, ScheduleSpec{Kind: ScheduleCron, Every: "15m", Cron: "0 9 * * 1", Timezone: "UTC"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateSchedule(tt.spec); err == nil {
				t.Errorf("ValidateSchedule(%s, every=15m) expected error", tt.kind)
			}
		})
	}
}

func TestValidateSchedule_Daily(t *testing.T) {
	s := ScheduleSpec{
		Kind:      ScheduleDaily,
		TimeOfDay: "14:30",
		Timezone:  "America/New_York",
	}
	if err := ValidateSchedule(s); err != nil {
		t.Errorf("ValidateSchedule(daily) error: %v", err)
	}
}

func TestValidateSchedule_DailyMissingTimeOfDay(t *testing.T) {
	s := ScheduleSpec{
		Kind:     ScheduleDaily,
		Timezone: "UTC",
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(daily without time_of_day) expected error")
	}
}

func TestValidateSchedule_DailyInvalidTimeOfDay(t *testing.T) {
	s := ScheduleSpec{
		Kind:      ScheduleDaily,
		TimeOfDay: "25:00",
		Timezone:  "UTC",
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(daily 25:00) expected error")
	}
}

func TestValidateSchedule_Weekly(t *testing.T) {
	s := ScheduleSpec{
		Kind:      ScheduleWeekly,
		DayOfWeek: "Monday",
		TimeOfDay: "09:00",
		Timezone:  "Europe/London",
	}
	if err := ValidateSchedule(s); err != nil {
		t.Errorf("ValidateSchedule(weekly) error: %v", err)
	}
}

func TestValidateSchedule_WeeklyMissingDayOfWeek(t *testing.T) {
	s := ScheduleSpec{
		Kind:      ScheduleWeekly,
		TimeOfDay: "09:00",
		Timezone:  "UTC",
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(weekly without day_of_week) expected error")
	}
}

func TestValidateSchedule_WeeklyInvalidDayOfWeek(t *testing.T) {
	s := ScheduleSpec{
		Kind:      ScheduleWeekly,
		DayOfWeek: "Funday",
		TimeOfDay: "09:00",
		Timezone:  "UTC",
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(weekly Funday) expected error")
	}
}

func TestValidateSchedule_WeeklyMissingTimeOfDay(t *testing.T) {
	s := ScheduleSpec{
		Kind:      ScheduleWeekly,
		DayOfWeek: "Monday",
		Timezone:  "UTC",
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(weekly without time_of_day) expected error")
	}
}

func TestValidateSchedule_Cron(t *testing.T) {
	s := ScheduleSpec{
		Kind:     ScheduleCron,
		Cron:     "*/5 * * * *",
		Timezone: "UTC",
	}
	if err := ValidateSchedule(s); err != nil {
		t.Errorf("ValidateSchedule(cron) error: %v", err)
	}
}

func TestValidateSchedule_CronMissingExpression(t *testing.T) {
	s := ScheduleSpec{
		Kind:     ScheduleCron,
		Timezone: "UTC",
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(cron without expression) expected error")
	}
}

func TestValidateSchedule_UnknownKind(t *testing.T) {
	s := ScheduleSpec{
		Kind:     ScheduleKind("minutely"),
		Timezone: "UTC",
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(unknown kind) expected error")
	}
}

func TestValidateSchedule_MissingTimezone(t *testing.T) {
	s := ScheduleSpec{
		Kind: ScheduleInterval,
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(no timezone) expected error")
	}
}

func TestValidateSchedule_InvalidTimezone(t *testing.T) {
	s := ScheduleSpec{
		Kind:     ScheduleInterval,
		Timezone: "Not/A/Timezone",
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(invalid timezone) expected error")
	}
}

func TestNextRun_Hourly(t *testing.T) {
	now := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	s := ScheduleSpec{Kind: ScheduleInterval, Timezone: "UTC"}
	next, err := NextRun(s, now)
	if err != nil {
		t.Fatalf("NextRun(hourly) error: %v", err)
	}
	want := now.Add(1 * time.Hour)
	if !next.Equal(want) {
		t.Errorf("NextRun(hourly) = %v, want %v", next, want)
	}
}

func TestNextRun_HourlyEvery(t *testing.T) {
	now := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	tests := []struct {
		name     string
		every    string
		wantMins int
	}{
		{"5m", "5m", 5},
		{"15m", "15m", 15},
		{"45m", "45m", 45},
		{"2h", "2h", 120},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ScheduleSpec{Kind: ScheduleInterval, Every: tt.every, Timezone: "UTC"}
			next, err := NextRun(s, now)
			if err != nil {
				t.Fatalf("NextRun(hourly, every=%s) error: %v", tt.every, err)
			}
			want := now.Add(time.Duration(tt.wantMins) * time.Minute)
			if !next.Equal(want) {
				t.Errorf("NextRun(hourly, every=%s) = %v, want %v", tt.every, next, want)
			}
		})
	}
}

func TestNextRun_Daily(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	s := ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "14:30", Timezone: "UTC"}
	next, err := NextRun(s, now)
	if err != nil {
		t.Fatalf("NextRun(daily) error: %v", err)
	}
	want := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun(daily) = %v, want %v", next, want)
	}
}

func TestNextRun_DailyWrap(t *testing.T) {
	now := time.Date(2026, 5, 27, 15, 0, 0, 0, time.UTC)
	s := ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "09:00", Timezone: "UTC"}
	next, err := NextRun(s, now)
	if err != nil {
		t.Fatalf("NextRun(daily wrap) error: %v", err)
	}
	want := time.Date(2026, 5, 28, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun(daily wrap) = %v, want %v", next, want)
	}
}

func TestNextRun_Weekly(t *testing.T) {
	// Tuesday 2026-05-26, want next Monday at 09:00
	now := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	s := ScheduleSpec{Kind: ScheduleWeekly, DayOfWeek: "Monday", TimeOfDay: "09:00", Timezone: "UTC"}
	next, err := NextRun(s, now)
	if err != nil {
		t.Fatalf("NextRun(weekly) error: %v", err)
	}
	// Next Monday after Tuesday is 2026-06-01
	want := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun(weekly) = %v, want %v", next, want)
	}
}

func TestNextRun_WeeklySameDay(t *testing.T) {
	// Monday 2026-05-25, want same-day Monday at 14:00 (now is before)
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	s := ScheduleSpec{Kind: ScheduleWeekly, DayOfWeek: "Monday", TimeOfDay: "14:00", Timezone: "UTC"}
	next, err := NextRun(s, now)
	if err != nil {
		t.Fatalf("NextRun(weekly same-day) error: %v", err)
	}
	want := time.Date(2026, 5, 25, 14, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun(weekly same-day) = %v, want %v", next, want)
	}
}

func TestNextRun_CronEveryFiveMinutes(t *testing.T) {
	now := time.Date(2026, 5, 26, 14, 3, 0, 0, time.UTC)
	s := ScheduleSpec{Kind: ScheduleCron, Cron: "*/5 * * * *", Timezone: "UTC"}
	next, err := NextRun(s, now)
	if err != nil {
		t.Fatalf("NextRun(cron */5) error: %v", err)
	}
	want := time.Date(2026, 5, 26, 14, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun(cron */5) = %v, want %v", next, want)
	}
}

func TestNextRun_CronDaily(t *testing.T) {
	now := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	s := ScheduleSpec{Kind: ScheduleCron, Cron: "0 9 * * *", Timezone: "UTC"}
	next, err := NextRun(s, now)
	if err != nil {
		t.Fatalf("NextRun(cron daily 09:00) error: %v", err)
	}
	want := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun(cron daily 09:00) = %v, want %v", next, want)
	}
}

func TestNextRun_CronWeekly(t *testing.T) {
	// Tuesday 2026-05-26, want Monday at 09:00 (next Monday is June 1)
	now := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	s := ScheduleSpec{Kind: ScheduleCron, Cron: "0 9 * * 1", Timezone: "UTC"}
	next, err := NextRun(s, now)
	if err != nil {
		t.Fatalf("NextRun(cron weekly Monday) error: %v", err)
	}
	want := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun(cron weekly Monday) = %v, want %v", next, want)
	}
}

func TestNextRun_CaseInsensitiveDayOfWeek(t *testing.T) {
	now := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	s := ScheduleSpec{Kind: ScheduleWeekly, DayOfWeek: "monday", TimeOfDay: "09:00", Timezone: "UTC"}
	next, err := NextRun(s, now)
	if err != nil {
		t.Fatalf("NextRun(weekly lowercase) error: %v", err)
	}
	want := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun(weekly lowercase) = %v, want %v", next, want)
	}
}

// B5: parseTimeOfDay should reject malformed input.
func TestParseTimeOfDay_RejectsMalformed(t *testing.T) {
	tests := []struct {
		input string
		desc  string
	}{
		{"9:5", "single-digit minute"},
		{"9:5:30", "trailing seconds"},
		{"99:99", "out-of-range"},
		{"14:30 garbage", "trailing garbage"},
		{"", "empty string"},
		{"noon", "text"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			_, _, err := parseTimeOfDay(tt.input)
			if err == nil {
				t.Errorf("parseTimeOfDay(%q) expected error, got nil", tt.input)
			}
		})
	}
}

// B5: parseTimeOfDay should accept valid HH:MM.
func TestParseTimeOfDay_AcceptsValid(t *testing.T) {
	tests := []struct {
		input string
		wantH int
		wantM int
	}{
		{"00:00", 0, 0},
		{"09:05", 9, 5},
		{"14:30", 14, 30},
		{"23:59", 23, 59},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			h, m, err := parseTimeOfDay(tt.input)
			if err != nil {
				t.Fatalf("parseTimeOfDay(%q) error: %v", tt.input, err)
			}
			if h != tt.wantH || m != tt.wantM {
				t.Errorf("parseTimeOfDay(%q) = %d:%d, want %d:%d", tt.input, h, m, tt.wantH, tt.wantM)
			}
		})
	}
}

// B6: parseCronInt should reject trailing junk.
func TestParseCronInt_RejectsTrailingJunk(t *testing.T) {
	tests := []struct {
		input string
		desc  string
	}{
		{"5xxx", "trailing letters"},
		{"5.5", "decimal"},
		{"", "empty"},
		{"abc", "non-numeric"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			_, err := parseCronInt(tt.input)
			if err == nil {
				t.Errorf("parseCronInt(%q) expected error, got nil", tt.input)
			}
		})
	}
}

// B6: parseCron should reject trailing junk in fields.
func TestParseCron_RejectsTrailingJunk(t *testing.T) {
	tests := []struct {
		expr  string
		desc  string
	}{
		{"5xxx * * * *", "minute field junk"},
		{"* 5xxx * * *", "hour field junk"},
		{"5.5 * * * *", "minute decimal"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			_, err := parseCron(tt.expr)
			if err == nil {
				t.Errorf("parseCron(%q) expected error, got nil", tt.expr)
			}
		})
	}
}

func TestParseEveryDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"5m", "5m", 5 * time.Minute, false},
		{"15m", "15m", 15 * time.Minute, false},
		{"45m", "45m", 45 * time.Minute, false},
		{"2h", "2h", 2 * time.Hour, false},
		{"90m", "90m", 90 * time.Minute, false},
		{"23h", "23h", 23 * time.Hour, false},
		{"1m", "1m", 1 * time.Minute, false},
		{"30s", "30s", 0, true},      // too small
		{"24h", "24h", 0, true},      // too large
		{"bad", "abc", 0, true},      // malformed
		{"1d", "1d", 0, true},        // unsupported unit
		{"5", "5", 0, true},          // missing unit
		{"negative", "-5m", 0, true}, // negative
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEveryDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseEveryDuration(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseEveryDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestEveryDuration(t *testing.T) {
	tests := []struct {
		name string
		spec ScheduleSpec
		want time.Duration
	}{
		{"default", ScheduleSpec{Kind: ScheduleInterval}, 1 * time.Hour},
		{"5m", ScheduleSpec{Kind: ScheduleInterval, Every: "5m"}, 5 * time.Minute},
		{"2h", ScheduleSpec{Kind: ScheduleInterval, Every: "2h"}, 2 * time.Hour},
		{"invalid falls back", ScheduleSpec{Kind: ScheduleInterval, Every: "bad"}, 1 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EveryDuration(tt.spec)
			if got != tt.want {
				t.Errorf("EveryDuration(%+v) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestDurationToISO8601(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"5m", 5 * time.Minute, "PT5M"},
		{"15m", 15 * time.Minute, "PT15M"},
		{"45m", 45 * time.Minute, "PT45M"},
		{"1h", 1 * time.Hour, "PT1H"},
		{"2h", 2 * time.Hour, "PT2H"},
		{"90m", 90 * time.Minute, "PT90M"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := durationToISO8601(tt.d)
			if got != tt.want {
				t.Errorf("durationToISO8601(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestExecutionTimeLimit_HourlyEvery(t *testing.T) {
	tests := []struct {
		name  string
		every string
		want  string
	}{
		{"default", "", "PT55M"},
		{"5m", "5m", "PT5M"},
		{"15m", "15m", "PT15M"},
		{"2h", "2h", "PT2H"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ScheduleSpec{Kind: ScheduleInterval, Every: tt.every}
			got := executionTimeLimit(spec)
			if got != tt.want {
				t.Errorf("executionTimeLimit(every=%q) = %q, want %q", tt.every, got, tt.want)
			}
		})
	}
}

func TestDefaultTimeoutFor(t *testing.T) {
	tests := []struct {
		name string
		spec ScheduleSpec
		want time.Duration
	}{
		{
			name: "interval default (1h)",
			spec: ScheduleSpec{Kind: ScheduleInterval, Timezone: "UTC"},
			want: 48 * time.Minute, // 1h * 0.8
		},
		{
			name: "interval 5m",
			spec: ScheduleSpec{Kind: ScheduleInterval, Every: "5m", Timezone: "UTC"},
			want: 4 * time.Minute, // 5m * 0.8
		},
		{
			name: "interval 15m",
			spec: ScheduleSpec{Kind: ScheduleInterval, Every: "15m", Timezone: "UTC"},
			want: 12 * time.Minute, // 15m * 0.8
		},
		{
			name: "interval 2h capped at 1h",
			spec: ScheduleSpec{Kind: ScheduleInterval, Every: "2h", Timezone: "UTC"},
			want: 1 * time.Hour, // hard cap
		},
		{
			name: "daily",
			spec: ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "09:00", Timezone: "UTC"},
			want: 30 * time.Minute,
		},
		{
			name: "weekly",
			spec: ScheduleSpec{Kind: ScheduleWeekly, DayOfWeek: "Monday", TimeOfDay: "09:00", Timezone: "UTC"},
			want: 30 * time.Minute,
		},
		{
			name: "cron",
			spec: ScheduleSpec{Kind: ScheduleCron, Cron: "*/5 * * * *", Timezone: "UTC"},
			want: 30 * time.Minute,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultTimeoutFor(tt.spec)
			if got != tt.want {
				t.Errorf("DefaultTimeoutFor(%+v) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}
