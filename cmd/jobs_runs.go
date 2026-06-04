package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/logging"
)

// newJobsRunsCommand returns "dreamer jobs runs <job-id>".
// Lists all runs for a background job with filtering and pagination.
func newJobsRunsCommand() *cobra.Command {
	var (
		jsonOutput bool
		limit      int
		status     string
	)

	command := &cobra.Command{
		Use:   "runs <job-id>",
		Short: "List runs for a background job.",
		Long:  "List execution history for a background job, most recent first.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return missingArgError(cmd, "job-id",
					"The ID of the job to list runs for.",
					"dreamer jobs runs <job-id>")
			}
			jobID := args[0]

			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			outputRoot, _, err := resolveOutputRoot(cmd, resolvedConfigPath)
			if err != nil {
				return err
			}

			lg := logging.Silent()
			store := backgroundjobs.NewStore(outputRoot, lg)
			runStore := backgroundjobs.NewRunStore(store.Dir(), lg)

			// Verify job exists.
			state, err := store.Load()
			if err != nil {
				return fmt.Errorf("load jobs: %w", err)
			}
			if state.Jobs[jobID] == nil {
				return jobNotFoundError(cmd, jobID)
			}

			runs, err := runStore.List(jobID)
			if err != nil {
				return fmt.Errorf("load runs: %w", err)
			}

			// Apply --status filter.
			if status != "" {
				if !isValidRunStatus(status) {
					return invalidFlagValueError(cmd, "status", status, sortedKeys(validRunStatuses))
				}
				statusLower := strings.ToLower(status)
				var filtered []backgroundjobs.Run
				for _, r := range runs {
					if strings.ToLower(string(r.Status)) == statusLower {
						filtered = append(filtered, r)
					}
				}
				runs = filtered
			}

			// Apply --limit.
			if limit > 0 && len(runs) > limit {
				runs = runs[:limit]
			}

			if jsonOutput {
				return printRunsJSON(cmd, runs)
			}
			return printRunsTable(cmd, jobID, runs)
		},
	}

	command.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON array.")
	command.Flags().IntVarP(&limit, "limit", "n", 20, "Maximum number of runs to show.")
	command.Flags().StringVar(&status, "status", "", "Filter by status (completed, failed, timed_out, cancelled, skipped).")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}

func printRunsJSON(cmd *cobra.Command, runs []backgroundjobs.Run) error {
	if runs == nil {
		runs = []backgroundjobs.Run{}
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(runs)
}

func printRunsTable(cmd *cobra.Command, jobID string, runs []backgroundjobs.Run) error {
	if len(runs) == 0 {
		cmd.Printf("No runs recorded for job %q.\n", jobID)
		return nil
	}

	cmd.Printf("%-20s %-12s %-10s %-18s %s\n",
		"STARTED_AT", "STATUS", "DURATION", "RUN_ID", "ERROR")

	for _, r := range runs {
		started := r.StartedAt.Format("2006-01-02 15:04:05")
		duration := "-"
		if r.FinishedAt != nil {
			duration = formatRunDuration(r.DurationMillis)
		}
		errMsg := r.Error
		if errMsg == "" && r.SkippedReason != "" {
			errMsg = r.SkippedReason
		}
		// Truncate error for table display (rune-safe).
		errMsg = truncateForDisplay(errMsg, 50)
		cmd.Printf("%-20s %-12s %-10s %-18s %s\n",
			started, r.Status, duration, r.ID, errMsg)
	}

	// Show warnings from any run that carried them.
	for _, r := range runs {
		for _, w := range r.Warnings {
			cmd.Printf("  ⚠ %s [%s]\n", w, r.ID)
		}
	}

	return nil
}

// formatRunDuration formats milliseconds into a human-readable duration.
func formatRunDuration(millis int64) string {
	d := time.Duration(millis) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", millis)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}

// validRunStatuses is the set of valid values for the --status flag.
var validRunStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
	"timed_out": true,
	"cancelled": true,
	"skipped":   true,
	"running":   true,
	"idle":      true,
	"never_run": true,
}

// isValidRunStatus reports whether s is a recognized run status value.
func isValidRunStatus(s string) bool {
	return validRunStatuses[strings.ToLower(s)]
}
