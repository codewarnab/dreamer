package jobqueue

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// JobStatus represents the lifecycle state of an analysis job.
type JobStatus string

const (
	StatusPending   JobStatus = "pending"   // enqueued, waiting for worker
	StatusRunning   JobStatus = "running"   // worker assigned, pipeline active
	StatusCompleted JobStatus = "completed" // pipeline finished successfully
	StatusFailed    JobStatus = "failed"    // pipeline errored
	StatusTimedOut  JobStatus = "timed_out" // max_analysis_duration exceeded
	StatusCancelled JobStatus = "cancelled" // daemon shutdown, context cancelled
)

// terminalStatuses lists all statuses that represent a finished job.
var terminalStatuses = map[JobStatus]bool{
	StatusCompleted: true,
	StatusFailed:    true,
	StatusTimedOut:  true,
	StatusCancelled: true,
}

// IsTerminal reports whether this status represents a finished job.
func (s JobStatus) IsTerminal() bool {
	return terminalStatuses[s]
}

// Job represents one analysis run for a single project.
type Job struct {
	ID            string        `json:"id"`
	Project       string        `json:"project"`
	ProjectPath   string        `json:"project_path"`
	Status        JobStatus     `json:"status"`
	EnqueuedAt    time.Time     `json:"enqueued_at"`
	StartedAt     *time.Time    `json:"started_at,omitempty"`
	FinishedAt    *time.Time    `json:"finished_at,omitempty"`
	Duration      time.Duration `json:"duration,omitempty"`
	Error         string        `json:"error,omitempty"`
	FindingsAdded int           `json:"findings_added,omitempty"`
	MessagesRead  int           `json:"messages_read,omitempty"`
	SourcesCount  int           `json:"sources_count,omitempty"`
	Provider      string        `json:"provider"`
	Since         string        `json:"since"`
}

// newJob creates a job in pending state. ID is generated from project name
// and current timestamp to be human-readable and unique enough for dedup.
func newJob(project, projectPath, provider, since string) *Job {
	now := time.Now().UTC()
	// 4-byte suffix so two enqueues within the same millisecond produce
	// distinct ids — matters when a job completes and is immediately retried.
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	return &Job{
		ID:         fmt.Sprintf("%s-%d-%s", project, now.UnixMilli(), hex.EncodeToString(suffix[:])),
		Project:    project,
		ProjectPath: projectPath,
		Status:     StatusPending,
		EnqueuedAt: now,
		Provider:   provider,
		Since:      since,
	}
}

// markRunning transitions the job to running status.
func (j *Job) markRunning() {
	now := time.Now().UTC()
	j.Status = StatusRunning
	j.StartedAt = &now
}

// markFinished transitions the job to a terminal status with metrics.
func (j *Job) markFinished(status JobStatus, errMsg string, findings, messages, sources int) {
	now := time.Now().UTC()
	j.Status = status
	j.FinishedAt = &now
	if j.StartedAt != nil {
		j.Duration = now.Sub(*j.StartedAt)
	}
	j.Error = errMsg
	j.FindingsAdded = findings
	j.MessagesRead = messages
	j.SourcesCount = sources
}
