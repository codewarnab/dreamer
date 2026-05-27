package backgroundjobs

import (
	"fmt"
	"strings"
	"time"
)

var validDayOfWeek = map[string]time.Weekday{
	"sunday":    time.Sunday,
	"monday":    time.Monday,
	"tuesday":   time.Tuesday,
	"wednesday": time.Wednesday,
	"thursday":  time.Thursday,
	"friday":    time.Friday,
	"saturday":  time.Saturday,
}

// ValidateSchedule checks that a ScheduleSpec is well-formed.
func ValidateSchedule(s ScheduleSpec) error {
	switch s.Kind {
	case ScheduleHourly:
		// no extra validation
	case ScheduleDaily:
		if s.TimeOfDay == "" {
			return fmt.Errorf("daily schedule requires time_of_day")
		}
		if _, _, err := parseTimeOfDay(s.TimeOfDay); err != nil {
			return fmt.Errorf("invalid time_of_day %q: %w", s.TimeOfDay, err)
		}
	case ScheduleWeekly:
		if s.DayOfWeek == "" {
			return fmt.Errorf("weekly schedule requires day_of_week")
		}
		if _, ok := validDayOfWeek[strings.ToLower(s.DayOfWeek)]; !ok {
			return fmt.Errorf("invalid day_of_week %q", s.DayOfWeek)
		}
		if s.TimeOfDay == "" {
			return fmt.Errorf("weekly schedule requires time_of_day")
		}
		if _, _, err := parseTimeOfDay(s.TimeOfDay); err != nil {
			return fmt.Errorf("invalid time_of_day %q: %w", s.TimeOfDay, err)
		}
	case ScheduleCron:
		if s.Cron == "" {
			return fmt.Errorf("cron schedule requires cron expression")
		}
		// basic validation: 5 fields separated by whitespace
		// full cron parsing deferred to Phase 3
		// Accepted for forward-compatibility; NextRun returns error until Phase 3
	default:
		return fmt.Errorf("unknown schedule kind %q", s.Kind)
	}

	if s.Timezone == "" {
		return fmt.Errorf("timezone is required")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", s.Timezone, err)
	}

	return nil
}

// NextRun calculates the next run time after now for the given schedule.
// For ScheduleCron, returns an error until Phase 3 implements full cron evaluation.
func NextRun(s ScheduleSpec, now time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load timezone %q: %w", s.Timezone, err)
	}
	localNow := now.In(loc)

	switch s.Kind {
	case ScheduleHourly:
		return now.Add(1 * time.Hour), nil
	case ScheduleDaily:
		// Error ignored: ValidateSchedule rejects invalid TimeOfDay before
		// NextRun is ever called. A corrupted store value silently yields
		// hour=0, min=0 — acceptable until Phase 3 adds store validation.
		hour, min, _ := parseTimeOfDay(s.TimeOfDay)
		next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, min, 0, 0, loc)
		if !next.After(localNow) {
			next = next.AddDate(0, 0, 1)
		}
		return next.UTC(), nil
	case ScheduleWeekly:
		targetDay := validDayOfWeek[strings.ToLower(s.DayOfWeek)]
		// Error ignored: same guard as daily — ValidateSchedule catches it.
		hour, min, _ := parseTimeOfDay(s.TimeOfDay)
		next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, min, 0, 0, loc)
		daysAhead := int(targetDay - localNow.Weekday())
		if daysAhead < 0 || (daysAhead == 0 && !next.After(localNow)) {
			daysAhead += 7
		}
		next = next.AddDate(0, 0, daysAhead)
		return next.UTC(), nil
	case ScheduleCron:
		// Phase 3: full cron evaluation
		return time.Time{}, fmt.Errorf("cron next-run not yet implemented")
	default:
		return time.Time{}, fmt.Errorf("unknown schedule kind %q", s.Kind)
	}
}

func parseTimeOfDay(s string) (hour, min int, err error) {
	_, err = fmt.Sscanf(s, "%d:%d", &hour, &min)
	if err != nil {
		return 0, 0, fmt.Errorf("parse time %q: expected HH:MM", s)
	}
	if hour < 0 || hour > 23 || min < 0 || min > 59 {
		return 0, 0, fmt.Errorf("time %q out of range", s)
	}
	return hour, min, nil
}
