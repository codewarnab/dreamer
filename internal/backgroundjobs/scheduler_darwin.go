//go:build darwin

package backgroundjobs

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
)

const launchAgentDir = "Library/LaunchAgents"

type darwinScheduler struct {
	cfg    SchedulerConfig
	logger *logging.Logger
	runCmd func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func newPlatformScheduler(cfg SchedulerConfig, logger *logging.Logger) Scheduler {
	return &darwinScheduler{
		cfg:    cfg,
		logger: logger,
		runCmd: runExternalCommand,
	}
}

// Install creates a LaunchAgent plist for the job.
func (s *darwinScheduler) Install(ctx context.Context, params ScheduleParams) (OSScheduleState, error) {
	if err := ctx.Err(); err != nil {
		return OSScheduleState{}, err
	}

	plistPath, err := s.writePlist(params)
	if err != nil {
		return OSScheduleState{}, fmt.Errorf("write plist: %w", err)
	}

	// Always bootstrap — launchctl is idempotent for existing agents.
	if err := s.bootstrap(ctx, plistPath); err != nil {
		return OSScheduleState{}, fmt.Errorf("bootstrap: %w", err)
	}

	if params.Enabled {
		if err := s.enable(ctx, params.JobID); err != nil {
			return OSScheduleState{}, fmt.Errorf("enable: %w", err)
		}
	}

	specHash, _ := HashScheduleSpec(params.Schedule)
	now := time.Now().UTC()
	return OSScheduleState{
		ScheduleID:     s.label(params.JobID),
		InstallID:      s.cfg.InstallID,
		ConfigPathHash: s.cfg.ConfigHash,
		ExecPathHash:   s.cfg.ExecHash,
		SpecHash:       specHash,
		LastInstalled:  &now,
	}, nil
}

// Update modifies an existing LaunchAgent plist for the job.
func (s *darwinScheduler) Update(ctx context.Context, params ScheduleParams) (OSScheduleState, error) {
	return s.Install(ctx, params)
}

// Remove unloads and deletes the LaunchAgent plist for the job.
func (s *darwinScheduler) Remove(ctx context.Context, jobID string) error {
	if err := ValidateJobID(jobID); err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	plistPath := s.plistPath(jobID)

	// bootout errors are non-fatal (agent may not be loaded), but log for diagnostics.
	if _, bootErr := s.runCmd(ctx, "launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid()), plistPath); bootErr != nil {
		s.logger.Debug("launchctl bootout (non-fatal)", logging.String("job_id", jobID), logging.Any("error", bootErr))
	}

	os.Remove(plistPath)
	return nil
}

// Inspect returns the OS-level health for a job's schedule.
func (s *darwinScheduler) Inspect(_ context.Context, jobID string) (ScheduleHealth, error) {
	plistPath := s.plistPath(jobID)
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return ScheduleHealth{Installed: false}, nil
	}

	health := ScheduleHealth{Installed: true}

	// Parse plist to check enabled status and extract StartCalendarInterval.
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return health, fmt.Errorf("read plist: %w", err)
	}

	var plist launchAgentPlist
	if err := xml.Unmarshal(data, &plist); err != nil {
		return health, fmt.Errorf("parse plist: %w", err)
	}

	health.Enabled = !plist.Disabled

	if plist.StartInterval > 0 {
		// Interval-based: next run is now + interval (approximate).
		next := time.Now().Add(time.Duration(plist.StartInterval) * time.Second)
		health.NextRunTime = &next
	} else if len(plist.StartCalendarInterval) > 0 {
		cal := plist.StartCalendarInterval[0]
		now := time.Now()
		next := computeNextCalendarRun(now, cal.Hour, cal.Minute, cal.Weekday)
		health.NextRunTime = &next
	}

	return health, nil
}

// ListOwn returns job IDs of Dreamer LaunchAgents owned by this installation.
func (s *darwinScheduler) ListOwn(_ context.Context) ([]string, error) {
	agentDir, err := s.agentDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(agentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agent dir: %w", err)
	}

	prefix := "com.dreamer.background."
	var jobIDs []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".plist") {
			jobID := strings.TrimPrefix(name, prefix)
			jobID = strings.TrimSuffix(jobID, ".plist")
			// B15: Validate to prevent path traversal via crafted filenames.
			if jobID != "" && ValidateJobID(jobID) == nil {
				jobIDs = append(jobIDs, jobID)
			}
		}
	}
	return jobIDs, nil
}

