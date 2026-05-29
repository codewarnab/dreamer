package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/jobqueue"
	"github.com/spf13/cobra"
)

func newStatusCommand() *cobra.Command {
	var (
		jsonOutput bool
		all        bool
		project    string
	)

	command := &cobra.Command{
		Use:   "status",
		Short: "Show analysis job queue status.",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			overlayPath, _ := config.GlobalOverlayPath()
			cfg, err := config.LoadConfigWithOverlay(resolvedConfigPath, overlayPath)
			if err != nil {
				return fmt.Errorf("load config %q: %w", resolvedConfigPath, err)
			}

			queue := jobqueue.New(jobqueue.Options{
				StorePath: filepath.Join(cfg.Daemon.OutputRoot, "jobs.json"),
			})
			if err := queue.Recover(); err != nil {
				return fmt.Errorf("load job queue: %w", err)
			}

			status := queue.Status()
			if project != "" {
				status = filterStatus(status, project)
			}
			if !all {
				status = filterRecent(status, recentFilterWindow)
			}

			if jsonOutput {
				return printStatusJSON(cmd, status)
			}
			printStatusTable(cmd, status)
			return nil
		},
	}

	command.Flags().BoolVarP(&jsonOutput, "json", "j", false, "Machine-readable JSON output")
	command.Flags().BoolVarP(&all, "all", "a", false, "Show all history (not just last 24h)")
	command.Flags().StringVarP(&project, "project", "P", "", "Filter to one project")

	return command
}

// filterStatus returns a copy of status with only jobs matching the given project name.
func filterStatus(status jobqueue.QueueStatus, project string) jobqueue.QueueStatus {
	var filtered []*jobqueue.Job
	for _, job := range status.Jobs {
		if job.Project == project {
			filtered = append(filtered, job)
		}
	}
	status.Jobs = filtered
	status = recount(status)
	return status
}

// filterRecent returns a copy of status with only active or recent jobs.
func filterRecent(status jobqueue.QueueStatus, maxAge time.Duration) jobqueue.QueueStatus {
	cutoff := time.Now().UTC().Add(-maxAge)
	var filtered []*jobqueue.Job
	for _, job := range status.Jobs {
		if !job.Status.IsTerminal() {
			filtered = append(filtered, job)
			continue
		}
		if job.FinishedAt != nil && job.FinishedAt.After(cutoff) {
			filtered = append(filtered, job)
		}
	}
	status.Jobs = filtered
	status = recount(status)
	return status
}

// recount recalculates the status counters after filtering.
func recount(status jobqueue.QueueStatus) jobqueue.QueueStatus {
	status.Running = 0
	status.Pending = 0
	status.Completed = 0
	status.Failed = 0
	status.TimedOut = 0
	status.Cancelled = 0
	for _, job := range status.Jobs {
		switch job.Status {
		case jobqueue.StatusRunning:
			status.Running++
		case jobqueue.StatusPending:
			status.Pending++
		case jobqueue.StatusCompleted:
			status.Completed++
		case jobqueue.StatusFailed:
			status.Failed++
		case jobqueue.StatusTimedOut:
			status.TimedOut++
		case jobqueue.StatusCancelled:
			status.Cancelled++
		}
	}
	return status
}

