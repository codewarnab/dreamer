# Plan: Job Queue for Long-Running Agentic Analysis

## Problem

The current daemon runs projects **sequentially and synchronously** inside `runDaemonCycle()`. Each project's `pipeline.Run()` is expected to complete in 30s-2min. As providers become more agentic (codebase exploration, multi-step reasoning), a single analysis can take 10min-8hrs. The current architecture cannot handle this:

- A long-running project blocks all subsequent projects
- No progress visibility
- No concurrency between projects
- No timeout handling for extended runs
- Crash = lost work (no partial results)

## Architecture Overview

```
┌──────────────┐     ┌──────────────┐     ┌──────────────────┐
│ Daemon Tick  │────▶│  Job Queue   │────▶│  Worker Pool     │
│ (hourly)     │     │  (FIFO,      │     │  (N concurrent)  │
│              │     │   dedup)     │     │                  │
└──────────────┘     └──────────────┘     └──────────────────┘
                            │                      │
                     ┌──────▼──────┐        ┌──────▼──────┐
                     │ jobs.json   │        │  pipeline   │
                     │ (persistence│        │  .Run()     │
                     │  + history) │        │  (existing) │
                     └──────────────┘        └─────────────┘
```

## New Package: `internal/jobqueue/`

### Data Structures

```go
// jobqueue.go

type JobStatus string

const (
    StatusPending   JobStatus = "pending"   // enqueued, waiting for worker
    StatusRunning   JobStatus = "running"   // worker assigned, pipeline active
    StatusCompleted JobStatus = "completed" // pipeline finished successfully
    StatusFailed    JobStatus = "failed"    // pipeline errored
    StatusTimedOut  JobStatus = "timed_out" // max_analysis_duration exceeded
    StatusCancelled JobStatus = "cancelled" // daemon shutdown, context cancelled
)

type Job struct {
    ID        string            `json:"id"`         // uuid or "<project>-<timestamp>"
    Project   string            `json:"project"`    // project name (from config)
    ProjectPath string          `json:"project_path"` // absolute path
    Status    JobStatus         `json:"status"`
    EnqueuedAt time.Time        `json:"enqueued_at"`
    StartedAt  *time.Time       `json:"started_at,omitempty"`
    FinishedAt *time.Time       `json:"finished_at,omitempty"`
    Duration   *time.Duration   `json:"duration,omitempty"` // wall-clock
    Error      string           `json:"error,omitempty"`    // last error message
    FindingsAdded int           `json:"findings_added,omitempty"` // from pipeline result
    MessagesRead  int           `json:"messages_read,omitempty"`
    SourcesCount  int           `json:"sources_count,omitempty"`
    // Snapshot of config at enqueue time
    Provider     string          `json:"provider"`
    Since        string          `json:"since"`
}

type Queue struct {
    mu       sync.Mutex
    jobs     []*Job           // ordered by EnqueuedAt
    store    *Store           // persistence
    maxConcurrent int
    maxDuration   time.Duration
}
```

### Queue Operations

```go
// queue.go

// Enqueue adds a job for the given project. If a pending or running job
// already exists for that project, the new job is dropped (dedup).
func (q *Queue) Enqueue(project string, cfg EnqueueConfig) *Job

// Dequeue returns the next pending job, or nil if at concurrency limit
// or no pending jobs. Caller must call Complete/Failed/TimedOut when done.
func (q *Queue) Dequeue() *Job

// RunningCount returns the number of currently running jobs.
func (q *Queue) RunningCount() int

// Complete marks a job as finished successfully.
func (q *Queue) Complete(job *Job, result PipelineResult)

// Failed marks a job as failed with an error.
func (q *Queue) Failed(job *Job, err error)

// TimedOut marks a job as timed out (max duration exceeded).
func (q *Queue) TimedOut(job *Job)

// Cancel marks a job as cancelled (daemon shutdown).
func (q *Queue) Cancel(job *Job)

// Status returns a snapshot of all jobs for `dreamer status`.
func (q *Queue) Status() QueueStatus

// PruneHistory removes completed/failed jobs older than maxAge.
func (q *Queue) PruneHistory(maxAge time.Duration)
```

### Persistence: `jobs.json`

```go
// store.go

// Stored at <outputRoot>/jobs.json alongside the per-project directories.
// Survives daemon restarts. On startup, running jobs are checked for
// stale PIDs and marked as interrupted.

type Store struct {
    path string
    mu   sync.Mutex
}

type persistedState struct {
    Version int    `json:"version"`
    Jobs    []*Job `json:"jobs"`
}

func (s *Store) Load() ([]*Job, error)
func (s *Store) Save(jobs []*Job) error
```

