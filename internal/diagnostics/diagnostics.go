// Package diagnostics collects environment, config, and health information
// into a structured report used by `dreamer doctor` and `dreamer bug`.
//
// The collector is read-only: it never mutates state outside a single
// throwaway probe file used for the output-root writability check. All
// content that leaves the machine (report files, issue bodies) must pass
// through the analyzer redaction pipeline first — see bundle.go.
package diagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/sandbox"
)

// Status is the outcome of a single diagnostic check.
type Status string

const (
	StatusPass    Status = "pass"
	StatusWarn    Status = "warn"
	StatusFail    Status = "fail"
	StatusInfo    Status = "info" // informational, neither good nor bad
	StatusSkipped Status = "skipped"
)

// Check is one named diagnostic result.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// SystemInfo describes the binary and host platform.
type SystemInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

// Report is the aggregated diagnostics result.
type Report struct {
	GeneratedAt time.Time  `json:"generated_at"`
	System      SystemInfo `json:"system"`
	Checks      []Check    `json:"checks"`
}

// Failed returns true when any check has status fail.
func (r Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// Warned returns true when any check has status warn.
func (r Report) Warned() bool {
	for _, c := range r.Checks {
		if c.Status == StatusWarn {
			return true
		}
	}
	return false
}

// Input carries everything Collect needs, resolved by the caller (cmd layer).
// Zero values are handled gracefully: every field is optional so doctor can
// produce useful output even when config loading failed.
type Input struct {
	Version   string
	Commit    string
	BuildDate string

	ConfigPath string
	Config     *config.App // nil when ConfigErr != nil
	ConfigErr  error

	OutputRoot string

	Health    *backgroundjobs.SystemHealth // nil when HealthErr != nil or not run
	HealthErr error
}

// Collect runs all diagnostic checks and returns the aggregated report.
// Collect never fails; individual failures surface as failed checks.
func Collect(in Input) Report {
	report := Report{
		GeneratedAt: time.Now().UTC(),
		System: SystemInfo{
			Version:   in.Version,
			Commit:    in.Commit,
			BuildDate: in.BuildDate,
			GoVersion: runtime.Version(),
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
		},
		Checks: []Check{},
	}

	report.Checks = append(report.Checks, collectConfigCheck(in))
	report.Checks = append(report.Checks, collectOutputRootCheck(in))
	report.Checks = append(report.Checks, collectLogFileCheck(in))
	report.Checks = append(report.Checks, collectSandboxCheck())
	report.Checks = append(report.Checks, collectHealthCheck(in))

	return report
}

// collectConfigCheck verifies the config loads. A broken config is the single
// most common failure mode, so it is checked first with full error detail.
func collectConfigCheck(in Input) Check {
	check := Check{Name: "config"}
	if in.ConfigErr != nil {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("config %q failed to load: %v", in.ConfigPath, in.ConfigErr)
		return check
	}
	if in.Config == nil {
		check.Status = StatusSkipped
		check.Detail = "config not loaded"
		return check
	}
	cfg := in.Config
	detail := fmt.Sprintf("%d project(s), default provider %q, log level %q",
		len(cfg.Projects), cfg.DefaultProvider, cfg.Logging.Level)
	if cfg.Daemon.OutputRoot != "" {
		detail += fmt.Sprintf(", output root %q", cfg.Daemon.OutputRoot)
	}
	check.Status = StatusPass
	check.Detail = detail
	return check
}

// collectOutputRootCheck probes that the output root exists (or can be
// created) and is writable via a create-and-remove temp file.
func collectOutputRootCheck(in Input) Check {
	check := Check{Name: "output-root"}
	if in.OutputRoot == "" {
		check.Status = StatusSkipped
		check.Detail = "no output root resolved"
		return check
	}
	if err := os.MkdirAll(in.OutputRoot, fsutil.DirPerms); err != nil {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("create %q: %v", in.OutputRoot, err)
		return check
	}
	probe, err := os.CreateTemp(in.OutputRoot, ".dreamer-doctor-probe-*")
	if err != nil {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("not writable: %v", err)
		return check
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	check.Status = StatusPass
	check.Detail = in.OutputRoot
	return check
}

// collectLogFileCheck reports on the main dreamer.log file.
func collectLogFileCheck(in Input) Check {
	check := Check{Name: "log-file"}
	if in.OutputRoot == "" {
		check.Status = StatusSkipped
		return check
	}
	logPath := filepath.Join(in.OutputRoot, "logging", "dreamer.log")
	fi, err := os.Stat(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			check.Status = StatusWarn
			check.Detail = fmt.Sprintf("no log file yet at %q", logPath)
			return check
		}
		check.Status = StatusWarn
		check.Detail = fmt.Sprintf("stat %q: %v", logPath, err)
		return check
	}
	check.Status = StatusPass
	check.Detail = fmt.Sprintf("%s (%d bytes)", logPath, fi.Size())
	return check
}

// collectSandboxCheck reports OS sandbox availability. Unavailability is a
// warning, not a failure: dreamer runs without containment by design there.
func collectSandboxCheck() Check {
	check := Check{Name: "sandbox"}
	if sandbox.Available() {
		check.Status = StatusPass
		check.Detail = "OS sandbox available"
		return check
	}
	check.Status = StatusWarn
	check.Detail = fmt.Sprintf(
		"OS sandbox unavailable on %s/%s — provider processes run without file-access restrictions",
		runtime.GOOS, runtime.GOARCH)
	return check
}

// collectHealthCheck summarizes background-jobs scheduler health.
func collectHealthCheck(in Input) Check {
	check := Check{Name: "background-jobs"}
	if in.HealthErr != nil {
		check.Status = StatusWarn
		check.Detail = fmt.Sprintf("health check could not run: %v", in.HealthErr)
		return check
	}
	if in.Health == nil {
		check.Status = StatusSkipped
		check.Detail = "health check not run"
		return check
	}
	h := in.Health
	detail := fmt.Sprintf("%d job(s), %d enabled, %d scheduled",
		h.TotalJobs, h.EnabledJobs, h.ScheduledJobs)
	if len(h.Issues) > 0 {
		check.Status = StatusWarn
		detail += fmt.Sprintf(", %d issue(s)", len(h.Issues))
	} else {
		check.Status = StatusPass
	}
	check.Detail = detail
	return check
}
