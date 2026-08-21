package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"dreamer/internal/analyzer"
	"dreamer/internal/backgroundjobs"
	"dreamer/internal/config"
	"dreamer/internal/diagnostics"
	"dreamer/internal/logging"
)

// diagContext bundles everything the doctor and bug commands need: the
// collected report plus a redactor for anything that leaves the machine.
type diagContext struct {
	report   diagnostics.Report
	input    diagnostics.Input
	redactor *analyzer.Redactor
}

// resolveDiagContext loads config (tolerating failure so doctor can still run
// against a broken setup), resolves the output root, runs background-jobs
// health checks, and collects the diagnostics report.
func resolveDiagContext(cmd *cobra.Command) (*diagContext, error) {
	resolvedConfigPath, err := resolveConfigPath(configPath)
	if err != nil {
		return nil, err
	}

	in := diagnostics.Input{
		Version:    Version(),
		Commit:     commit,
		BuildDate:  date,
		ConfigPath: resolvedConfigPath,
	}

	overlayPath, _ := config.GlobalOverlayPath()
	cfg, cfgErr := config.LoadConfigWithOverlay(resolvedConfigPath, overlayPath)
	if cfgErr != nil {
		in.ConfigErr = cfgErr
	} else {
		in.Config = cfg
	}

	in.OutputRoot = resolveDiagOutputRoot(cmd, cfg)

	// Health check is best-effort: scheduler bootstrap failures (missing
	// executable path, unwritable store dir) surface as a warn-level check,
	// never abort the whole report.
	deps, depsErr := buildSchedulerDeps(in.OutputRoot, resolvedConfigPath, logging.Silent())
	if depsErr != nil {
		in.HealthErr = depsErr
	} else {
		checker := &backgroundjobs.HealthChecker{
			Scheduler: deps.scheduler,
			Store:     deps.store,
			RunStore:  deps.runStore,
			Logger:    deps.logger,
		}
		health, healthErr := checker.CheckHealth(cmd.Context())
		if healthErr != nil {
			in.HealthErr = healthErr
		} else {
			in.Health = &health
		}
	}

	extraPatterns := []string(nil)
	if cfg != nil {
		extraPatterns = cfg.Redaction.Patterns
	}
	redactor, err := analyzer.NewRedactor(extraPatterns)
	if err != nil {
		return nil, fmt.Errorf("build redactor: %w", err)
	}

	return &diagContext{
		report:   diagnostics.Collect(in),
		input:    in,
		redactor: redactor,
	}, nil
}

// resolveDiagOutputRoot determines the output root without failing when
// config is broken — unlike resolveOutputRoot, which hard-errors. Falls back
// to the default ~/.dreamer/output so diagnostics still have somewhere to look.
func resolveDiagOutputRoot(cmd *cobra.Command, cfg *config.App) string {
	if flag := cmd.Flag(outputRootFlag); flag != nil && flag.Changed {
		if abs, err := filepath.Abs(flag.Value.String()); err == nil {
			return abs
		}
		return flag.Value.String()
	}
	if cfg != nil && cfg.Daemon.OutputRoot != "" {
		if abs, err := filepath.Abs(cfg.Daemon.OutputRoot); err == nil {
			return abs
		}
		return cfg.Daemon.OutputRoot
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dreamer", "output")
}
