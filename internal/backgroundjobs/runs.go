package backgroundjobs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
)

const runsDir = "runs"

// RunStore manages per-job run history in JSONL files.
// Thread-safe: holds its own mutex for cross-goroutine serialization.
// Cross-process safe: uses fsutil.AcquireLock per job file during writes.
type RunStore struct {
	dir    string
	logger *logging.Logger
	mu     sync.Mutex
}

// NewRunStore creates a RunStore rooted at <backgroundJobsDir>/runs.
// The parentDir parameter is the background-jobs store directory
// (i.e., Store.Dir()), NOT the output root.
// Creates the runs directory eagerly so run history is visible even
// when the first job execution fails before Append is called.
func NewRunStore(parentDir string, logger *logging.Logger) *RunStore {
	dir := filepath.Join(parentDir, runsDir)
	if err := os.MkdirAll(dir, fsutil.DirPerms); err != nil {
		logger.Warn("create runs dir eagerly (non-fatal)", logging.Any("err", err))
	}
	return &RunStore{
		dir:    dir,
		logger: logger,
	}
}

// Dir returns the runs directory path.
func (runStore *RunStore) Dir() string { return runStore.dir }

// LogDir returns the per-run log directory for a job: <runs_dir>/<job_id>/.
func (runStore *RunStore) LogDir(jobID string) string {
	return filepath.Join(runStore.dir, jobID)
}

// Append adds a run record to runs/<job_id>.jsonl.
// Acquires a per-job file lock for cross-process safety.
func (runStore *RunStore) Append(run Run) error {
	runStore.mu.Lock()
	defer runStore.mu.Unlock()

	if err := os.MkdirAll(runStore.dir, fsutil.DirPerms); err != nil {
		return fmt.Errorf("create runs dir: %w", err)
	}

	lockPath := filepath.Join(runStore.dir, run.JobID+".jsonl.lock")
	release, err := fsutil.AcquireLock(lockPath, runStore.logger)
	if err != nil {
		return fmt.Errorf("acquire run lock for job %q: %w", run.JobID, err)
	}
	defer release()

	data, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}
	data = append(data, '\n')

	path := runStore.filePath(run.JobID)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fsutil.FilePerms)
	if err != nil {
		return fmt.Errorf("open runs file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write run: %w", err)
	}
	return f.Sync()
}

// List returns all runs for a job, most recent first.
// Returns empty slice if no runs exist. Skips malformed lines (crash-safe).
func (runStore *RunStore) List(jobID string) ([]Run, error) {
	runStore.mu.Lock()
	defer runStore.mu.Unlock()

	return runStore.readAll(jobID)
}

// Latest returns the most recent run for a job, or nil if none.
func (runStore *RunStore) Latest(jobID string) (*Run, error) {
	runStore.mu.Lock()
	defer runStore.mu.Unlock()

	runs, err := runStore.readAll(jobID)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return &runs[0], nil
}

// Count returns the number of runs for a job.
func (runStore *RunStore) Count(jobID string) (int, error) {
	runStore.mu.Lock()
	defer runStore.mu.Unlock()

	return runStore.countLines(jobID)
}

// Prune removes runs beyond the retention limit (default 100).
// Returns the number of runs removed. Rewrites file under lock.
// Also removes orphaned per-run log files for pruned runs.
func (runStore *RunStore) Prune(jobID string, keep int) (int, error) {
	runStore.mu.Lock()
	defer runStore.mu.Unlock()

	// Acquire file lock BEFORE reading so concurrent Append from another
	// process cannot sneak in between readAll and the file truncation.
	lockPath := filepath.Join(runStore.dir, jobID+".jsonl.lock")
	release, err := fsutil.AcquireLock(lockPath, runStore.logger)
	if err != nil {
		return 0, fmt.Errorf("acquire run lock for prune: %w", err)
	}
	defer release()

	runs, err := runStore.readAll(jobID)
	if err != nil {
		return 0, err
	}

	if len(runs) <= keep {
		return 0, nil
	}

	// Collect IDs of runs that will be pruned so we can remove their log files.
	pruned := runs[keep:]
	runs = runs[:keep]
	removed := len(pruned)

	// Write kept runs back (most recent first order preserved).
	path := runStore.filePath(jobID)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fsutil.FilePerms)
	if err != nil {
		return 0, fmt.Errorf("open runs file for rewrite: %w", err)
	}
	defer f.Close()

	for _, run := range runs {
		data, err := json.Marshal(run)
		if err != nil {
			return 0, fmt.Errorf("marshal run during prune: %w", err)
		}
		data = append(data, '\n')
		if _, err := f.Write(data); err != nil {
			return 0, fmt.Errorf("write run during prune: %w", err)
		}
	}

	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("sync runs file after prune: %w", err)
	}

	// Remove orphaned per-run log files (best-effort, non-fatal).
	logDir := runStore.LogDir(jobID)
	for _, r := range pruned {
		logPath := filepath.Join(logDir, r.ID+".log")
		if removeErr := os.Remove(logPath); removeErr != nil && !os.IsNotExist(removeErr) {
			runStore.logger.Warn("remove orphaned run log", logging.String("path", logPath), logging.Any("err", removeErr))
		}
	}

	return removed, nil
}

// countLines counts non-empty lines in the JSONL file without deserializing.
// Caller must hold runStore.mu.
func (runStore *RunStore) countLines(jobID string) (int, error) {
	path := runStore.filePath(jobID)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open runs file: %w", err)
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if len(scanner.Bytes()) > 0 {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan runs file: %w", err)
	}
	return count, nil
}

// filePath returns the JSONL file path for a job.
func (runStore *RunStore) filePath(jobID string) string {
	return filepath.Join(runStore.dir, jobID+".jsonl")
}

// readAll reads all runs for a job, most recent first.
// Caller must hold runStore.mu. Skips malformed lines.
func (runStore *RunStore) readAll(jobID string) ([]Run, error) {
	path := runStore.filePath(jobID)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open runs file: %w", err)
	}
	defer f.Close()

	var runs []Run
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var run Run
		if err := json.Unmarshal(line, &run); err != nil {
			// Skip malformed lines (crash-safe partial write recovery).
			continue
		}
		runs = append(runs, run)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan runs file: %w", err)
	}

	// Sort most recent first by StartedAt.
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].StartedAt.After(runs[j].StartedAt)
	})

	return runs, nil
}
