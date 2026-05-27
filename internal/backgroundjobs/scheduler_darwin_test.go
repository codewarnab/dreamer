//go:build darwin

package backgroundjobs

import (
	"testing"
	"time"
)

func TestBuildCalendarIntervals_Hourly(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleHourly}
	intervals := buildCalendarIntervals(spec)
	if len(intervals) != 1 {
		t.Fatalf("got %d intervals, want 1", len(intervals))
	}
	if intervals[0].Hour != -1 {
		t.Errorf("Hour = %d, want -1", intervals[0].Hour)
	}
	if intervals[0].Minute != 0 {
		t.Errorf("Minute = %d, want 0", intervals[0].Minute)
	}
	if intervals[0].Weekday != -1 {
		t.Errorf("Weekday = %d, want -1 (not specified)", intervals[0].Weekday)
	}
}

func TestBuildCalendarIntervals_Daily(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "14:30"}
	intervals := buildCalendarIntervals(spec)
	if len(intervals) != 1 {
		t.Fatalf("got %d intervals, want 1", len(intervals))
	}
	if intervals[0].Hour != 14 {
		t.Errorf("Hour = %d, want 14", intervals[0].Hour)
	}
	if intervals[0].Minute != 30 {
		t.Errorf("Minute = %d, want 30", intervals[0].Minute)
	}
	if intervals[0].Weekday != -1 {
		t.Errorf("Weekday = %d, want -1 (not specified)", intervals[0].Weekday)
	}
}

func TestBuildCalendarIntervals_Weekly(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleWeekly, DayOfWeek: "Monday", TimeOfDay: "09:00"}
	intervals := buildCalendarIntervals(spec)
	if len(intervals) != 1 {
		t.Fatalf("got %d intervals, want 1", len(intervals))
	}
	if intervals[0].Hour != 9 {
		t.Errorf("Hour = %d, want 9", intervals[0].Hour)
	}
	if intervals[0].Minute != 0 {
		t.Errorf("Minute = %d, want 0", intervals[0].Minute)
	}
	// Monday = 1 in launchd.
	if intervals[0].Weekday != 1 {
		t.Errorf("Weekday = %d, want 1 (Monday)", intervals[0].Weekday)
	}
}

func TestBuildCalendarIntervals_Sunday(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleWeekly, DayOfWeek: "Sunday", TimeOfDay: "10:00"}
	intervals := buildCalendarIntervals(spec)
	if len(intervals) != 1 {
		t.Fatalf("got %d intervals, want 1", len(intervals))
	}
	// Sunday = 0 in launchd.
	if intervals[0].Weekday != 0 {
		t.Errorf("Weekday = %d, want 0 (Sunday)", intervals[0].Weekday)
	}
}

func TestComputeNextCalendarRun_Hourly(t *testing.T) {
	now := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	next := computeNextCalendarRun(now, -1, 0, -1)
	// Next hourly run should be at 15:00.
	if next.Hour() != 15 || next.Minute() != 0 {
		t.Errorf("next = %v, want 15:00", next)
	}
}

func TestComputeNextCalendarRun_Daily(t *testing.T) {
	// Before target time.
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	next := computeNextCalendarRun(now, 9, 0, -1)
	if next.Day() != 27 || next.Hour() != 9 {
		t.Errorf("next = %v, want May 27 09:00", next)
	}
	// After target time — should be tomorrow.
	now = time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	next = computeNextCalendarRun(now, 9, 0, -1)
	if next.Day() != 28 || next.Hour() != 9 {
		t.Errorf("next = %v, want May 28 09:00", next)
	}
}

func TestComputeNextCalendarRun_Weekly(t *testing.T) {
	// Wednesday = 3. It's Monday (weekday 1).
	now := time.Date(2026, 5, 25, 8, 0, 0, 0, time.UTC)
	next := computeNextCalendarRun(now, 9, 0, 3)
	if next.Weekday() != time.Wednesday {
		t.Errorf("next weekday = %v, want Wednesday", next.Weekday())
	}
}

func TestWeekdayToLaunchd(t *testing.T) {
	tests := []struct {
		day  string
		want int
	}{
		{"sunday", 0},
		{"monday", 1},
		{"tuesday", 2},
		{"wednesday", 3},
		{"thursday", 4},
		{"friday", 5},
		{"saturday", 6},
		{"invalid", -1},
	}
	for _, tt := range tests {
		t.Run(tt.day, func(t *testing.T) {
			got := weekdayToLaunchd(tt.day)
			if got != tt.want {
				t.Errorf("weekdayToLaunchd(%q) = %d, want %d", tt.day, got, tt.want)
			}
		})
	}
}