### Startup Recovery

On daemon start, the queue loads `jobs.json` and handles stale state:

1. Jobs with `StatusRunning` — check if the pipeline process is still alive
   - If alive: leave as running (resume tracking)
   - If dead: mark as `StatusFailed` with error "daemon restarted"
2. Jobs with `StatusPending` — leave as pending (will be picked up by workers)
3. Jobs with terminal status (`Completed`, `Failed`, `TimedOut`, `Cancelled`) — keep for history

## Config Changes

### New Fields in `DaemonConfig`

```go
// internal/config/loader.go

type DaemonConfig struct {
    FrequencySeconds    int    `yaml:"frequency_seconds"`      // existing
    OutputRoot          string `yaml:"output_root"`            // existing
    MaxConcurrentJobs   int    `yaml:"max_concurrent_jobs"`    // NEW: default 1
    MaxAnalysisDuration string `yaml:"max_analysis_duration"`  // NEW: default "8h"
    JobHistoryRetention string `yaml:"job_history_retention"`  // NEW: default "720h" (30 days)
}
```

### Config Template Addition

```yaml
daemon:
  frequency_seconds: 3600
  output_root: ""  # default: <UserConfigDir>/dreamer
  max_concurrent_jobs: 1          # how many analyses can run simultaneously
  max_analysis_duration: "8h"     # per-job timeout
  job_history_retention: "720h"   # how long to keep completed job records (30 days)
```

### Per-Project Override

```yaml
projects:
  - name: big-project
    path: ~/code/big-project
    max_analysis_duration: "24h"  # override for this project
```

### Defaults

| Field | Default | Validation |
|-------|---------|------------|
| `max_concurrent_jobs` | `1` | `>= 1` |
| `max_analysis_duration` | `"8h"` | parseable duration, `> 0` |
| `job_history_retention` | `"720h"` | parseable duration, `> 0` |

## Daemon Changes

### Replace `runDaemonCycle` with Scheduler

**Current flow** (`cmd/daemon.go`):
```
ticker fires → runDaemonCycle() → for each project: pipeline.Run() synchronously
```

**New flow**:
```
ticker fires → enqueueMissingJobs() → worker pool picks up pending jobs
              ↕ (separate goroutine)
         worker loop → dequeue → pipeline.Run() with timeout → complete/fail
```

### New Daemon Structure

```go
// cmd/daemon.go

func runDaemon(ctx context.Context, cfg *config.Config, ...) error {
    // 1. Load/create job queue with persistence
    queue := jobqueue.New(jobqueue.Options{
        StorePath:     filepath.Join(cfg.Daemon.OutputRoot, "jobs.json"),
        MaxConcurrent: cfg.Daemon.MaxConcurrentJobs,
        MaxDuration:   parseDuration(cfg.Daemon.MaxAnalysisDuration),
    })

    // 2. Recover from previous run
    if err := queue.Recover(); err != nil {
        logger.Warn("failed to recover job queue", ...)
    }

    // 3. Start worker pool (separate goroutines)
    workers := newWorkerPool(ctx, queue, cfg, logger, discoveryCache)
    workers.Start()
    defer workers.Stop()

    // 4. Start ticker loop for enqueueing
    ticker := time.NewTicker(frequency)
    defer ticker.Stop()

    // 5. Enqueue immediately on start
    enqueueMissingJobs(ctx, queue, cfg, logger)

    for {
        select {
        case <-ctx.Done():
            // Graceful shutdown: cancel running jobs, save state
            workers.Stop() // waits for running jobs to finish or timeout
            return nil
        case <-ticker.C:
            enqueueMissingJobs(ctx, queue, cfg, logger)
        }
    }
}
```

### Worker Pool

```go
// workers.go

type workerPool struct {
    ctx     context.Context
    queue   *jobqueue.Queue
    cfg     *config.Config
    logger  *logging.Logger
    cache   *pipeline.DiscoveryCache
    wg      sync.WaitGroup
}

func (wp *workerPool) Start() {
    for i := 0; i < wp.queue.MaxConcurrent(); i++ {
        wp.wg.Add(1)
        go wp.worker(i)
    }
}

func (wp *workerPool) Stop() {
    wp.wg.Wait() // blocks until all workers finish current job
}

func (wp *workerPool) worker(id int) {
    defer wp.wg.Done()
    for {
        job := wp.queue.Dequeue()
        if job == nil {
            select {
            case <-wp.ctx.Done():
                return
            case <-time.After(5 * time.Second):
                continue // poll for new jobs
            }
        }
        wp.runJob(id, job)
    }
}

func (wp *workerPool) runJob(workerID int, job *jobqueue.Job) {
    // 1. Create per-job context with timeout
    maxDur := resolveMaxDuration(job, wp.cfg)
    jobCtx, cancel := context.WithTimeout(wp.ctx, maxDur)
    defer cancel()

    // 2. Build pipeline options (same as current)
    opts := pipeline.Options{
        ProjectPath:    job.ProjectPath,
        ProjectName:    job.Project,
        Since:          job.Since,
        DiscoveryCache: wp.cache,
        // ... CLI overrides from config
    }

    // 3. Run pipeline (existing code, unchanged)
    result, err := pipeline.Run(jobCtx, opts, wp.logger)

    // 4. Update job status
    if errors.Is(jobCtx.Err(), context.DeadlineExceeded) {
        wp.queue.TimedOut(job)
    } else if err != nil {
        wp.queue.Failed(job, err)
    } else {
        wp.queue.Complete(job, result)
    }
}
```

