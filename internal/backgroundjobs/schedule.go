package backgroundjobs

import (
	"fmt"
	"slices"
	"strconv"
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
	case ScheduleInterval:
		if s.Every != "" {
			if _, err := parseEveryDuration(s.Every); err != nil {
				return fmt.Errorf("invalid every %q: %w", s.Every, err)
			}
		}
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
		if _, err := parseCron(s.Cron); err != nil {
			return fmt.Errorf("invalid cron expression %q: %w", s.Cron, err)
		}
	default:
		return fmt.Errorf("unknown schedule kind %q", s.Kind)
	}

	if s.Every != "" && s.Kind != ScheduleInterval {
		return fmt.Errorf("--every is only valid with interval schedule")
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
func NextRun(s ScheduleSpec, now time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load timezone %q: %w", s.Timezone, err)
	}
	localNow := now.In(loc)

	switch s.Kind {
	case ScheduleInterval:
		interval := 1 * time.Hour
		if s.Every != "" {
			if d, err := parseEveryDuration(s.Every); err == nil {
				interval = d
			}
		}
		return now.Add(interval), nil
	case ScheduleDaily:
		hour, min, err := parseTimeOfDay(s.TimeOfDay)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid time_of_day %q: %w", s.TimeOfDay, err)
		}
		next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, min, 0, 0, loc)
		if !next.After(localNow) {
			next = next.AddDate(0, 0, 1)
		}
		return next.UTC(), nil
	case ScheduleWeekly:
		targetDay := validDayOfWeek[strings.ToLower(s.DayOfWeek)]
		hour, min, err := parseTimeOfDay(s.TimeOfDay)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid time_of_day %q: %w", s.TimeOfDay, err)
		}
		next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, min, 0, 0, loc)
		daysAhead := int(targetDay - localNow.Weekday())
		if daysAhead < 0 || (daysAhead == 0 && !next.After(localNow)) {
			daysAhead += 7
		}
		next = next.AddDate(0, 0, daysAhead)
		return next.UTC(), nil
	case ScheduleCron:
		return nextCronRun(s.Cron, localNow, loc)
	default:
		return time.Time{}, fmt.Errorf("unknown schedule kind %q", s.Kind)
	}
}

func parseTimeOfDay(s string) (hour, min int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("parse time %q: expected HH:MM", s)
	}
	return t.Hour(), t.Minute(), nil
}

// cronFields holds the parsed fields of a 5-field cron expression.
// Each field is a sorted slice of valid values.
type cronFields struct {
	minute []int // 0-59
	hour   []int // 0-23
	dom    []int // 1-31
	month  []int // 1-12
	dow    []int // 0-6 (0=Sunday)
}

// parseCron validates and parses a 5-field cron expression.
// Supported syntax: *, */N, N, N-M, N,M. No whitespace in field values.
func parseCron(expr string) (cronFields, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return cronFields{}, fmt.Errorf("expected 5 fields, got %d", len(fields))
	}

	mins, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return cronFields{}, fmt.Errorf("minute: %w", err)
	}
	hours, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return cronFields{}, fmt.Errorf("hour: %w", err)
	}
	doms, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return cronFields{}, fmt.Errorf("day-of-month: %w", err)
	}
	months, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return cronFields{}, fmt.Errorf("month: %w", err)
	}
	dows, err := parseCronField(fields[4], 0, 6)
	if err != nil {
		return cronFields{}, fmt.Errorf("day-of-week: %w", err)
	}

	return cronFields{minute: mins, hour: hours, dom: doms, month: months, dow: dows}, nil
}

// parseCronField parses a single cron field into a sorted slice of valid values.
// Supports: *, */N, N, N-M, N,M.
func parseCronField(field string, lo, hi int) ([]int, error) {
	if field == "*" {
		return cronRange(lo, hi), nil
	}
	if strings.HasPrefix(field, "*/") {
		return parseCronStep(field[2:], lo, hi)
	}
	return parseCronParts(field, lo, hi)
}

