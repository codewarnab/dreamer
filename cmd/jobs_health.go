package cmd

import (
	"encoding/json"
	"fmt"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/logging"

	"github.com/spf13/cobra"
)

func newJobsHealthCommand() *cobra.Command {
	var (
		verbose    bool
		jsonOutput bool
	)

	command := &cobra.Command{
		Use:   "health",
		Short: "Run health diagnostics on all jobs.",
		Long: "Check every job's schedule integrity, OS artifact health, " +
			"permission validity, and report all issues found. Read-only.",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			outputRoot, _, err := resolveOutputRoot(cmd, resolvedConfigPath)
			if err != nil {
				return err
			}

			deps, err := buildSchedulerDeps(outputRoot, resolvedConfigPath, logging.Silent())
			if err != nil {
				return err
			}

			checker := &backgroundjobs.HealthChecker{
				Scheduler: deps.scheduler,
				Store:     deps.store,
				RunStore:  deps.runStore,
				Logger:    deps.logger,
			}

			health, err := checker.CheckHealth(cmd.Context())
			if err != nil {
				return fmt.Errorf("health check: %w", err)
			}

			return printHealthResult(cmd, health, verbose, jsonOutput)
		},
	}

	command.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show per-job health details.")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON.")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}

func printHealthResult(cmd *cobra.Command, health backgroundjobs.SystemHealth, verbose bool, jsonOutput bool) error {
	if jsonOutput {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(health)
	}

	if health.SystemHealthy {
		cmd.Printf("All %d jobs healthy.\n", health.TotalJobs)
		return nil
	}

	cmd.Printf("Issues found: %d\n", len(health.Issues))
	cmd.Printf("Total jobs: %d, Enabled: %d, Scheduled: %d\n",
		health.TotalJobs, health.EnabledJobs, health.ScheduledJobs)

	if len(health.Issues) > 0 {
		cmd.Printf("\nIssues:\n")
		for _, issue := range health.Issues {
			cmd.Printf("  [%s] %s: %s\n", issue.Severity, issue.JobID, issue.Message)
		}
	}

	if verbose {
		cmd.Printf("\nPer-job health:\n")
		for _, jh := range health.JobHealth {
			status := "OK"
			if !jh.Healthy {
				status = "ISSUES"
			}
			cmd.Printf("  %s: enabled=%t scheduled=%t installed=%t [%s]\n",
				jh.JobID, jh.Enabled, jh.HasOSSchedule, jh.Installed, status)
		}
	}

	return nil
}
