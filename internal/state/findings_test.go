package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestState_Findings_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	testState := &State{
		Version: StateVersion,
		Findings: map[string]FindingState{
			"abc123": {Status: "applied", AppliedAt: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC), AppliedReversal: &FindingReversal{
				Path:            filepath.Join(dir, "CLAUDE.md"),
				Strategy:        "append-section",
				PreImageSHA256:  "deadbeef",
				PostImageSHA256: "cafebabe",
				PreImage:        "original content",
			}},
			"def456": {Status: "dismissed", DismissedAt: time.Date(2026, 5, 20, 12, 5, 0, 0, time.UTC)},
		},
	}
	if err := Save(dir, "proj", testState); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(dir, "proj")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Findings) != 2 {
		t.Fatalf("Findings len = %d, want 2", len(loaded.Findings))
	}
	if loaded.Findings["abc123"].Status != "applied" {
		t.Fatalf("Findings[abc123].Status = %q", loaded.Findings["abc123"].Status)
	}
	if loaded.Findings["abc123"].AppliedReversal == nil ||
		loaded.Findings["abc123"].AppliedReversal.PreImageSHA256 != "deadbeef" {
		t.Fatalf("AppliedReversal not round-tripped: %+v", loaded.Findings["abc123"].AppliedReversal)
	}
	if loaded.Findings["def456"].Status != "dismissed" {
		t.Fatalf("Findings[def456].Status = %q", loaded.Findings["def456"].Status)
	}
}

func TestState_LegacyFile_LoadsWithNilFindings(t *testing.T) {
	dir := t.TempDir()
	st := &State{Version: StateVersion}
	if err := Save(dir, "proj", st); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(dir, "proj")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Findings == nil {
		t.Fatalf("Findings should be empty map, not nil, after Load normalization")
	}
	if len(loaded.Findings) != 0 {
		t.Fatalf("Findings len = %d, want 0", len(loaded.Findings))
	}
}
