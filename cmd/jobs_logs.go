package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/logging"
)

const jobLogScannerBufferSize = 1024 * 1024 // tolerate verbose provider output lines

// newJobsLogsCommand returns "dreamer jobs logs <job-id> [--run <run-id>] [--tail N]".
// Shows the full provider output for a run, falling back to Error + OutputSummary.
func newJobsLogsCommand() *cobra.Command {
	var (
		runID  string
		tail   int
		follow bool
	)

	command := &cobra.Command{
		Use:   "logs <job-id>",
		Short: "Show logs for a background job run.",
		Long: "Show the full provider output for a job run. " +
			"By default shows the most recent run. Use --run to target a specific run.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return missingArgError(cmd, "job-id",
					"The ID of the job to show logs for.",
					"dreamer jobs logs <job-id>")
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

			// Find the target run.
			var run *backgroundjobs.Run
			if runID != "" {
				runs, listErr := runStore.List(jobID)
				if listErr != nil {
					return fmt.Errorf("load runs: %w", listErr)
				}
				for _, r := range runs {
					if r.ID == runID {
						run = &r
						break
					}
				}
				if run == nil {
					return fmt.Errorf("run %q not found for job %q", runID, jobID)
				}
			} else {
				latest, latestErr := runStore.Latest(jobID)
				if latestErr != nil {
					return fmt.Errorf("load latest run: %w", latestErr)
				}
				if latest == nil {
					cmd.Printf("No runs recorded for job %q.\n", jobID)
					return nil
				}
				run = latest
			}

			// If --follow and the run is still running, wait for completion.
			if follow && run.Status == backgroundjobs.RunStatusRunning {
				cmd.Printf("Waiting for run %s to complete (Ctrl-C to stop)...\n", run.ID)
				completed, waitErr := waitForRunCompletion(cmd.Context(), runStore, jobID, run.ID)
				if waitErr != nil {
					return waitErr
				}
				run = completed
			} else if follow {
				cmd.Println("Run already finished; showing logs.")
			}

			// Try to read the per-run log file first.
			if run.LogPath != "" {
				if err := printLogFile(cmd, run.LogPath, tail); err == nil {
					return nil
				}
				// Log file exists in the record but couldn't be read — warn and fall through.
				cmd.Printf("Warning: could not read log file %s: %v\n", run.LogPath, err)
			}

			// Fallback: show Error + OutputSummary from the Run record.
			printRunFallback(cmd, run)
			return nil
		},
	}

	command.Flags().StringVar(&runID, "run", "", "Specific run ID (default: most recent).")
	command.Flags().IntVarP(&tail, "tail", "t", 0, "Show only the last N lines (0 = all).")
	command.Flags().BoolVarP(&follow, "follow", "f", false, "Wait for a running job to complete, then show logs.")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}

// printLogFile reads and prints a log file, optionally tailing the last N lines.
func printLogFile(cmd *cobra.Command, path string, tail int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if tail <= 0 {
		scanner := newJobLogScanner(f)
		for scanner.Scan() {
			cmd.Println(scanner.Text())
		}
		return scanner.Err()
	}

	lines := make([]string, tail)
	nextLine := 0
	linesRead := 0
	scanner := newJobLogScanner(f)
	for scanner.Scan() {
		lines[nextLine] = scanner.Text()
		nextLine = (nextLine + 1) % tail
		linesRead++
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	linesToPrint := linesRead
	if linesToPrint > tail {
		linesToPrint = tail
	}
	start := 0
	if linesRead > tail {
		start = nextLine
	}
	for i := 0; i < linesToPrint; i++ {
		cmd.Println(lines[(start+i)%tail])
	}
	return nil
}

func newJobLogScanner(f *os.File) *bufio.Scanner {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), jobLogScannerBufferSize)
	return scanner
}

// printRunFallback shows Error + OutputSummary from the Run record
// when the per-run log file is unavailable.
func printRunFallback(cmd *cobra.Command, run *backgroundjobs.Run) {
	cmd.Printf("Run:     %s\n", run.ID)
	cmd.Printf("Status:  %s\n", run.Status)
	cmd.Printf("Started: %s\n", run.StartedAt.Format("2006-01-02 15:04:05"))

	if run.FinishedAt != nil {
		cmd.Printf("Finished: %s (took %s)\n",
			run.FinishedAt.Format("2006-01-02 15:04:05"), formatRunDuration(run.DurationMillis))
	}

	if run.LogPath == "" {
		cmd.Printf("\n(no per-run log file available)\n")
	}

	var hasContent bool
	if run.Error != "" {
		cmd.Printf("\nError:\n%s\n", run.Error)
		hasContent = true
	}
	if run.OutputSummary != "" {
		if !hasContent {
			cmd.Printf("\n")
		}
		cmd.Printf("Output (truncated to %d chars):\n%s\n",
			backgroundjobs.MaxOutputSummaryRunes, run.OutputSummary)
	}
	if !hasContent && run.OutputSummary == "" {
		cmd.Printf("\n(no output captured)\n")
	}
}

// waitForRunCompletion polls the run store until the given run leaves
// "running" status or the context is cancelled. Returns the updated Run.
func waitForRunCompletion(ctx context.Context, runStore *backgroundjobs.RunStore, jobID, runID string) (*backgroundjobs.Run, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			runs, err := runStore.List(jobID)
			if err != nil {
				return nil, fmt.Errorf("poll run status: %w", err)
			}
			for i := range runs {
				if runs[i].ID == runID && runs[i].Status != backgroundjobs.RunStatusRunning {
					return &runs[i], nil
				}
			}
		}
	}
}
