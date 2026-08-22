package capture

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"dreamer/internal/fsutil"
)

// DefaultRetainRuns is the number of run directories kept when the config
// does not override it.
const DefaultRetainRuns = 20

// DefaultMaxRecordKB caps each stored prompt and response field. Large
// transcripts are the norm, so the default is generous; set max_record_kb
// in config to lower it on constrained disks.
const DefaultMaxRecordKB = 2048

// truncatedMarker is appended to fields shortened by the size cap so a
// reader can tell a complete payload from a clipped one.
const truncatedMarker = "…[truncated]"

// ErrRunNotFound is returned when a run ID has no directory under runs/.
var ErrRunNotFound = errors.New("capture run not found")

// runIDHexLen matches pipeline's run ID width: 8 hex chars.
const runIDHexLen = 8

// NewRunID returns a random 8-character hex run ID for replay run
// directories. Pipeline owns its own generator for analysis runs; new
// callers (replay) share this one so IDs stay uniform.
func NewRunID() (string, error) {
	raw := make([]byte, runIDHexLen/2)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("capture: generate run id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// Writer appends call records to a run directory. It is safe for
// concurrent use — parallel phase-1 dispatch shares one writer.
type Writer struct {
	mu         sync.Mutex
	f          *os.File
	dir        string
	next       int
	maxFieldKB int
}

// RunsRoot returns the directory holding every run for a project.
func RunsRoot(outputRoot, projectName string) string {
	return filepath.Join(outputRoot, projectName, "runs")
}

// RunDir returns one run's directory.
func RunDir(outputRoot, projectName, runID string) string {
	return filepath.Join(RunsRoot(outputRoot, projectName), runID)
}

// Open creates the run directory, writes meta.json atomically, and opens
// calls.jsonl for appending. The caller must Close the writer.
func Open(dir string, meta RunMeta) (*Writer, error) {
	if strings.TrimSpace(meta.RunID) == "" {
		return nil, errors.New("capture: meta.RunID is required")
	}
	if err := os.MkdirAll(dir, fsutil.DirPerms); err != nil {
		return nil, fmt.Errorf("capture: create run dir: %w", err)
	}
	if meta.StartedAt.IsZero() {
		meta.StartedAt = time.Now().UTC()
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("capture: marshal meta: %w", err)
	}
	metaPath := filepath.Join(dir, MetaFileName)
	if err := fsutil.WriteFileAtomic(metaPath, metaJSON, fsutil.FilePerms); err != nil {
		return nil, fmt.Errorf("capture: write meta: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, CallsFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, fsutil.FilePerms)
	if err != nil {
		return nil, fmt.Errorf("capture: open calls file: %w", err)
	}
	return &Writer{f: f, dir: dir, next: 0, maxFieldKB: DefaultMaxRecordKB}, nil
}

// SetMaxFieldKB caps prompt and response storage per record. kb == 0 keeps
// the default cap; kb < 0 stores payloads unclipped.
func (w *Writer) SetMaxFieldKB(kb int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case kb > 0:
		w.maxFieldKB = kb
	case kb < 0:
		w.maxFieldKB = -1
	}
}

// Append assigns the next sequence index to rec, applies the field-size
// cap, and writes it as one JSONL line.
func (w *Writer) Append(rec Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.Index = w.next
	w.next++
	if w.maxFieldKB > 0 {
		limit := w.maxFieldKB * 1024
		if len(rec.Prompt) > limit {
			rec.Prompt = rec.Prompt[:limit] + truncatedMarker
		}
		if len(rec.Response) > limit {
			rec.Response = rec.Response[:limit] + truncatedMarker
		}
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("capture: marshal record: %w", err)
	}
	line = append(line, '\n')
	if _, err := w.f.Write(line); err != nil {
		return fmt.Errorf("capture: append record: %w", err)
	}
	return nil
}

// Count returns how many records have been written so far.
func (w *Writer) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.next
}

// Close flushes and closes the underlying file. Safe to call twice.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// RunSummary is the list-view form of one captured run.
type RunSummary struct {
	RunID     string    `json:"run_id"`
	Kind      string    `json:"kind,omitempty"`
	Provider  string    `json:"provider_id"`
	Model     string    `json:"model"`
	Status    string    `json:"status,omitempty"` // derived: worst record status
	Calls     int       `json:"calls"`
	StartedAt time.Time `json:"started_at"`
}

// ListRuns reads every run summary under runsRoot, newest first.
func ListRuns(runsRoot string) ([]RunSummary, error) {
	entries, err := os.ReadDir(runsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return []RunSummary{}, nil
		}
		return nil, fmt.Errorf("capture: list runs: %w", err)
	}
	summaries := make([]RunSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := LoadMeta(filepath.Join(runsRoot, entry.Name()))
		if err != nil {
			continue // skip incomplete or corrupt run dirs
		}
		records, err := loadRecords(filepath.Join(runsRoot, entry.Name()))
		if err != nil {
			continue
		}
		summaries = append(summaries, RunSummary{
			RunID:     meta.RunID,
			Kind:      meta.Kind,
			Provider:  meta.ProviderID,
			Model:     meta.Model,
			Status:    worstStatus(records),
			Calls:     len(records),
			StartedAt: meta.StartedAt,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].StartedAt.After(summaries[j].StartedAt)
	})
	return summaries, nil
}

// LoadMeta reads one run's meta.json from its directory.
func LoadMeta(dir string) (RunMeta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, MetaFileName))
	if err != nil {
		return RunMeta{}, fmt.Errorf("capture: read meta: %w", err)
	}
	var meta RunMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return RunMeta{}, fmt.Errorf("capture: parse meta: %w", err)
	}
	return meta, nil
}

