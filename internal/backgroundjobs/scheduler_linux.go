//go:build linux

package backgroundjobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
)

const systemdUnitDir = ".config/systemd/user"

type linuxScheduler struct {
	cfg    SchedulerConfig
	logger *logging.Logger
	runCmd func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func newPlatformScheduler(cfg SchedulerConfig, logger *logging.Logger) Scheduler {
	return &linuxScheduler{
		cfg:    cfg,
		logger: logger,
		runCmd: runExternalCommand,
	}
}

// Install creates systemd user timer + service unit files for the job.
func (s *linuxScheduler) Install(ctx context.Context, params ScheduleParams) (OSScheduleState, error) {
	if err := ctx.Err(); err != nil {
		return OSScheduleState{}, err
	}

	// systemd OnCalendar syntax is NOT compatible with standard 5-field cron.
	if params.Schedule.Kind == ScheduleCron {
		return OSScheduleState{}, fmt.Errorf("cron schedules cannot be expressed as systemd OnCalendar; use daily or weekly instead")
	}

	unitDir, err := s.ensureUnitDir()
	if err != nil {
		return OSScheduleState{}, err
	}

	baseName := "dreamer-job-" + params.JobID

	timerContent, err := s.buildTimerUnit(params)
	if err != nil {
		return OSScheduleState{}, fmt.Errorf("build timer unit: %w", err)
	}
	serviceContent := s.buildServiceUnit(params)

	if err := writeFileAtomic(filepath.Join(unitDir, baseName+".timer"), []byte(timerContent)); err != nil {
		return OSScheduleState{}, fmt.Errorf("write timer unit: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(unitDir, baseName+".service"), []byte(serviceContent)); err != nil {
		return OSScheduleState{}, fmt.Errorf("write service unit: %w", err)
	}

	if err := s.reloadDaemon(ctx); err != nil {
		return OSScheduleState{}, fmt.Errorf("systemctl --user daemon-reload: %w", err)
	}

	if params.Enabled {
		if _, err := s.runCmd(ctx, "systemctl", "--user", "enable", "--now", baseName+".timer"); err != nil {
			return OSScheduleState{}, fmt.Errorf("enable timer: %w", err)
		}
	}

	specHash, _ := HashScheduleSpec(params.Schedule)
	now := time.Now().UTC()
	return OSScheduleState{
		ScheduleID:     baseName,
		InstallID:      s.cfg.InstallID,
		ConfigPathHash: s.cfg.ConfigHash,
		ExecPathHash:   s.cfg.ExecHash,
		SpecHash:       specHash,
		LastInstalled:  &now,
	}, nil
}

// Update modifies existing systemd unit files for the job.
func (s *linuxScheduler) Update(ctx context.Context, params ScheduleParams) (OSScheduleState, error) {
	return s.Install(ctx, params)
}

// Remove disables and removes systemd unit files for the job.
func (s *linuxScheduler) Remove(ctx context.Context, jobID string) error {
	if err := ValidateJobID(jobID); err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	baseName := "dreamer-job-" + jobID
	// Ignore errors from disable/stop — units may not exist.
	s.runCmd(ctx, "systemctl", "--user", "disable", "--now", baseName+".timer")
	s.runCmd(ctx, "systemctl", "--user", "stop", baseName+".timer")
	s.runCmd(ctx, "systemctl", "--user", "stop", baseName+".service")

	unitDir, err := s.unitDir()
	if err != nil {
		return err
	}
	os.Remove(filepath.Join(unitDir, baseName+".timer"))
	os.Remove(filepath.Join(unitDir, baseName+".service"))
	return s.reloadDaemon(ctx)
}

// Inspect returns the OS-level health for a job's schedule.
func (s *linuxScheduler) Inspect(ctx context.Context, jobID string) (ScheduleHealth, error) {
	baseName := "dreamer-job-" + jobID

	output, err := s.runCmd(ctx, "systemctl", "--user", "is-active", baseName+".timer")
	installed := err == nil && strings.TrimSpace(string(output)) == "active"

	if !installed {
		// Check if unit files exist even if not active.
		unitDir, dirErr := s.unitDir()
		if dirErr == nil {
			timerPath := filepath.Join(unitDir, baseName+".timer")
			if _, statErr := os.Stat(timerPath); statErr == nil {
				return ScheduleHealth{
					Installed: true,
					Enabled:   false,
					Detail:    "timer unit exists but is not active",
				}, nil
			}
		}
		return ScheduleHealth{Installed: false}, nil
	}

	health := ScheduleHealth{Installed: true, Enabled: true}

	// Get next elapse from systemctl status.
	statusOutput, err := s.runCmd(ctx, "systemctl", "--user", "show", baseName+".timer", "--property=NextElapseUSecRealtime")
	if err == nil {
		line := strings.TrimSpace(string(statusOutput))
		if idx := strings.Index(line, "="); idx >= 0 {
			timeStr := strings.TrimSpace(line[idx+1:])
			if t, parseErr := time.Parse("Mon 2006-01-02 15:04:05 MST", timeStr); parseErr == nil {
				health.NextRunTime = &t
			}
		}
	}

	return health, nil
}

// ListOwn returns job IDs of Dreamer schedules owned by this installation.
func (s *linuxScheduler) ListOwn(_ context.Context) ([]string, error) {
	unitDir, err := s.unitDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(unitDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read unit dir: %w", err)
	}

	var jobIDs []string
	seen := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "dreamer-job-") && strings.HasSuffix(name, ".timer") {
			jobID := strings.TrimPrefix(name, "dreamer-job-")
			jobID = strings.TrimSuffix(jobID, ".timer")
			// B15: Validate to prevent path traversal via crafted filenames.
			if jobID != "" && !seen[jobID] && ValidateJobID(jobID) == nil {
				jobIDs = append(jobIDs, jobID)
				seen[jobID] = true
			}
		}
	}
	return jobIDs, nil
}

