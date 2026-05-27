//go:build linux

package backgroundjobs

import (
	"strings"
	"testing"
)

func TestScheduleToOnCalendar_Hourly(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleHourly}
	got := scheduleToOnCalendar(spec)
	if got != "*-*-* *:00:00" {
		t.Errorf("scheduleToOnCalendar(hourly) = %q, want *-*-* *:00:00", got)
	}
}

func TestScheduleToOnCalendar_Daily(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "14:30"}
	got := scheduleToOnCalendar(spec)
	if got != "*-*-* 14:30:00" {
		t.Errorf("scheduleToOnCalendar(daily 14:30) = %q, want *-*-* 14:30:00", got)
	}
}

func TestScheduleToOnCalendar_Weekly(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleWeekly, DayOfWeek: "Monday", TimeOfDay: "09:00"}
	got := scheduleToOnCalendar(spec)
	if !strings.Contains(got, "Mon") {
		t.Errorf("scheduleToOnCalendar(weekly Monday) should contain Mon, got %q", got)
	}
	if !strings.Contains(got, "09:00:00") {
		t.Errorf("scheduleToOnCalendar(weekly 09:00) should contain 09:00:00, got %q", got)
	}
}

func TestWeekdayToSystemdDay(t *testing.T) {
	tests := []struct {
		day  string
		want string
	}{
		{"monday", "Mon"},
		{"tuesday", "Tue"},
		{"wednesday", "Wed"},
		{"thursday", "Thu"},
		{"friday", "Fri"},
		{"saturday", "Sat"},
		{"sunday", "Sun"},
		{"invalid", ""},
	}
	for _, tt := range tests {
		t.Run(tt.day, func(t *testing.T) {
			got := weekdayToSystemdDay(tt.day)
			if got != tt.want {
				t.Errorf("weekdayToSystemdDay(%q) = %q, want %q", tt.day, got, tt.want)
			}
		})
	}
}

func TestTimeoutSec_Hourly(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleHourly}
	got := timeoutSec(spec)
	if got != 2700 { // 45 minutes
		t.Errorf("timeoutSec(hourly) = %d, want 2700", got)
	}
}

func TestTimeoutSec_Daily(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleDaily}
	got := timeoutSec(spec)
	if got != 7200 { // 2 hours
		t.Errorf("timeoutSec(daily) = %d, want 7200", got)
	}
}

func TestRandomizedDelaySec(t *testing.T) {
	got := randomizedDelaySec(ScheduleSpec{Kind: ScheduleHourly})
	if got < 0 || got > 120 {
		t.Errorf("randomizedDelaySec(hourly) = %d, want [0, 120]", got)
	}
}