### Enqueue Logic

```go
// scheduler.go

func enqueueMissingJobs(ctx context.Context, queue *jobqueue.Queue, cfg *config.Config, logger *logging.Logger) {
    for _, project := range cfg.Projects {
        if queue.HasActiveJob(project.Name) {
            continue // already pending or running — dedup
        }
        queue.Enqueue(project.Name, jobqueue.EnqueueConfig{
            ProjectPath: project.Path,
            Provider:    resolveProvider(cfg, project),
            Since:       project.Since,
        })
        logger.Info("enqueued analysis job", "project", project.Name)
    }
}
```

## New Command: `dreamer status`

### Command Structure

```go
// cmd/status.go

func newStatusCommand() *cobra.Command {
    return &cobra.Command{
        Use:   "status",
        Short: "Show analysis job queue status",
        RunE:  runStatus,
    }
}
```

### Output Format

```
$ dreamer status

Job Queue (2 running, 1 pending, 5 completed)

RUNNING
  big-project     started 14:23  elapsed 2h15m  provider claude-cli  findings: 3
  small-api       started 15:01  elapsed 45m    provider codex-cli   findings: 0

PENDING
  microservice    enqueued 15:30  provider gemini-cli

COMPLETED (last 24h)
  big-project     completed 12:05  duration 1h32m  findings: 12
  small-api       completed 11:05  duration 45m    findings: 5
  microservice    timed_out  09:05  duration 8h0m   findings: 7 (partial)
  old-project     failed     08:05  duration 5m     error: rate_limit

Use 'dreamer status --json' for machine-readable output.
```

### Flags

- `--json` — JSON output for scripting
- `--all` — show all history (not just last 24h)
- `--project <name>` — filter to one project

## Edge Cases Handled

### 1. Job Timeout

