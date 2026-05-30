//go:build windows

package backgroundjobs

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
)

const (
	taskFolderPrefix = `\Dreamer\BackgroundJobs\`
	schtasksExe      = "schtasks.exe"
)

type windowsScheduler struct {
	cfg    SchedulerConfig
	logger *logging.Logger
	runCmd func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func newPlatformScheduler(cfg SchedulerConfig, logger *logging.Logger) Scheduler {
	return &windowsScheduler{
		cfg:    cfg,
		logger: logger,
		runCmd: runExternalCommand,
	}
}

// Install creates or replaces a Task Scheduler entry for the job.
func (s *windowsScheduler) Install(ctx context.Context, params ScheduleParams) (OSScheduleState, error) {
	if err := ctx.Err(); err != nil {
		return OSScheduleState{}, err
	}
	if err := os.MkdirAll(s.cfg.StoreDir, fsutil.DirPerms); err != nil {
		return OSScheduleState{}, fmt.Errorf("create store dir: %w", err)
	}

	xmlBytes, err := s.buildTaskXML(params)
	if err != nil {
		return OSScheduleState{}, fmt.Errorf("build task XML: %w", err)
	}

	taskPath := taskFolderPrefix + params.JobID
	if err := s.writeTask(ctx, taskPath, xmlBytes); err != nil {
		return OSScheduleState{}, fmt.Errorf("install task: %w", err)
	}

	specHash, _ := HashScheduleSpec(params.Schedule)
	now := time.Now().UTC()
	return OSScheduleState{
		ScheduleID:     taskPath,
		InstallID:      s.cfg.InstallID,
		ConfigPathHash: s.cfg.ConfigHash,
		ExecPathHash:   s.cfg.ExecHash,
		SpecHash:       specHash,
		LastInstalled:  &now,
	}, nil
}

// Update modifies an existing Task Scheduler entry for the job.
// For Windows, this is idempotent — same as Install with /F flag.
func (s *windowsScheduler) Update(ctx context.Context, params ScheduleParams) (OSScheduleState, error) {
	return s.Install(ctx, params)
}

// Remove deletes the Task Scheduler entry for the job.
func (s *windowsScheduler) Remove(ctx context.Context, jobID string) error {
	if err := ValidateJobID(jobID); err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	taskPath := taskFolderPrefix + jobID
	_, err := s.runCmd(ctx, schtasksExe, "/Delete", "/TN", taskPath, "/F")
	if err != nil {
		if classifyScheduleError(err) == scheduleErrNotFound {
			return nil // already gone — idempotent
		}
		return fmt.Errorf("delete task %q: %w", taskPath, classifyScheduleError(err))
	}
	return nil
}

// Inspect returns the OS-level health for a job's schedule.
func (s *windowsScheduler) Inspect(ctx context.Context, jobID string) (ScheduleHealth, error) {
	taskPath := taskFolderPrefix + jobID
	output, err := s.runCmd(ctx, schtasksExe, "/Query", "/TN", taskPath, "/XML")
	if err != nil {
		cat := classifyScheduleError(err)
		if cat == scheduleErrNotFound {
			return ScheduleHealth{Installed: false}, nil
		}
		return ScheduleHealth{Installed: false}, fmt.Errorf("inspect task %q: %w", taskPath, cat)
	}

	health := ScheduleHealth{Installed: true}
	outputStr := string(output)
	if strings.Contains(outputStr, "<Enabled>false</Enabled>") {
		health.Enabled = false
	} else {
		health.Enabled = true
	}

	if nextRun := extractXMLField(outputStr, "NextRunTime"); nextRun != "" {
		if t, parseErr := parseWindowsTime(nextRun); parseErr == nil {
			health.NextRunTime = &t
		} else {
			health.Detail = "next_run: " + nextRun
		}
	}
	if lastRun := extractXMLField(outputStr, "LastRunTime"); lastRun != "" {
		if t, parseErr := parseWindowsTime(lastRun); parseErr == nil {
			health.LastRunTime = &t
		}
	}

	return health, nil
}

// ListOwn returns job IDs of schedules owned by this Dreamer installation.
func (s *windowsScheduler) ListOwn(ctx context.Context) ([]string, error) {
	// Use /XML to avoid localized "TaskName:" field labels on non-English Windows (B16).
	output, err := s.runCmd(ctx, schtasksExe, "/Query", "/TN", `\Dreamer\BackgroundJobs`, "/XML")
	if err != nil {
		return nil, nil // folder doesn't exist
	}

	var jobIDs []string
	seen := make(map[string]bool)
	xmlStr := string(output)
	prefix := `\Dreamer\BackgroundJobs\`
	for {
		idx := strings.Index(xmlStr, "<URI>")
		if idx == -1 {
			break
		}
		xmlStr = xmlStr[idx+5:]
		endIdx := strings.Index(xmlStr, "</URI>")
		if endIdx == -1 {
			break
		}
		uri := xmlStr[:endIdx]
		xmlStr = xmlStr[endIdx:]
		if strings.HasPrefix(uri, prefix) {
			jobID := strings.TrimPrefix(uri, prefix)
			// B15: Validate to prevent path traversal via crafted task names.
			if jobID != "" && !seen[jobID] && ValidateJobID(jobID) == nil {
				jobIDs = append(jobIDs, jobID)
				seen[jobID] = true
			}
		}
	}
	return jobIDs, nil
}

// writeTask writes XML to a temp file and calls schtasks /Create /XML.
func (s *windowsScheduler) writeTask(ctx context.Context, taskPath string, xmlBytes []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp("", "dreamer-task-*.xml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(xmlBytes); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	_, err = s.runCmd(ctx, schtasksExe, "/Create", "/TN", taskPath, "/XML", tmpFile.Name(), "/F")
	if err != nil {
		_, detail := classifyScheduleErrorWithDetail(err)
		return fmt.Errorf("schtasks /Create: %w", detail)
	}
	return nil
}

// taskXMLData holds template data for XML generation.
type taskXMLData struct {
	Description     string
	Enabled         string
	TimeLimit       string
	ExecutablePath  string
	Arguments       string
	WorkingDir      string
	TriggerXML      string
}

// buildTaskXML generates Windows Task Scheduler XML for a job.
func (s *windowsScheduler) buildTaskXML(params ScheduleParams) ([]byte, error) {
	specHash := specHashOrEmpty(params.Schedule)
	description := xmlEscapeText(fmt.Sprintf("dreamer:job_id=%s;install_id=%s;config_hash=%s;spec_hash=%s;exec_hash=%s",
		stripControlChars(params.JobID),
		stripControlChars(s.cfg.InstallID),
		stripControlChars(s.cfg.ConfigHash),
		stripControlChars(specHash),
		stripControlChars(s.cfg.ExecHash),
	))

	enabledStr := "true"
	if !params.Enabled {
		enabledStr = "false"
	}

	triggerXML, err := buildTriggerXML(params.Schedule)
	if err != nil {
		return nil, fmt.Errorf("build trigger: %w", err)
	}

	data := taskXMLData{
		Description:    description,
		Enabled:        enabledStr,
		TimeLimit:      executionTimeLimit(params.Schedule),
		ExecutablePath: xmlEscapeText(s.cfg.ExecutablePath),
		Arguments:      xmlEscapeText(fmt.Sprintf("jobs run %s --config %s", params.JobID, s.cfg.ConfigPath)),
		WorkingDir:     xmlEscapeText(s.cfg.StoreDir),
		TriggerXML:     triggerXML,
	}

	tmpl, err := template.New("task").Parse(taskXMLTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	// Note: xml.Header is intentionally omitted. schtasks.exe requires
	// UTF-16LE when an XML declaration with encoding="UTF-8" is present.
	// Omitting the declaration lets schtasks accept UTF-8 bytes as-is.
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return buf.Bytes(), nil
}

const taskXMLTemplate = `<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>{{.Description}}</Description>
  </RegistrationInfo>
  <Triggers>
    {{.TriggerXML}}
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>{{.Enabled}}</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>{{.TimeLimit}}</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>{{.ExecutablePath}}</Command>
      <Arguments>{{.Arguments}}</Arguments>
      <WorkingDirectory>{{.WorkingDir}}</WorkingDirectory>
    </Exec>
  </Actions>
</Task>`

// buildTriggerXML returns the trigger XML fragment for a schedule kind.
func buildTriggerXML(spec ScheduleSpec) (string, error) {
	switch spec.Kind {
	case ScheduleInterval:
		interval := "PT1H"
		if spec.Every != "" {
			if d, err := parseEveryDuration(spec.Every); err == nil {
				interval = durationToISO8601(d)
			}
		}
		return fmt.Sprintf(`<CalendarTrigger>
      <StartBoundary>2026-01-01T00:00:00</StartBoundary>
      <Enabled>true</Enabled>
      <Repetition>
        <Interval>%s</Interval>
      </Repetition>
      <ScheduleByDay>
        <DaysInterval>1</DaysInterval>
      </ScheduleByDay>
    </CalendarTrigger>`, interval), nil

	case ScheduleDaily:
		hour, min, err := parseTimeOfDay(spec.TimeOfDay)
		if err != nil {
			return "", fmt.Errorf("invalid time_of_day %q: %w", spec.TimeOfDay, err)
		}
		return fmt.Sprintf(`<CalendarTrigger>
      <StartBoundary>2026-01-01T%02d:%02d:00</StartBoundary>
      <Enabled>true</Enabled>
      <ScheduleByDay>
        <DaysInterval>1</DaysInterval>
      </ScheduleByDay>
    </CalendarTrigger>`, hour, min), nil

	case ScheduleWeekly:
		hour, min, err := parseTimeOfDay(spec.TimeOfDay)
		if err != nil {
			return "", fmt.Errorf("invalid time_of_day %q: %w", spec.TimeOfDay, err)
		}
		dayElement := weekdayToXMLElement(strings.ToLower(spec.DayOfWeek))
		return fmt.Sprintf(`<CalendarTrigger>
      <StartBoundary>2026-01-01T%02d:%02d:00</StartBoundary>
      <Enabled>true</Enabled>
      <ScheduleByWeek>
        <WeeksInterval>1</WeeksInterval>
        <DaysOfWeek>
          <%s/>
        </DaysOfWeek>
      </ScheduleByWeek>
    </CalendarTrigger>`, hour, min, dayElement), nil

	case ScheduleCron:
		return "", fmt.Errorf("cron schedules cannot be expressed as Windows Task Scheduler triggers; use daily or weekly instead")

	default:
		return "", fmt.Errorf("unsupported schedule kind %q", spec.Kind)
	}
}

// weekdayToXMLElement converts a lowercase weekday name to a Task Scheduler XML element name.
func weekdayToXMLElement(day string) string {
	switch day {
	case "sunday":
		return "Sunday"
	case "monday":
		return "Monday"
	case "tuesday":
		return "Tuesday"
	case "wednesday":
		return "Wednesday"
	case "thursday":
		return "Thursday"
	case "friday":
		return "Friday"
	case "saturday":
		return "Saturday"
	default:
		return "Monday"
	}
}

// stripControlChars removes control characters and newlines from text.
// Does NOT escape XML special characters (<, >, &, ", ') — callers
// must use xmlEscapeText for user-controlled content.
func stripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		// Keep valid XML whitespace (\t, \n, \r) but strip all other control chars.
		if r == '\t' || r == '\n' || r == '\r' {
			return r
		}
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, s)
}

// xmlEscapeText escapes XML special characters using encoding/xml.
func xmlEscapeText(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}

// extractXMLField extracts the text content of an XML element by tag name.
func extractXMLField(xmlStr, tagName string) string {
	startTag := "<" + tagName + ">"
	endTag := "</" + tagName + ">"
	startIdx := strings.Index(xmlStr, startTag)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(startTag)
	endIdx := strings.Index(xmlStr[startIdx:], endTag)
	if endIdx == -1 {
		return ""
	}
	return strings.TrimSpace(xmlStr[startIdx : startIdx+endIdx])
}

// parseWindowsTime parses common Windows Task Scheduler time formats.
func parseWindowsTime(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.999999999",
		time.RFC3339,
		"1/2/2006 3:04:05 PM",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable time %q", s)
}

// specHashOrEmpty returns the spec hash or empty string on error.
func specHashOrEmpty(spec ScheduleSpec) string {
	h, err := HashScheduleSpec(spec)
	if err != nil {
		return ""
	}
	return h
}

// scheduleErrorCategory classifies schtasks errors for user-friendly messages.
type scheduleErrorCategory int

const (
	scheduleErrNone       scheduleErrorCategory = iota // nil error (no error)
	scheduleErrPermission                              // access denied
	scheduleErrNotFound                                // task doesn't exist
	scheduleErrXML                                     // XML/validation error
	scheduleErrUnknown                                 // unrecognized
)

func (c scheduleErrorCategory) Error() string {
	switch c {
	case scheduleErrNone:
		return "no error"
	case scheduleErrPermission:
		return "access denied — try running as administrator, or check Task Scheduler permissions"
	case scheduleErrNotFound:
		return "task not found"
	case scheduleErrXML:
		return "invalid task definition — run 'dreamer jobs reconcile' to repair"
	default:
		return "unknown scheduler error"
	}
}

// classifyScheduleError inspects a schtasks error and returns a categorized
// error with a user-friendly message. Falls back to the original error if
// unrecognized.
func classifyScheduleError(err error) scheduleErrorCategory {
	if err == nil {
		return scheduleErrNone
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "access is denied") || strings.Contains(msg, "permission"):
		return scheduleErrPermission
	case strings.Contains(msg, "the system cannot find the file specified") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "does not exist"):
		return scheduleErrNotFound
	case strings.Contains(msg, "xml") || strings.Contains(msg, "invalid"):
		return scheduleErrXML
	default:
		return scheduleErrUnknown
	}
}

// classifyScheduleErrorWithDetail inspects a schtasks error and returns both
// the category and a wrapped error that preserves the original message.
func classifyScheduleErrorWithDetail(err error) (scheduleErrorCategory, error) {
	if err == nil {
		return scheduleErrNone, nil
	}
	cat := classifyScheduleError(err)
	return cat, fmt.Errorf("%w: %s", cat, err.Error())
}
