//go:build darwin

package backgroundjobs

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
)

const launchAgentDir = "Library/LaunchAgents"

type darwinScheduler struct {
	cfg    SchedulerConfig
	logger *logging.Logger
	runCmd func(name string, args ...string) ([]byte, error)
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
	plistPath := s.plistPath(jobID)

	// bootout errors are non-fatal (agent may not be loaded).
	s.runCmd("launchctl", "bootout", "gui/"+os.Getenv("UID"), plistPath)

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

	if len(plist.StartCalendarInterval) > 0 {
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
			if jobID != "" {
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

	calIntervals := buildCalendarIntervals(params.Schedule)
	disabled := !params.Enabled

	plist := launchAgentPlist{
		Label:                s.label(params.JobID),
		ProgramArguments:     []string{s.cfg.ExecutablePath, "jobs", "run", params.JobID, "--config", s.cfg.ConfigPath},
		WorkingDirectory:     s.cfg.StoreDir,
		StartCalendarInterval: calIntervals,
		StandardOutPath:      filepath.Join(s.cfg.StoreDir, params.JobID+".stdout.log"),
		StandardErrorPath:    filepath.Join(s.cfg.StoreDir, params.JobID+".stderr.log"),
		Disabled:             disabled,
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
	_, err := s.runCmd("launchctl", "bootstrap", "gui/"+os.Getenv("UID"), plistPath)
	// "already bootstrapped" is not an error.
	if err != nil && !strings.Contains(string(err.Error()), "already bootstrapped") {
		return err
	}
	return nil
}

// enable sets Disabled=false and kicks the agent.
func (s *darwinScheduler) enable(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, _ = s.runCmd("launchctl", "enable", "gui/"+os.Getenv("UID")+"/"+s.label(jobID))
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
	if err := writePlistCalendarArray(e, "StartCalendarInterval", p.StartCalendarInterval); err != nil {
		return err
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
func buildCalendarIntervals(spec ScheduleSpec) []calendarInterval {
	switch spec.Kind {
	case ScheduleHourly:
		return []calendarInterval{{Hour: -1, Minute: 0, Weekday: -1}} // every hour at :00
	case ScheduleDaily:
		hour, min, _ := parseTimeOfDay(spec.TimeOfDay)
		return []calendarInterval{{Hour: hour, Minute: min, Weekday: -1}}
	case ScheduleWeekly:
		hour, min, _ := parseTimeOfDay(spec.TimeOfDay)
		wd := weekdayToLaunchd(strings.ToLower(spec.DayOfWeek))
		return []calendarInterval{{Hour: hour, Minute: min, Weekday: wd}}
	default:
		return []calendarInterval{{Hour: 0, Minute: 0, Weekday: -1}}
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
		return 1
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