// cronRange returns all integers in [lo, hi].
func cronRange(lo, hi int) []int {
	result := make([]int, hi-lo+1)
	for i := range result {
		result[i] = lo + i
	}
	return result
}

// parseCronStep handles */N step syntax.
func parseCronStep(stepStr string, lo, hi int) ([]int, error) {
	step, err := parseCronInt(stepStr)
	if err != nil || step < 1 {
		return nil, fmt.Errorf("invalid step %q", stepStr)
	}
	var result []int
	for i := lo; i <= hi; i += step {
		result = append(result, i)
	}
	return result, nil
}

// parseCronParts handles comma-separated parts (N, N-M, N,M).
func parseCronParts(field string, lo, hi int) ([]int, error) {
	parts := strings.Split(field, ",")
	var result []int
	seen := make(map[int]bool)
	for _, part := range parts {
		vals, err := parseCronPart(strings.TrimSpace(part), lo, hi)
		if err != nil {
			return nil, err
		}
		for _, v := range vals {
			if !seen[v] {
				result = append(result, v)
				seen[v] = true
			}
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("empty field")
	}
	slices.Sort(result)
	return result, nil
}

// parseCronPart handles a single part: either a range (N-M) or a single value (N).
func parseCronPart(part string, lo, hi int) ([]int, error) {
	if strings.Contains(part, "-") {
		return parseCronRange(part, lo, hi)
	}
	val, err := parseCronInt(part)
	if err != nil {
		return nil, fmt.Errorf("invalid value %q: %w", part, err)
	}
	if val < lo || val > hi {
		return nil, fmt.Errorf("value %d out of bounds [%d,%d]", val, lo, hi)
	}
	return []int{val}, nil
}

// parseCronRange handles N-M range syntax.
func parseCronRange(part string, lo, hi int) ([]int, error) {
	bounds := strings.SplitN(part, "-", 2)
	if len(bounds) != 2 {
		return nil, fmt.Errorf("invalid range %q", part)
	}
	rangeStart, err := parseCronInt(bounds[0])
	if err != nil {
		return nil, fmt.Errorf("invalid range start %q: %w", bounds[0], err)
	}
	rangeEnd, err := parseCronInt(bounds[1])
	if err != nil {
		return nil, fmt.Errorf("invalid range end %q: %w", bounds[1], err)
	}
	if rangeStart < lo || rangeEnd > hi || rangeStart > rangeEnd {
		return nil, fmt.Errorf("range %d-%d out of bounds [%d,%d]", rangeStart, rangeEnd, lo, hi)
	}
	result := make([]int, rangeEnd-rangeStart+1)
	for i := range result {
		result[i] = rangeStart + i
	}
	return result, nil
}

// parseCronInt parses a decimal integer from a string. Rejects trailing junk.
func parseCronInt(s string) (int, error) {
	val, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse int %q", s)
	}
	return val, nil
}