// LoadRun reads one run's metadata and records by run ID.
func LoadRun(runsRoot, runID string) (RunMeta, []Record, error) {
	dir := filepath.Join(runsRoot, runID)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return RunMeta{}, nil, ErrRunNotFound
	}
	meta, err := LoadMeta(dir)
	if err != nil {
		return RunMeta{}, nil, err
	}
	records, err := loadRecords(dir)
	if err != nil {
		return RunMeta{}, nil, err
	}
	return meta, records, nil
}

// Prune keeps only the newest `keep` run directories (by StartedAt from
// meta.json, falling back to directory mtime). Directories without valid
// metadata are treated as oldest and removed first. Pass keep ≤ 0 to use
// DefaultRetainRuns.
func Prune(runsRoot string, keep int) error {
	if keep <= 0 {
		keep = DefaultRetainRuns
	}
	entries, err := os.ReadDir(runsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("capture: prune list: %w", err)
	}
	type dated struct {
		name string
		at   time.Time
	}
	dirs := make([]dated, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(runsRoot, entry.Name())
		// Zero time sorts as the oldest entry, so runs with missing or
		// unreadable metadata are pruned first.
		at := time.Time{}
		if meta, err := LoadMeta(dirPath); err == nil {
			at = meta.StartedAt
		}
		dirs = append(dirs, dated{name: dirPath, at: at})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].at.After(dirs[j].at) })
	if keep >= len(dirs) {
		return nil
	}
	for _, d := range dirs[keep:] {
		if err := os.RemoveAll(d.name); err != nil {
			return fmt.Errorf("capture: prune remove %s: %w", d.name, err)
		}
	}
	return nil
}

// loadRecords parses every line of a run's calls.jsonl. A missing file is
// an empty slice, not an error.
func loadRecords(dir string) ([]Record, error) {
	f, err := os.Open(filepath.Join(dir, CallsFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return []Record{}, nil
		}
		return nil, fmt.Errorf("capture: open calls file: %w", err)
	}
	defer func() { _ = f.Close() }()
	records := []Record{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("capture: parse record: %w", err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("capture: scan calls file: %w", err)
	}
	return records, nil
}

// worstStatus derives a run-level status: error beats parse_failed beats ok.
func worstStatus(records []Record) string {
	worst := StatusOK
	rank := map[string]int{StatusOK: 0, StatusParseFailed: 1, StatusError: 2}
	for _, rec := range records {
		if rank[rec.Status] > rank[worst] {
			worst = rec.Status
		}
	}
	return worst
}