// label returns the LaunchAgent label for a job.
func (s *darwinScheduler) label(jobID string) string {
	return "com.dreamer.background." + jobID
}

// plistPath returns the full path to the LaunchAgent plist for a job.
func (s *darwinScheduler) plistPath(jobID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, launchAgentDir, s.label(jobID)+".plist")
}

// agentDir returns the LaunchAgents directory path.
func (s *darwinScheduler) agentDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, launchAgentDir), nil
}

// writePlist generates and writes the LaunchAgent plist file.
func (s *darwinScheduler) writePlist(params ScheduleParams) (string, error) {
	agentDir, err := s.agentDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(agentDir, fsutil.DirPerms); err != nil {
		return "", fmt.Errorf("create agent dir: %w", err)
	}

	plistPath := filepath.Join(agentDir, s.label(params.JobID)+".plist")

	calIntervals, err := buildCalendarIntervals(params.Schedule)
	if err != nil {
		return "", fmt.Errorf("build calendar intervals: %w", err)
	}
	disabled := !params.Enabled
	startInterval := 0
	if params.Schedule.Kind == ScheduleInterval && params.Schedule.Every != "" {
		startInterval = int(EveryDuration(params.Schedule).Seconds())
	}

	plist := launchAgentPlist{
		Label:                 s.label(params.JobID),
		ProgramArguments:      append([]string{s.cfg.ExecutablePath}, runArgsFor(params.JobID, s.cfg.ConfigPath)...),
		WorkingDirectory:      s.cfg.StoreDir,
		StartCalendarInterval: calIntervals,
		StartInterval:         startInterval,
		StandardOutPath:       filepath.Join(s.cfg.StoreDir, params.JobID+".stdout.log"),
		StandardErrorPath:     filepath.Join(s.cfg.StoreDir, params.JobID+".stderr.log"),
		Disabled:              disabled,
	}

	data, err := xml.MarshalIndent(plist, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal plist: %w", err)
	}

	content := xml.Header + string(data) + "\n"
	if err := os.WriteFile(plistPath, []byte(content), fsutil.FilePerms); err != nil {
		return "", fmt.Errorf("write plist: %w", err)
	}

	return plistPath, nil
}