// buildTimerUnit generates a systemd timer unit file for the job.
func (s *linuxScheduler) buildTimerUnit(params ScheduleParams) (string, error) {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=Timer for Dreamer background job " + params.JobID + "\n")
	b.WriteString("\n[Timer]\n")

	if params.Schedule.Kind == ScheduleInterval && params.Schedule.Every != "" {
		// Use interval-based triggering for custom Every intervals.
		interval := durationToSystemdSpan(EveryDuration(params.Schedule))
		b.WriteString("OnBootSec=1min\n")
		b.WriteString("OnUnitActiveSec=" + interval + "\n")
		b.WriteString("Persistent=true\n")
	} else {
		onCalendar, err := scheduleToOnCalendar(params.Schedule)
		if err != nil {
			return "", err
		}
		b.WriteString("OnCalendar=" + onCalendar + "\n")
		b.WriteString("Persistent=true\n")
		b.WriteString(fmt.Sprintf("RandomizedDelaySec=%d\n", randomizedDelaySec(params.Schedule)))
	}

	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=timers.target\n")

	return b.String(), nil
}

// buildServiceUnit generates a systemd service unit file for the job.
func (s *linuxScheduler) buildServiceUnit(params ScheduleParams) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=Run Dreamer background job " + params.JobID + "\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("\n[Service]\n")
	b.WriteString("Type=oneshot\n")
	// Use argv-list form with quoted paths to handle spaces in paths.
	// Escape % as %% per systemd.service(5) specifier syntax.
	exe := strings.ReplaceAll(s.cfg.ExecutablePath, "%", "%%")
	cfg := strings.ReplaceAll(s.cfg.ConfigPath, "%", "%%")
	b.WriteString(fmt.Sprintf("ExecStart=%q jobs run %s --config %q\n", exe, params.JobID, cfg))
	b.WriteString("WorkingDirectory=" + s.cfg.StoreDir + "\n")
	b.WriteString(fmt.Sprintf("TimeoutStartSec=%d\n", timeoutSec(params.Schedule)))
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

