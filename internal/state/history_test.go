package state

import (
	"testing"
	"time"
)

func TestHistory_UpdateToday_CreatesAndIncrements(t *testing.T) {
	dir := t.TempDir()
	if err := UpdateHistoryToday(dir, "proj", "2026-05-20", DaySummaryDelta{Runs: 1, FindingsNew: 3, PerCategory: map[string]int{"doc": 2, "test": 1}, RunMillis: 12000}); err != nil {
		t.Fatalf("update: %v", err)
	}
	h, err := LoadHistory(dir, "proj")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(h.Days) != 1 || h.Days[0].Date != "2026-05-20" {
		t.Fatalf("days = %+v", h.Days)
	}
	if h.Days[0].Runs != 1 || h.Days[0].FindingsNew != 3 || h.Days[0].AvgRunMillis != 12000 {
		t.Fatalf("day = %+v", h.Days[0])
	}
	if err := UpdateHistoryToday(dir, "proj", "2026-05-20", DaySummaryDelta{Runs: 1, FindingsNew: 2, PerCategory: map[string]int{"doc": 1}, RunMillis: 18000}); err != nil {
		t.Fatalf("update 2: %v", err)
	}
	h, err = LoadHistory(dir, "proj")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if h.Days[0].Runs != 2 || h.Days[0].FindingsNew != 5 || h.Days[0].PerCategory["doc"] != 3 {
		t.Fatalf("merged day = %+v", h.Days[0])
	}
	if h.Days[0].AvgRunMillis != 15000 {
		t.Fatalf("avg = %d, want 15000", h.Days[0].AvgRunMillis)
	}
}

func TestHistory_PrunesPast90Days(t *testing.T) {
	dir := t.TempDir()
	h := &History{Version: 1}
	for i := 0; i < 95; i++ {
		day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format("2006-01-02")
		h.Days = append(h.Days, DaySummary{Date: day, Runs: 1, PerCategory: map[string]int{}})
	}
	if err := SaveHistory(dir, "proj", h); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := UpdateHistoryToday(dir, "proj", "2026-04-15", DaySummaryDelta{Runs: 1, RunMillis: 1000, PerCategory: map[string]int{"doc": 1}}); err != nil {
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
