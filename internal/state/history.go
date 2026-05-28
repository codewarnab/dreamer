package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"dreamer/internal/fsutil"
)

const (
	historyFile    = "history.json"
	historyVersion = 1
	maxHistoryDays = 90
)

// History is the per-project daily rollup.
type History struct {
	Version int          `json:"version"`
	Days    []DaySummary `json:"days"`
}

// DaySummary captures one calendar-day's run counters.
type DaySummary struct {
	Date          string         `json:"date"`
	Runs          int            `json:"runs"`
	FindingsNew   int            `json:"findings_new"`
	FindingsTotal int            `json:"findings_total"`
	Tokens        int64          `json:"tokens"`
	AvgRunDurationMillis  int64          `json:"avg_run_millis"`
	PerCategory   map[string]int `json:"per_category,omitempty"`
}

// DaySummaryDelta is the per-run contribution merged into today's bucket.
type DaySummaryDelta struct {
	Runs          int
	FindingsNew   int
	FindingsTotal int
	Tokens        int64
	RunDurationMillis     int64
	PerCategory   map[string]int
}

// HistoryPath returns the per-project history.json path.
func HistoryPath(outputRoot, projectName string) (string, error) {
	statePath, err := PathForProject(outputRoot, projectName)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(statePath), historyFile), nil
}

// LoadHistory returns the on-disk history or a fresh zero-value when missing.
func LoadHistory(outputRoot, projectName string) (*History, error) {
	path, err := HistoryPath(outputRoot, projectName)
	if err != nil {
		return nil, err
	}
	historyBytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &History{Version: historyVersion}, nil
		}
		return nil, fmt.Errorf("read history %q: %w", path, err)
	}
	var history History
	if err := json.Unmarshal(historyBytes, &history); err != nil {
		return nil, fmt.Errorf("unmarshal history %q: %w", path, err)
	}
	if history.Version == 0 {
		history.Version = historyVersion
	}
	return &history, nil
}

// SaveHistory writes the history atomically.
func SaveHistory(outputRoot, projectName string, h *History) error {
	path, err := HistoryPath(outputRoot, projectName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), fsutil.DirPerms); err != nil {
		return err
	}
	historyBytes, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, historyBytes, fsutil.FilePerms)
}

// UpdateHistoryToday merges delta into the bucket whose Date == today (a
// caller-provided ISO date string in UTC). It creates the bucket if
// absent, prunes entries beyond maxHistoryDays, and persists atomically.
func UpdateHistoryToday(outputRoot, projectName, today string, delta DaySummaryDelta) error {
	history, err := LoadHistory(outputRoot, projectName)
	if err != nil {
		return err
	}
	idx := -1
	for i, d := range history.Days {
		if d.Date == today {
			idx = i
			break
		}
	}
	var bucket DaySummary
	if idx >= 0 {
		bucket = history.Days[idx]
	} else {
		bucket = DaySummary{Date: today, PerCategory: map[string]int{}}
	}
	if bucket.PerCategory == nil {
		bucket.PerCategory = map[string]int{}
	}
	priorRuns := bucket.Runs
	bucket.Runs += delta.Runs
	bucket.FindingsNew += delta.FindingsNew
	bucket.FindingsTotal = delta.FindingsTotal // most-recent snapshot wins
	bucket.Tokens += delta.Tokens
	if bucket.Runs > 0 {
		// Rolling average across runs in this day.
		bucket.AvgRunDurationMillis = (bucket.AvgRunDurationMillis*int64(priorRuns) + delta.RunDurationMillis*int64(delta.Runs)) / int64(bucket.Runs)
	}
	for category, count := range delta.PerCategory {
		bucket.PerCategory[category] += count
	}

	if idx >= 0 {
		history.Days[idx] = bucket
	} else {
		history.Days = append(history.Days, bucket)
	}
	sort.SliceStable(history.Days, func(i, j int) bool { return history.Days[i].Date < history.Days[j].Date })
	if len(history.Days) > maxHistoryDays {
		history.Days = history.Days[len(history.Days)-maxHistoryDays:]
	}
	history.Version = historyVersion
	return SaveHistory(outputRoot, projectName, history)
}