When a job exceeds `max_analysis_duration`:
- Worker's `context.WithTimeout` fires
- `pipeline.Run()` returns with `context.DeadlineExceeded`
- Job marked as `StatusTimedOut`
- Partial findings are already on disk (current `GenerateTodos` writes atomically on each pipeline run; streaming writes are a future enhancement)
- State is NOT updated (pipeline didn't complete), so next run will re-process

### 2. Daemon Crash Recovery

On restart:
1. Load `jobs.json`
2. Any `StatusRunning` jobs: check if process is alive (reuse `fsutil.isProcessAlive`)
3. Dead process → mark `StatusFailed` with "daemon restarted during analysis"
4. Live process → leave as running (orphan detection is best-effort)
5. `StatusPending` jobs → remain in queue, will be picked up by workers
6. Next tick will re-enqueue projects that have no active/queued job

### 3. Manual `analyze` vs Daemon Conflict

When user runs `dreamer analyze --path <project>`:
- Check `jobs.json` for a running/pending job for that project
- If found: print error "analysis already in progress (job <id>, status: running)"
- If not found: run normally (one-shot analyze doesn't touch the queue)

### 4. Rate Limiting

- Each job handles rate limits independently (current behavior)
- If multiple jobs hit the same provider's rate limit, they all back off independently
- No global rate limit coordination needed initially — providers handle this client-side
- Future: add a shared rate-limit state if thundering herd becomes a problem

### 5. Concurrent `todos.md` Writes

- Currently impossible: `max_concurrent_jobs` defaults to 1
- If user sets `max_concurrent_jobs > 1`: multiple projects run simultaneously, but each project has at most one job (dedup), so no concurrent writes to the same `todos.md`
- Race condition protection: the queue's `HasActiveJob()` check ensures one-job-per-project

### 6. New Chat Data During Analysis

- Current job uses its snapshot of chat data from when it started
- New chat data is picked up on the next enqueue (next tick)
- No hot-injection of new data into running sessions

### 7. Machine Sleep / Hibernate

- Use wall-clock time for timeout (simplest)
- On wake: ticker fires, `enqueueMissingJobs()` runs, workers resume polling
- If a job was running when machine slept and the wall-clock timeout expired:
  - Worker's context deadline fires on next operation
  - Job marked as `StatusTimedOut`
- If timeout not expired: job continues normally

### 8. Per-Project Timeout Override

- `ProjectConfig.MaxAnalysisDuration` overrides `DaemonConfig.MaxAnalysisDuration`
- Resolved in `resolveMaxDuration(job, cfg)` by looking up the project config

### 9. Job History / Cleanup

- `jobs.json` stores all jobs (running, pending, completed, failed, timed out)
- On each enqueue, `PruneHistory()` removes terminal jobs older than `job_history_retention`
- Default retention: 720 hours (30 days)
- Keeps `jobs.json` bounded

### 10. Provider CLI Crash Mid-Analysis

- `pipeline.Run()` returns an error
- Job marked as `StatusFailed` with the error message
- Next tick: project has no active job → re-enqueued automatically

### 11. Config Changes Mid-Analysis

- Jobs snapshot their config at enqueue time (`Provider`, `Since`)
- Config changes take effect on the next new job
- Max duration is resolved at run time from project config (so timeout changes apply immediately)

### 12. Graceful Shutdown

On SIGINT/SIGTERM:
1. Context is cancelled
2. `workers.Stop()` is called — waits for all workers to finish current jobs
3. Running jobs get cancelled context → `pipeline.Run()` returns error
4. Jobs marked as `StatusCancelled`
5. Pending jobs remain in `jobs.json` for next startup
6. Lock file released

## File Changes Summary

### New Files

| File | Purpose |
|------|---------|
| `internal/jobqueue/job.go` | `Job` struct, `JobStatus` enum |
| `internal/jobqueue/queue.go` | `Queue` with enqueue/dequeue/dedup/status |
| `internal/jobqueue/store.go` | `jobs.json` persistence |
| `internal/jobqueue/queue_test.go` | Tests for queue logic |
| `internal/jobqueue/store_test.go` | Tests for persistence |
| `cmd/status.go` | `dreamer status` command |
| `cmd/status_test.go` | Status command tests |
| `cmd/workers.go` | Worker pool for running jobs |
| `cmd/scheduler.go` | `enqueueMissingJobs` logic |

### Modified Files

| File | Change |
|------|--------|
| `internal/config/loader.go` | Add `MaxConcurrentJobs`, `MaxAnalysisDuration`, `JobHistoryRetention` to `DaemonConfig`; add `MaxAnalysisDuration` to `ProjectConfig`; update `applyDefaults` and `validateConfig` |
| `cmd/daemon.go` | Replace `runDaemonCycle` loop with scheduler + worker pool; add queue initialization and recovery |
| `cmd/root.go` | Register `newStatusCommand()` |
| `cmd/config.go` | Update `defaultConfigTemplate` with new fields |
| `cmd/analyze.go` | Add conflict check against running jobs |

### Unchanged Files

| File | Reason |
|------|--------|
| `internal/pipeline/pipeline.go` | No changes needed — `pipeline.Run()` already accepts `context.Context` and respects cancellation |
| `internal/state/tracker.go` | No changes needed — state is per-project, updated by pipeline |
| `internal/output/generator.go` | No changes needed — `GenerateTodos` is idempotent via hash dedup |
| `internal/analyzer/*` | No changes needed — provider integrations are independent |

## Implementation Order

1. **Phase 1: Core queue** — `internal/jobqueue/` package with `Job`, `Queue`, `Store`, tests
2. **Phase 2: Config** — Add new config fields, defaults, validation
3. **Phase 3: Daemon refactor** — Replace `runDaemonCycle` with scheduler + worker pool
4. **Phase 4: Status command** — `dreamer status` with tabular and JSON output
5. **Phase 5: Analyze conflict** — Check for running jobs in `dreamer analyze`
6. **Phase 6: Integration tests** — End-to-end test with mock provider
7. **Phase 7: Provider sandboxing** — Apply the flags from `PLANS/provider-sandboxing-implementation.md` (separate PR)

## Risks and Mitigations

| Risk | Mitigation |
|------|-----------|
| `jobs.json` corruption | Atomic writes via `fsutil.WriteFileAtomic`; version field for migration |
| Worker goroutine leak | `sync.WaitGroup` ensures all workers exit on shutdown |
| Memory from long job history | `PruneHistory` on each enqueue; configurable retention |
| Multiple daemons (user error) | Existing PID lock file prevents this |
| Pipeline changes break queue | Queue is independent of pipeline; only needs `pipeline.Run()` signature |
