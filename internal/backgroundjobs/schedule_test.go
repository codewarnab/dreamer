package backgroundjobs

import (
	"testing"
	"time"
)

func TestValidateSchedule_Hourly(t *testing.T) {
	s := ScheduleSpec{
		Kind:     ScheduleHourly,
		Timezone: "UTC",
	}
	if err := ValidateSchedule(s); err != nil {
		t.Errorf("ValidateSchedule(hourly) error: %v", err)
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
		Kind: ScheduleHourly,
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(no timezone) expected error")
	}
}

func TestValidateSchedule_InvalidTimezone(t *testing.T) {
	s := ScheduleSpec{
		Kind:     ScheduleHourly,
		Timezone: "Not/A/Timezone",
	}
	if err := ValidateSchedule(s); err == nil {
		t.Error("ValidateSchedule(invalid timezone) expected error")
	}
}

func TestNextRun_Hourly(t *testing.T) {
	now := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	s := ScheduleSpec{Kind: ScheduleHourly, Timezone: "UTC"}
	next, err := NextRun(s, now)
	if err != nil {
		t.Fatalf("NextRun(hourly) error: %v", err)
	}
	want := now.Add(1 * time.Hour)
	if !next.Equal(want) {
		t.Errorf("NextRun(hourly) = %v, want %v", next, want)
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

func TestNextRun_CronNotImplemented(t *testing.T) {
	now := time.Now().UTC()
	s := ScheduleSpec{Kind: ScheduleCron, Cron: "*/5 * * * *", Timezone: "UTC"}
	_, err := NextRun(s, now)
	if err == nil {
		t.Error("NextRun(cron) expected 'not yet implemented' error")
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
