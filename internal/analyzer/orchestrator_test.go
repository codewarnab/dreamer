package analyzer

import (
	"encoding/json"
	"math"
	"testing"
)

func TestGuardrail_ApplyFieldRoundTrips(t *testing.T) {
	in := `{"kind":"doc","tool":"CLAUDE.md","rule":"Cache","config_snippet":"x","apply":{"target_file":"CLAUDE.md","strategy":"append-section","anchor":"Cache","snippet":"x"}}`
	var g Guardrail
	if err := json.Unmarshal([]byte(in), &g); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if g.Apply == nil {
		t.Fatalf("Apply nil")
	}
	if g.Apply.TargetFile != "CLAUDE.md" || g.Apply.Strategy != "append-section" {
		t.Fatalf("Apply = %+v", g.Apply)
	}
}

// B14: confidence threshold filter must drop missing/zero and NaN
// confidences rather than letting them slip through.
func TestFilterMistakesByThresholdDropsZeroAndNaN(t *testing.T) {
	threshold := 0.5
	in := []Mistake{
		{Summary: "high", Confidence: 0.9},
		{Summary: "low", Confidence: 0.3},
		{Summary: "boundary", Confidence: 0.5},
		{Summary: "missing", Confidence: 0},
		{Summary: "nan", Confidence: math.NaN()},
		{Summary: "negative", Confidence: -0.1},
		{Summary: "neg-inf", Confidence: math.Inf(-1)},
	}

	out := filterMistakesByThreshold(in, threshold)

	gotSummaries := map[string]bool{}
	for _, m := range out {
		gotSummaries[m.Summary] = true
	}
	if !gotSummaries["high"] {
		t.Errorf("high-confidence mistake dropped")
	}
	if !gotSummaries["boundary"] {
		t.Errorf("boundary-confidence mistake at threshold dropped (must use >=)")
	}
	if gotSummaries["low"] {
		t.Errorf("low-confidence mistake admitted")
	}
	if gotSummaries["missing"] {
		t.Errorf("missing/zero confidence admitted (must drop)")
	}
	if gotSummaries["nan"] {
		t.Errorf("NaN confidence admitted (must drop)")
	}
	if gotSummaries["negative"] {
		t.Errorf("negative confidence admitted (must drop)")
	}
	if gotSummaries["neg-inf"] {
		t.Errorf("-Inf confidence admitted (must drop)")
	}
}

func TestValidateFindingsDropsZeroAndNaNConfidence(t *testing.T) {
	pack := RulePack{Category: RuleCategoryTest, Threshold: 0.5}
	req := PhaseRequest{ExistingHashes: map[string]struct{}{}}
	in := []Finding{
		{Mistake: "high", Confidence: 0.9, Hash: "h1"},
		{Mistake: "boundary", Confidence: 0.5, Hash: "h2"},
		{Mistake: "low", Confidence: 0.3, Hash: "h3"},
		{Mistake: "missing", Confidence: 0, Hash: "h4"},
		{Mistake: "nan", Confidence: math.NaN(), Hash: "h5"},
	}
	out, _ := validateFindings(in, pack, req)
	seen := map[string]bool{}
	for _, f := range out {
		seen[f.Mistake] = true
	}
	if !seen["high"] || !seen["boundary"] {
		t.Errorf("high/boundary findings missing: got %v", seen)
	}
	if seen["low"] || seen["missing"] || seen["nan"] {
		t.Errorf("low/missing/NaN must be dropped: got %v", seen)
	}
}