// bootstrap loads the agent into launchd.
func (s *darwinScheduler) bootstrap(ctx context.Context, plistPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	out, err := s.runCmd(ctx, "launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), plistPath)
	// "already bootstrapped" is not an error. launchctl writes the message
	// to stderr; CombinedOutput (the default runCmd implementation) merges
	// stdout+stderr, so search the output bytes, not err.Error().
	if err != nil && !strings.Contains(string(out), "already bootstrapped") {
		return err
	}
	return nil
}

// enable sets Disabled=false and kicks the agent.
func (s *darwinScheduler) enable(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, _ = s.runCmd(ctx, "launchctl", "enable", "gui/"+strconv.Itoa(os.Getuid())+"/"+s.label(jobID))
	return nil
}

// launchAgentPlist represents a macOS LaunchAgent plist.
type launchAgentPlist struct {
	XMLName               xml.Name           `xml:"plist"`
	Version               string             `xml:"version,attr"`
	Dict                  plistDict          `xml:"dict"`
	Label                 string             `xml:"-"`
	ProgramArguments      []string           `xml:"-"`
	WorkingDirectory      string             `xml:"-"`
	StartCalendarInterval []calendarInterval `xml:"-"`
	StartInterval         int                `xml:"-"` // seconds; 0 means unused
	StandardOutPath       string             `xml:"-"`
	StandardErrorPath     string             `xml:"-"`
	Disabled              bool               `xml:"-"`
}

// plistDict is a plist dictionary with ordered key-value pairs.
type plistDict struct {
	Keys   []string `xml:"key"`
	Values []any    `xml:",any"`
}

// calendarInterval represents a StartCalendarInterval entry.
// Weekday uses -1 as sentinel for "not specified" (0=Sunday in launchd).
type calendarInterval struct {
	Hour    int `xml:"Hour"`
	Minute  int `xml:"Minute"`
	Weekday int `xml:"Weekday,omitempty"`
}

// writePlistKey writes a <key> element to the plist encoder.
func writePlistKey(e *xml.Encoder, key string) error {
	if err := e.EncodeElement(xml.Name{Local: "key"}, xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
		return err
	}
	if err := e.EncodeToken(xml.CharData([]byte(key))); err != nil {
		return err
	}
	return e.EncodeToken(xml.EndElement{Name: xml.Name{Local: "key"}})
}

// writePlistString writes a key-string pair.
func writePlistString(e *xml.Encoder, key, value string) error {
	if err := writePlistKey(e, key); err != nil {
		return err
	}
	return e.EncodeElement(value, xml.StartElement{Name: xml.Name{Local: "string"}})
}

// writePlistBool writes a key-boolean pair.
func writePlistBool(e *xml.Encoder, key string, value bool) error {
	if err := writePlistKey(e, key); err != nil {
		return err
	}
	if value {
		return e.EncodeElement(nil, xml.StartElement{Name: xml.Name{Local: "true"}})
	}
	return e.EncodeElement(nil, xml.StartElement{Name: xml.Name{Local: "false"}})
}

// writePlistInt writes a key-integer pair.
func writePlistInt(e *xml.Encoder, key string, value int) error {
	if err := writePlistKey(e, key); err != nil {
		return err
	}
	return e.EncodeElement(value, xml.StartElement{Name: xml.Name{Local: "integer"}})
}

// writePlistStringArray writes a key-array-of-strings pair.
func writePlistStringArray(e *xml.Encoder, key string, values []string) error {
	if err := writePlistKey(e, key); err != nil {
		return err
	}
	arrStart := xml.StartElement{Name: xml.Name{Local: "array"}}
	if err := e.EncodeToken(arrStart); err != nil {
		return err
	}
	for _, v := range values {
		if err := e.EncodeElement(v, xml.StartElement{Name: xml.Name{Local: "string"}}); err != nil {
			return err
		}
	}
	return e.EncodeToken(xml.EndElement{Name: xml.Name{Local: "array"}})
}

// writePlistCalendarArray writes a key-calendar-intervals pair.
// A single interval is written as a bare dict; multiple as an array of dicts.
func writePlistCalendarArray(e *xml.Encoder, key string, intervals []calendarInterval) error {
	if err := writePlistKey(e, key); err != nil {
		return err
	}

	if len(intervals) == 1 {
		return writePlistCalendarDict(e, intervals[0])
	}

	arrStart := xml.StartElement{Name: xml.Name{Local: "array"}}
	if err := e.EncodeToken(arrStart); err != nil {
		return err
	}
	for _, interval := range intervals {
		if err := writePlistCalendarDict(e, interval); err != nil {
			return err
		}
	}
	return e.EncodeToken(xml.EndElement{Name: xml.Name{Local: "array"}})
}

// writePlistCalendarDict writes a single calendar interval as a dict.
func writePlistCalendarDict(e *xml.Encoder, cal calendarInterval) error {
	dictStart := xml.StartElement{Name: xml.Name{Local: "dict"}}
	if err := e.EncodeToken(dictStart); err != nil {
		return err
	}
	if err := writePlistInt(e, "Hour", cal.Hour); err != nil {
		return err
	}
	if err := writePlistInt(e, "Minute", cal.Minute); err != nil {
		return err
	}
	if cal.Weekday >= 0 {
		if err := writePlistInt(e, "Weekday", cal.Weekday); err != nil {
			return err
		}
	}
	return e.EncodeToken(xml.EndElement{Name: xml.Name{Local: "dict"}})
}

// UnmarshalXML implements xml.Unmarshaler to parse plist XML back into structured fields.
func (p *launchAgentPlist) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	// Skip to the <dict> element inside <plist>.
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "dict" {
			return p.unmarshalDict(d, se)
		}
	}
}

// unmarshalDict reads key-value pairs from a plist <dict> element.
func (p *launchAgentPlist) unmarshalDict(d *xml.Decoder, _ xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok.(type) {
		case xml.EndElement:
			return nil // end of dict
		case xml.StartElement:
			// Should be a <key> element.
		default:
			continue
		}

		// Read key text.
		keyTok, err := d.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(xml.CharData)
		if !ok {
			continue
		}
		keyStr := string(key)
		// Skip the </key> end element.
		if _, err := d.Token(); err != nil {
			return err
		}

		// Read the value.
		valTok, err := d.Token()
		if err != nil {
			return err
		}
		valStart, ok := valTok.(xml.StartElement)
		if !ok {
			continue
		}

		switch keyStr {
		case "Label":
			p.Label, err = readPlistString(d, valStart)
		case "ProgramArguments":
			p.ProgramArguments, err = readPlistStringArray(d, valStart)
		case "WorkingDirectory":
			p.WorkingDirectory, err = readPlistString(d, valStart)
		case "StartCalendarInterval":
			p.StartCalendarInterval, err = readPlistCalendarArray(d, valStart)
		case "StartInterval":
			p.StartInterval, err = readPlistInt(d, valStart)
		case "StandardOutPath":
			p.StandardOutPath, err = readPlistString(d, valStart)
		case "StandardErrorPath":
			p.StandardErrorPath, err = readPlistString(d, valStart)
		case "Disabled":
			p.Disabled, err = readPlistBool(d, valStart)
		default:
			// Skip unknown elements.
			if err := d.Skip(); err != nil {
				return err
			}
		}
		if err != nil {
			return err
		}
	}
}

