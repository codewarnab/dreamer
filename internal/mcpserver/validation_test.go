package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateFinding_Valid(t *testing.T) {
	f := &FindingInput{
		Category:   "test",
		Mistake:    "missing edge-case test",
		Confidence: 0.9,
		Guardrail: GuardrailInput{
			Kind: "test",
			Tool: "go test",
			Rule: "TestEdgeCases",
		},
	}
	if err := ValidateFinding(f); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateFinding_InvalidCategory(t *testing.T) {
	f := &FindingInput{
		Category:   "nonexistent",
		Mistake:    "something",
		Confidence: 0.5,
		Guardrail:  GuardrailInput{Kind: "x", Tool: "y", Rule: "z"},
	}
	if err := ValidateFinding(f); err == nil {
		t.Fatal("expected error for invalid category")
	}
}

func TestValidateFinding_EmptyMistake(t *testing.T) {
	f := &FindingInput{
		Category:   "test",
		Confidence: 0.5,
		Guardrail:  GuardrailInput{Kind: "x", Tool: "y", Rule: "z"},
	}
	if err := ValidateFinding(f); err == nil {
		t.Fatal("expected error for empty mistake")
	}
}

func TestValidateFinding_BadConfidence(t *testing.T) {
	f := &FindingInput{
		Category:   "test",
		Mistake:    "something",
		Confidence: 1.5,
		Guardrail:  GuardrailInput{Kind: "x", Tool: "y", Rule: "z"},
	}
	if err := ValidateFinding(f); err == nil {
		t.Fatal("expected error for confidence > 1")
	}
}

func TestValidateFinding_EmptyGuardrailKind(t *testing.T) {
	f := &FindingInput{
		Category:   "test",
		Mistake:    "something",
		Confidence: 0.5,
		Guardrail:  GuardrailInput{Tool: "y", Rule: "z"},
	}
	if err := ValidateFinding(f); err == nil {
		t.Fatal("expected error for empty guardrail kind")
	}
}

func TestValidateFinding_AllCategories(t *testing.T) {
	for cat := range ValidCategories {
		f := &FindingInput{
			Category:   cat,
			Mistake:    "test mistake",
			Confidence: 0.5,
			Guardrail:  GuardrailInput{Kind: cat, Tool: "test", Rule: "test"},
		}
		if err := ValidateFinding(f); err != nil {
			t.Errorf("category %q should be valid: %v", cat, err)
		}
	}
}

func TestFindingRecorder_Record(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.jsonl")

	rec, err := NewFindingRecorder(path)
	if err != nil {
		t.Fatalf("NewFindingRecorder: %v", err)
	}
	defer rec.Close()

	f := FindingInput{
		Category:   "test",
		Mistake:    "missing edge-case test",
		Confidence: 0.9,
		Guardrail:  GuardrailInput{Kind: "test", Tool: "go test", Rule: "TestEdgeCases"},
	}

	total, err := rec.Record(&f)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total 1, got %d", total)
	}

	// Record another
	total, err = rec.Record(&f)
	if err != nil {
		t.Fatalf("Record second: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total 2, got %d", total)
	}

	if rec.Count() != 2 {
		t.Fatalf("expected Count() 2, got %d", rec.Count())
	}
}

func TestFindingRecorder_InvalidRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.jsonl")

	rec, err := NewFindingRecorder(path)
	if err != nil {
		t.Fatalf("NewFindingRecorder: %v", err)
	}
	defer rec.Close()

	f := FindingInput{
		Category:   "nonexistent",
		Mistake:    "test",
		Confidence: 0.5,
		Guardrail:  GuardrailInput{Kind: "x", Tool: "y", Rule: "z"},
	}

	_, err = rec.Record(&f)
	if err == nil {
		t.Fatal("expected error for invalid finding")
	}
	if rec.Count() != 0 {
		t.Fatalf("expected Count() 0, got %d", rec.Count())
	}
}

func TestReadFindingsJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.jsonl")

	// Write test data
	data := []FindingInput{
		{Category: "test", Mistake: "first", Confidence: 0.9, Guardrail: GuardrailInput{Kind: "test", Tool: "t", Rule: "r"}},
		{Category: "lint-rule", Mistake: "second", Confidence: 0.8, Guardrail: GuardrailInput{Kind: "lint-rule", Tool: "g", Rule: "g"}},
	}
	b, err := json.Marshal(data[0])
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(data[1])
	os.WriteFile(path, append(append(b, '\n'), append(b2, '\n')...), 0o644)

	findings, err := ReadFindingsJSONL(path)
	if err != nil {
		t.Fatalf("ReadFindingsJSONL: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	if findings[0].Mistake != "first" {
		t.Errorf("expected first mistake 'first', got %q", findings[0].Mistake)
	}
	if findings[1].Category != "lint-rule" {
		t.Errorf("expected second category 'lint-rule', got %q", findings[1].Category)
	}
}

func TestReadFindingsJSONL_Nonexistent(t *testing.T) {
	findings, err := ReadFindingsJSONL("/nonexistent/path.jsonl")
	if err != nil {
		t.Fatalf("expected nil error for nonexistent file, got: %v", err)
	}
	if findings != nil {
		t.Fatalf("expected nil findings for nonexistent file, got %v", findings)
	}
}

func TestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.jsonl")

	rec, err := NewFindingRecorder(path)
	if err != nil {
		t.Fatalf("NewFindingRecorder: %v", err)
	}

	original := FindingInput{
		Category:   "refactor-boundary",
		Mistake:    "direct DB access in handler",
		Confidence: 0.85,
		Guardrail: GuardrailInput{
			Kind:          "refactor-boundary",
			Tool:          "golangci-lint",
			Rule:          "layered-arch",
			ConfigSnippet: "linters-settings.layered-arch.enabled: true",
			Apply: &ApplyInput{
				TargetFile: ".golangci.yml",
				Strategy:   "append-section",
				Anchor:     "linters-settings:",
				Snippet:    "  layered-arch:\n    enabled: true",
			},
		},
		CodebaseEvidence: []EvidenceInput{
			{Path: "internal/handler/user.go", Lines: "45-52", Symbol: "CreateUser"},
		},
	}

	total, err := rec.Record(&original)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total 1, got %d", total)
	}
	rec.Close()

	// Read back
	findings, err := ReadFindingsJSONL(path)
	if err != nil {
		t.Fatalf("ReadFindingsJSONL: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	got := findings[0]
	if got.Category != original.Category {
		t.Errorf("category: got %q, want %q", got.Category, original.Category)
	}
	if got.Mistake != original.Mistake {
		t.Errorf("mistake: got %q, want %q", got.Mistake, original.Mistake)
	}
	if got.Guardrail.ConfigSnippet != original.Guardrail.ConfigSnippet {
		t.Errorf("config snippet: got %q, want %q", got.Guardrail.ConfigSnippet, original.Guardrail.ConfigSnippet)
	}
	if got.Guardrail.Apply == nil {
		t.Fatal("expected Apply to be non-nil")
	}
	if got.Guardrail.Apply.TargetFile != ".golangci.yml" {
		t.Errorf("apply target: got %q, want .golangci.yml", got.Guardrail.Apply.TargetFile)
	}
	if len(got.CodebaseEvidence) != 1 {
		t.Fatalf("expected 1 evidence, got %d", len(got.CodebaseEvidence))
	}
	if got.CodebaseEvidence[0].Symbol != "CreateUser" {
		t.Errorf("evidence symbol: got %q, want CreateUser", got.CodebaseEvidence[0].Symbol)
	}
}