// nextCronRun finds the next run time for a cron expression after 'now'.
// Searches up to 366 days into the future to avoid infinite loops on rare expressions.
func nextCronRun(expr string, now time.Time, loc *time.Location) (time.Time, error) {
	fields, err := parseCron(expr)
	if err != nil {
		return time.Time{}, err
	}

	domWildcard := isWildcard(fields.dom, 1, 31)
	dowWildcard := isWildcard(fields.dow, 0, 6)

	// Start from the next minute boundary.
	candidate := now.Truncate(time.Minute).Add(time.Minute)

	for day := 0; day < 366; day++ {
		checkDate := candidate.AddDate(0, 0, day)

		// Check month.
		if !slices.Contains(fields.month, int(checkDate.Month())) {
			continue
		}

		// Day matching follows standard cron semantics:
		// - If both dom and dow are wildcards: match every day.
		// - If only one is restricted: match when the restricted one matches.
		// - If both are restricted: match when EITHER matches (OR logic).
		dayMatch := false
		switch {
		case domWildcard && dowWildcard:
			dayMatch = true
		case domWildcard && !dowWildcard:
			dayMatch = slices.Contains(fields.dow, int(checkDate.Weekday()))
		case !domWildcard && dowWildcard:
			dayMatch = slices.Contains(fields.dom, checkDate.Day())
		default:
			dayMatch = slices.Contains(fields.dom, checkDate.Day()) ||
				slices.Contains(fields.dow, int(checkDate.Weekday()))
		}
		if !dayMatch {
			continue
		}

		// Find the next valid hour:minute on this day.
		for _, h := range fields.hour {
			for _, m := range fields.minute {
				candidateTime := time.Date(
					checkDate.Year(), checkDate.Month(), checkDate.Day(),
					h, m, 0, 0, loc,
				)
				if candidateTime.After(now) {
					return candidateTime.UTC(), nil
				}
			}
		}
	}

	return time.Time{}, fmt.Errorf("no matching cron time in next 366 days")
}

// isWildcard checks if a cron field contains every value in the range [lo, hi].
func isWildcard(values []int, lo, hi int) bool {
	return len(values) == hi-lo+1 && values[0] == lo && values[len(values)-1] == hi
}

// DefaultTimeoutFor derives a session timeout from the schedule.
// For interval schedules, uses 80% of the interval capped at 1 hour.
// For daily/weekly/cron, returns 30 minutes.
func DefaultTimeoutFor(spec ScheduleSpec) time.Duration {
	const (
		hardCap      = 1 * time.Hour
		dailyDefault = 30 * time.Minute
	)
	switch spec.Kind {
	case ScheduleInterval:
		interval := EveryDuration(spec)
		timeout := time.Duration(float64(interval) * 0.8)
		if timeout > hardCap {
			timeout = hardCap
		}
		if timeout < 1*time.Minute {
			timeout = 1 * time.Minute
		}
		return timeout
	default:
		return dailyDefault
	}
}

// executionTimeLimit returns the PT duration string for a schedule's time limit.
// Shared across platforms — used by Windows Task Scheduler and systemd timeout.
func executionTimeLimit(spec ScheduleSpec) string {
	switch spec.Kind {
	case ScheduleInterval:
		if spec.Every != "" {
			if d, err := parseEveryDuration(spec.Every); err == nil {
				// Cap at the interval so the job finishes before the next trigger.
				return durationToISO8601(d)
			}
		}
		return "PT55M"
	case ScheduleDaily, ScheduleWeekly:
		return "PT2H"
	case ScheduleCron:
		return "PT2H"
	default:
		return "PT1H"
	}
}

// parseEveryDuration parses and validates an --every duration string.
// Accepts Go duration strings (e.g. "5m", "15m", "2h", "90m").
// Rejects values < 1 minute and > 23 hours.
func parseEveryDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: expected Go duration like 5m, 15m, 2h", s)
	}
	if d < 1*time.Minute {
		return 0, fmt.Errorf("minimum interval is 1m, got %v", d)
	}
	if d > 23*time.Hour {
		return 0, fmt.Errorf("maximum interval is 23h, got %v", d)
	}
	return d, nil
}

// EveryDuration returns the parsed Every duration, or 1 hour as default.
func EveryDuration(spec ScheduleSpec) time.Duration {
	if spec.Every != "" {
		if d, err := parseEveryDuration(spec.Every); err == nil {
			return d
		}
	}
	return 1 * time.Hour
}

// durationToISO8601 converts a Go duration to ISO 8601 format (PT{N}H or PT{N}M).
func durationToISO8601(d time.Duration) string {
	totalMinutes := int(d.Minutes())
	if totalMinutes >= 60 && totalMinutes%60 == 0 {
		return fmt.Sprintf("PT%dH", totalMinutes/60)
	}
	return fmt.Sprintf("PT%dM", totalMinutes)
}