// readPlistInt reads an <integer> value.
func readPlistInt(d *xml.Decoder, _ xml.StartElement) (int, error) {
	tok, err := d.Token()
	if err != nil {
		return 0, err
	}
	if cd, ok := tok.(xml.CharData); ok {
		n := 0
		fmt.Sscanf(string(cd), "%d", &n)
		// Skip end element.
		if _, err := d.Token(); err != nil {
			return 0, err
		}
		return n, nil
	}
	// End element or empty.
	if _, ok := tok.(xml.EndElement); ok {
		return 0, nil
	}
	return 0, nil
}

// readPlistString reads a <string> value.
func readPlistString(d *xml.Decoder, _ xml.StartElement) (string, error) {
	tok, err := d.Token()
	if err != nil {
		return "", err
	}
	if cd, ok := tok.(xml.CharData); ok {
		// Skip end element.
		if _, err := d.Token(); err != nil {
			return "", err
		}
		return string(cd), nil
	}
	// Empty string or end element.
	if _, ok := tok.(xml.EndElement); ok {
		return "", nil
	}
	return "", nil
}

// readPlistBool reads a <true/> or <false/> value.
func readPlistBool(d *xml.Decoder, start xml.StartElement) (bool, error) {
	// <true/> or <false/> are self-closing or empty elements.
	// Skip to end element.
	if err := d.Skip(); err != nil {
		return false, err
	}
	return start.Name.Local == "true", nil
}

