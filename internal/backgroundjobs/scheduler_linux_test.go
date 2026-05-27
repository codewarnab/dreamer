//go:build linux

package backgroundjobs

import (
	"strings"
	"testing"

	"dreamer/internal/logging"
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
		{"invalid", "Mon"},
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
	if got != 3300 { // 55 minutes
		t.Errorf("timeoutSec(hourly) = %d, want 3300", got)
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
	if got != 300 { // 5 minutes
		t.Errorf("randomizedDelaySec(hourly) = %d, want 300", got)
	}
}

// B4: Service unit should quote paths with spaces.
func TestBuildServiceUnit_PathsWithSpaces(t *testing.T) {
	s := &linuxScheduler{
		cfg: SchedulerConfig{
			StoreDir:       "/home/My User/.dreamer/background-jobs",
			ExecutablePath: "/home/My User/.local/bin/dreamer",
			ConfigPath:     "/home/My User/.config/dreamer/config.yaml",
		},
		logger: logging.Silent(),
	}

	params := ScheduleParams{
		JobID:    "aabbccdd11223344",
		Schedule: ScheduleSpec{Kind: ScheduleHourly, Timezone: "UTC"},
		Name:     "Test Job",
		Enabled:  true,
	}

	unit := s.buildServiceUnit(params)

	// Verify paths are quoted in ExecStart.
	if !strings.Contains(unit, `ExecStart="/home/My User/.local/bin/dreamer"`) {
		t.Errorf("ExecStart should quote executable path, got: %s", unit)
	}
	if !strings.Contains(unit, `--config "/home/My User/.config/dreamer/config.yaml"`) {
		t.Errorf("ExecStart should quote config path, got: %s", unit)
	}

	// Verify % is escaped as %%.
	s2 := &linuxScheduler{
		cfg: SchedulerConfig{
			StoreDir:       "/home/test/.dreamer",
			ExecutablePath: "/home/test/100%/dreamer",
			ConfigPath:     "/home/test/.config/dreamer/config.yaml",
		},
		logger: logging.Silent(),
	}
	unit2 := s2.buildServiceUnit(params)
	if strings.Contains(unit2, "100%") && !strings.Contains(unit2, "100%%") {
		t.Error("ExecStart should escape % as %%")
	}
}
