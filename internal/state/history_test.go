package state

import (
	"testing"
	"time"
)

func TestHistory_UpdateToday_CreatesAndIncrements(t *testing.T) {
	dir := t.TempDir()
	if err := UpdateHistoryToday(dir, "proj", "2026-05-20", DaySummaryDelta{Runs: 1, FindingsNew: 3, PerCategory: map[string]int{"doc": 2, "test": 1}, RunDurationMillis: 12000}); err != nil {
		t.Fatalf("update: %v", err)
	}
	history, err := LoadHistory(dir, "proj")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(history.Days) != 1 || history.Days[0].Date != "2026-05-20" {
		t.Fatalf("days = %+v", history.Days)
	}
	if history.Days[0].Runs != 1 || history.Days[0].FindingsNew != 3 || history.Days[0].AvgRunDurationMillis != 12000 {
		t.Fatalf("day = %+v", history.Days[0])
	}
	if err := UpdateHistoryToday(dir, "proj", "2026-05-20", DaySummaryDelta{Runs: 1, FindingsNew: 2, PerCategory: map[string]int{"doc": 1}, RunDurationMillis: 18000}); err != nil {
		t.Fatalf("update 2: %v", err)
	}
	history, err = LoadHistory(dir, "proj")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if history.Days[0].Runs != 2 || history.Days[0].FindingsNew != 5 || history.Days[0].PerCategory["doc"] != 3 {
		t.Fatalf("merged day = %+v", history.Days[0])
	}
	if history.Days[0].AvgRunDurationMillis != 15000 {
		t.Fatalf("avg = %d, want 15000", history.Days[0].AvgRunDurationMillis)
	}
}

func TestHistory_PrunesPast90Days(t *testing.T) {
	dir := t.TempDir()
	history := &History{Version: 1}
	for i := 0; i < 95; i++ {
		day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format("2006-01-02")
		history.Days = append(history.Days, DaySummary{Date: day, Runs: 1, PerCategory: map[string]int{}})
	}
	if err := SaveHistory(dir, "proj", history); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := UpdateHistoryToday(dir, "proj", "2026-04-15", DaySummaryDelta{Runs: 1, RunDurationMillis: 1000, PerCategory: map[string]int{"doc": 1}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	loaded, err := LoadHistory(dir, "proj")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Days) > maxHistoryDays {
		t.Fatalf("days = %d, want <= %d", len(loaded.Days), maxHistoryDays)
	}
	// Oldest entries (2026-01-*) should have been pruned.
	for _, d := range loaded.Days {
		if d.Date == "2026-01-01" {
			t.Fatalf("oldest entry not pruned: %+v", d)
		}
	}
}