// readPlistStringArray reads an <array> of <string> elements.
func readPlistStringArray(d *xml.Decoder, _ xml.StartElement) ([]string, error) {
	var result []string
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return result, nil
		case xml.StartElement:
			if t.Name.Local == "string" {
				s, err := readPlistString(d, t)
				if err != nil {
					return nil, err
				}
				result = append(result, s)
			} else {
				if err := d.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

// readPlistCalendarArray reads calendar intervals. In plist, a single interval
// is a bare <dict>, multiple are wrapped in <array>.
func readPlistCalendarArray(d *xml.Decoder, start xml.StartElement) ([]calendarInterval, error) {
	if start.Name.Local == "array" {
		// Multiple intervals wrapped in <array>.
		var result []calendarInterval
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.EndElement:
				return result, nil
			case xml.StartElement:
				if t.Name.Local == "dict" {
					cal, err := readPlistCalendarDict(d, t)
					if err != nil {
						return nil, err
					}
					result = append(result, cal)
				} else {
					if err := d.Skip(); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	// Single interval is a bare <dict>.
	cal, err := readPlistCalendarDict(d, start)
	if err != nil {
		return nil, err
	}
	return []calendarInterval{cal}, nil
}

// readPlistCalendarDict reads a single calendar interval <dict>.
func readPlistCalendarDict(d *xml.Decoder, _ xml.StartElement) (calendarInterval, error) {
	var cal calendarInterval
	cal.Weekday = -1 // default sentinel
	for {
		tok, err := d.Token()
		if err != nil {
			return cal, err
		}
		switch tok.(type) {
		case xml.EndElement:
			return cal, nil
		case xml.StartElement:
			// Read key.
			keyTok, err := d.Token()
			if err != nil {
				return cal, err
			}
			key := string(keyTok.(xml.CharData))
			if _, err := d.Token(); err != nil { // skip </key>
				return cal, err
			}
			// Read integer value.
			valTok, err := d.Token()
			if err != nil {
				return cal, err
			}
			if cd, ok := valTok.(xml.CharData); ok {
				n := 0
				fmt.Sscanf(string(cd), "%d", &n)
				switch key {
				case "Hour":
					cal.Hour = n
				case "Minute":
					cal.Minute = n
				case "Weekday":
					cal.Weekday = n
				}
			}
			if _, err := d.Token(); err != nil { // skip end element
				return cal, err
			}
		}
	}
}

// MarshalXML implements xml.Marshaler to produce correct plist XML output.
func (p launchAgentPlist) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	start.Name = xml.Name{Local: "plist"}
	start.Attr = []xml.Attr{{Name: xml.Name{Local: "version"}, Value: "1.0"}}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	if err := e.EncodeToken(xml.Directive(`DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"`)); err != nil {
		return err
	}
	if err := e.EncodeToken(xml.StartElement{Name: xml.Name{Local: "dict"}}); err != nil {
		return err
	}
	if err := writePlistString(e, "Label", p.Label); err != nil {
		return err
	}
	if err := writePlistStringArray(e, "ProgramArguments", p.ProgramArguments); err != nil {
		return err
	}
	if err := writePlistString(e, "WorkingDirectory", p.WorkingDirectory); err != nil {
		return err
	}
	if p.StartInterval > 0 {
		if err := writePlistInt(e, "StartInterval", p.StartInterval); err != nil {
			return err
		}
	} else if len(p.StartCalendarInterval) > 0 {
		if err := writePlistCalendarArray(e, "StartCalendarInterval", p.StartCalendarInterval); err != nil {
			return err
		}
	}
	if err := writePlistString(e, "StandardOutPath", p.StandardOutPath); err != nil {
		return err
	}
	if err := writePlistString(e, "StandardErrorPath", p.StandardErrorPath); err != nil {
		return err
	}
	if err := writePlistBool(e, "Disabled", p.Disabled); err != nil {
		return err
	}
	if err := e.EncodeToken(xml.EndElement{Name: xml.Name{Local: "dict"}}); err != nil {
		return err
	}
	return e.EncodeToken(xml.EndElement{Name: xml.Name{Local: "plist"}})
}

// buildCalendarIntervals converts a ScheduleSpec to plist calendar intervals.
// Returns nil when StartInterval should be used instead (Every is set).
func buildCalendarIntervals(spec ScheduleSpec) ([]calendarInterval, error) {
	switch spec.Kind {
	case ScheduleInterval:
		if spec.Every != "" {
			return nil, nil // StartInterval handles the scheduling
		}
		return []calendarInterval{{Hour: -1, Minute: 0, Weekday: -1}}, nil // every hour at :00
	case ScheduleDaily:
		hour, min, err := parseTimeOfDay(spec.TimeOfDay)
		if err != nil {
			return nil, fmt.Errorf("invalid time_of_day %q: %w", spec.TimeOfDay, err)
		}
		return []calendarInterval{{Hour: hour, Minute: min, Weekday: -1}}, nil
	case ScheduleWeekly:
		hour, min, err := parseTimeOfDay(spec.TimeOfDay)
		if err != nil {
			return nil, fmt.Errorf("invalid time_of_day %q: %w", spec.TimeOfDay, err)
		}
		wd := weekdayToLaunchd(strings.ToLower(spec.DayOfWeek))
		if wd < 0 {
			return nil, fmt.Errorf("invalid day_of_week %q", spec.DayOfWeek)
		}
		return []calendarInterval{{Hour: hour, Minute: min, Weekday: wd}}, nil
	default:
		return []calendarInterval{{Hour: 0, Minute: 0, Weekday: -1}}, nil
	}
}

// weekdayToLaunchd converts a weekday name to launchd's weekday numbering (0=Sunday).
func weekdayToLaunchd(day string) int {
	switch day {
	case "sunday":
		return 0
	case "monday":
		return 1
	case "tuesday":
		return 2
	case "wednesday":
		return 3
	case "thursday":
		return 4
	case "friday":
		return 5
	case "saturday":
		return 6
	default:
		return -1
	}
}

// computeNextCalendarRun computes the next run time for a calendar interval.
func computeNextCalendarRun(now time.Time, hour, minute, weekday int) time.Time {
	if hour < 0 {
		// Hourly: next :00 mark.
		next := now.Truncate(time.Hour).Add(time.Hour)
		return next
	}

	// Daily/weekly: compute next matching time.
	year, month, day := now.Date()
	candidate := time.Date(year, month, day, hour, minute, 0, 0, now.Location())

	if weekday >= 0 {
		// Weekly: find next matching weekday.
		for candidate.Weekday() != time.Weekday(weekday) || !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}
	} else {
		// Daily: if already past today, move to tomorrow.
		if !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}
	}

	return candidate
}