// printStatusTable writes a human-readable status summary to the command output.
func printStatusTable(cmd *cobra.Command, status jobqueue.QueueStatus) {
	total := len(status.Jobs)
	if total == 0 {
		cmd.Println("No jobs in queue.")
		return
	}

	cmd.Printf("Job Queue (%d running, %d pending, %d completed, %d failed)\n",
		status.Running, status.Pending, status.Completed, status.Failed+status.TimedOut+status.Cancelled)
	cmd.Println()

	groups := map[jobqueue.Status][]*jobqueue.Job{
		jobqueue.StatusRunning:   {},
		jobqueue.StatusPending:   {},
		jobqueue.StatusCompleted: {},
		jobqueue.StatusFailed:    {},
		jobqueue.StatusTimedOut:  {},
		jobqueue.StatusCancelled: {},
	}
	for _, job := range status.Jobs {
		groups[job.Status] = append(groups[job.Status], job)
	}

	if jobs := groups[jobqueue.StatusRunning]; len(jobs) > 0 {
		cmd.Println("RUNNING")
		for _, job := range jobs {
			elapsed := ""
			started := "-"
			if job.StartedAt != nil {
				elapsed = time.Since(*job.StartedAt).Truncate(time.Minute).String()
				started = job.StartedAt.Format("15:04")
			}
			cmd.Printf("  %-20s  started %s  elapsed %-8s  provider %-15s findings: %d\n",
				job.Project, started, elapsed, job.Provider, job.FindingsAdded)
		}
		cmd.Println()
	}

	if jobs := groups[jobqueue.StatusPending]; len(jobs) > 0 {
		cmd.Println("PENDING")
		for _, job := range jobs {
			cmd.Printf("  %-20s  enqueued %s  provider %s\n",
				job.Project, job.EnqueuedAt.Format("15:04"), job.Provider)
		}
		cmd.Println()
	}

	finishedJobs := make([]*jobqueue.Job, 0)
	finishedJobs = append(finishedJobs, groups[jobqueue.StatusCompleted]...)
	finishedJobs = append(finishedJobs, groups[jobqueue.StatusFailed]...)
	finishedJobs = append(finishedJobs, groups[jobqueue.StatusTimedOut]...)
	finishedJobs = append(finishedJobs, groups[jobqueue.StatusCancelled]...)
	if len(finishedJobs) > 0 {
		cmd.Println("COMPLETED")
		for _, job := range finishedJobs {
			finished := ""
			if job.FinishedAt != nil {
				finished = job.FinishedAt.Format("15:04")
			}
			dur := job.Duration.Truncate(time.Minute).String()
			extra := ""
			if job.Error != "" {
				extra = "  error: " + job.Error
			}
			cmd.Printf("  %-20s  %-10s %s  duration %-8s  findings: %d%s\n",
				job.Project, job.Status, finished, dur, job.FindingsAdded, extra)
		}
	}

	if !quiet {
		cmd.Println("\nUse 'dreamer status --json' for machine-readable output.")
	}
}

// printStatusJSON writes the status as JSON to the command output.
func printStatusJSON(cmd *cobra.Command, status jobqueue.QueueStatus) error {
	// Build a JSON-friendly view.
	type jsonJob struct {
		ID            string        `json:"id"`
		Project       string        `json:"project"`
		Status        string        `json:"status"`
		EnqueuedAt    time.Time     `json:"enqueued_at"`
		StartedAt     *time.Time    `json:"started_at,omitempty"`
		FinishedAt    *time.Time    `json:"finished_at,omitempty"`
		Duration      time.Duration `json:"duration,omitempty"`
		Error         string        `json:"error,omitempty"`
		FindingsAdded int           `json:"findings_added"`
		MessagesRead  int           `json:"messages_read"`
		SourcesCount  int           `json:"sources_count"`
		Provider      string        `json:"provider"`
	}

	type jsonStatus struct {
		Running   int       `json:"running"`
		Pending   int       `json:"pending"`
		Completed int       `json:"completed"`
		Failed    int       `json:"failed"`
		TimedOut  int       `json:"timed_out"`
		Cancelled int       `json:"cancelled"`
		Jobs      []jsonJob `json:"jobs"`
	}

	out := jsonStatus{
		Running:   status.Running,
		Pending:   status.Pending,
		Completed: status.Completed,
		Failed:    status.Failed,
		TimedOut:  status.TimedOut,
		Cancelled: status.Cancelled,
		Jobs:      make([]jsonJob, 0, len(status.Jobs)),
	}
	for _, job := range status.Jobs {
		out.Jobs = append(out.Jobs, jsonJob{
			ID:            job.ID,
			Project:       job.Project,
			Status:        string(job.Status),
			EnqueuedAt:    job.EnqueuedAt,
			StartedAt:     job.StartedAt,
			FinishedAt:    job.FinishedAt,
			Duration:      job.Duration,
			Error:         job.Error,
			FindingsAdded: job.FindingsAdded,
			MessagesRead:  job.MessagesRead,
			SourcesCount:  job.SourcesCount,
			Provider:      job.Provider,
		})
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	cmd.Println(string(data))
	return nil
}
