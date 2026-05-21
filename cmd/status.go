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
			cfg, err := config.LoadConfig(resolvedConfigPath)
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
				status = filterRecent(status, 24*time.Hour)
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
	for _, j := range status.Jobs {
		if j.Project == project {
			filtered = append(filtered, j)
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
	for _, j := range status.Jobs {
		if !j.Status.IsTerminal() {
			filtered = append(filtered, j)
			continue
		}
		if j.FinishedAt != nil && j.FinishedAt.After(cutoff) {
			filtered = append(filtered, j)
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
	for _, j := range status.Jobs {
		switch j.Status {
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

	groups := map[jobqueue.JobStatus][]*jobqueue.Job{
		jobqueue.StatusRunning:   {},
		jobqueue.StatusPending:   {},
		jobqueue.StatusCompleted: {},
		jobqueue.StatusFailed:    {},
		jobqueue.StatusTimedOut:  {},
		jobqueue.StatusCancelled: {},
	}
	for _, j := range status.Jobs {
		groups[j.Status] = append(groups[j.Status], j)
	}

	if jobs := groups[jobqueue.StatusRunning]; len(jobs) > 0 {
		cmd.Println("RUNNING")
		for _, j := range jobs {
			elapsed := ""
			started := "-"
			if j.StartedAt != nil {
				elapsed = time.Since(*j.StartedAt).Truncate(time.Minute).String()
				started = j.StartedAt.Format("15:04")
			}
			cmd.Printf("  %-20s  started %s  elapsed %-8s  provider %-15s findings: %d\n",
				j.Project, started, elapsed, j.Provider, j.FindingsAdded)
		}
		cmd.Println()
	}

	if jobs := groups[jobqueue.StatusPending]; len(jobs) > 0 {
		cmd.Println("PENDING")
		for _, j := range jobs {
			cmd.Printf("  %-20s  enqueued %s  provider %s\n",
				j.Project, j.EnqueuedAt.Format("15:04"), j.Provider)
		}
		cmd.Println()
	}

	terminal := make([]*jobqueue.Job, 0)
	terminal = append(terminal, groups[jobqueue.StatusCompleted]...)
	terminal = append(terminal, groups[jobqueue.StatusFailed]...)
	terminal = append(terminal, groups[jobqueue.StatusTimedOut]...)
	terminal = append(terminal, groups[jobqueue.StatusCancelled]...)
	if len(terminal) > 0 {
		cmd.Println("COMPLETED")
		for _, j := range terminal {
			finished := ""
			if j.FinishedAt != nil {
				finished = j.FinishedAt.Format("15:04")
			}
			dur := j.Duration.Truncate(time.Minute).String()
			extra := ""
			if j.Error != "" {
				extra = "  error: " + j.Error
			}
			cmd.Printf("  %-20s  %-10s %s  duration %-8s  findings: %d%s\n",
				j.Project, j.Status, finished, dur, j.FindingsAdded, extra)
		}
	}

	cmd.Println("\nUse 'dreamer status --json' for machine-readable output.")
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
	for _, j := range status.Jobs {
		out.Jobs = append(out.Jobs, jsonJob{
			ID:            j.ID,
			Project:       j.Project,
			Status:        string(j.Status),
			EnqueuedAt:    j.EnqueuedAt,
			StartedAt:     j.StartedAt,
			FinishedAt:    j.FinishedAt,
			Duration:      j.Duration,
			Error:         j.Error,
			FindingsAdded: j.FindingsAdded,
			MessagesRead:  j.MessagesRead,
			SourcesCount:  j.SourcesCount,
			Provider:      j.Provider,
		})
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	cmd.Println(string(data))
	return nil
}