// scheduleToOnCalendar converts a ScheduleSpec to a systemd OnCalendar expression.
func scheduleToOnCalendar(spec ScheduleSpec) (string, error) {
	switch spec.Kind {
	case ScheduleInterval:
		return "*-*-* *:00:00", nil
	case ScheduleDaily:
		hour, min, err := parseTimeOfDay(spec.TimeOfDay)
		if err != nil {
			return "", fmt.Errorf("invalid time_of_day %q: %w", spec.TimeOfDay, err)
		}
		return fmt.Sprintf("*-*-* %02d:%02d:00", hour, min), nil
	case ScheduleWeekly:
		hour, min, err := parseTimeOfDay(spec.TimeOfDay)
		if err != nil {
			return "", fmt.Errorf("invalid time_of_day %q: %w", spec.TimeOfDay, err)
		}
		day := weekdayToSystemdDay(strings.ToLower(spec.DayOfWeek))
		return fmt.Sprintf("%s *-*-* %02d:%02d:00", day, hour, min), nil
	case ScheduleCron:
		// systemd OnCalendar syntax is NOT compatible with standard 5-field cron.
		// The caller (Install) rejects cron schedules before reaching here.
		return "*-*-* *:00:00", nil // fallback — should never be reached
	default:
		return "*-*-* *:00:00", nil
	}
}

// weekdayToSystemdDay converts a weekday name to systemd's abbreviated format.
func weekdayToSystemdDay(day string) string {
	switch day {
	case "sunday":
		return "Sun"
	case "monday":
		return "Mon"
	case "tuesday":
		return "Tue"
	case "wednesday":
		return "Wed"
	case "thursday":
		return "Thu"
	case "friday":
		return "Fri"
	case "saturday":
		return "Sat"
	default:
		return "Mon"
	}
}

// randomizedDelaySec returns a randomized delay based on schedule kind.
// Prevents thundering herd for hourly jobs.
func randomizedDelaySec(spec ScheduleSpec) int {
	switch spec.Kind {
	case ScheduleInterval:
		return 300 // 5 min
	case ScheduleDaily, ScheduleWeekly:
		return 0
	default:
		return 0
	}
}

// timeoutSec returns the service timeout in seconds.
func timeoutSec(spec ScheduleSpec) int {
	switch spec.Kind {
	case ScheduleInterval:
		if spec.Every != "" {
			if d, err := parseEveryDuration(spec.Every); err == nil {
				return int(d.Seconds())
			}
		}
		return 3300 // 55 min
	case ScheduleDaily, ScheduleWeekly:
		return 7200 // 2 hours
	default:
		return 3600
	}
}

// durationToSystemdSpan converts a Go duration to systemd time span format.
// Systemd accepts: "5min", "1h", "30s", etc.
func durationToSystemdSpan(d time.Duration) string {
	totalMinutes := int(d.Minutes())
	if totalMinutes >= 60 && totalMinutes%60 == 0 {
		return fmt.Sprintf("%dh", totalMinutes/60)
	}
	return fmt.Sprintf("%dmin", totalMinutes)
}

// ensureUnitDir creates the systemd user unit directory if it doesn't exist.
func (s *linuxScheduler) ensureUnitDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, systemdUnitDir)
	if err := os.MkdirAll(dir, fsutil.DirPerms); err != nil {
		return "", fmt.Errorf("create unit dir: %w", err)
	}
	return dir, nil
}

// unitDir returns the systemd user unit directory path.
func (s *linuxScheduler) unitDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, systemdUnitDir), nil
}

// reloadDaemon runs systemctl --user daemon-reload.
func (s *linuxScheduler) reloadDaemon(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.runCmd(ctx, "systemctl", "--user", "daemon-reload")
	return err
}

// writeFileAtomic writes content to a path atomically (temp + rename),
// creating parent dirs if needed.
func writeFileAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, fsutil.DirPerms); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	return fsutil.WriteFileAtomic(path, content, fsutil.FilePerms)
}
