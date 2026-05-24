package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/mcpserver"
)

// --- Tail tests ---

func TestJsonPhase2Tail_NilLead(t *testing.T) {
	tail, warnings := jsonPhase2Tail(PhaseRequest{}, nil)
	if tail != "" {
		t.Fatalf("expected empty tail, got %q", tail)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestJsonPhase2Tail_WithSchema(t *testing.T) {
	pack := RulePack{Phase2ResponseSchema: `{"type":"object"}`}
	tail, warnings := jsonPhase2Tail(PhaseRequest{}, &pack)
	if !strings.Contains(tail, "Return JSON only") {
		t.Fatalf("tail missing JSON instruction: %q", tail)
	}
	if !strings.Contains(tail, `{"type":"object"}`) {
		t.Fatalf("tail missing schema: %q", tail)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestMcpPhase2Tail_NilLead(t *testing.T) {
	tail, warnings := mcpPhase2Tail(PhaseRequest{}, nil)
	if tail != "" {
		t.Fatalf("expected empty tail, got %q", tail)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestMcpPhase2Tail_WithInstructions(t *testing.T) {
	pack := RulePack{Phase2RecordingInstructions: "use the record_finding tool"}
	tail, warnings := mcpPhase2Tail(PhaseRequest{}, &pack)
	if !strings.Contains(tail, "record_finding") {
		t.Fatalf("tail missing instructions: %q", tail)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestMcpPhase2Tail_MissingInstructionsWarns(t *testing.T) {
	pack := RulePack{Phase2ResponseSchema: `{"type":"object"}`}
	tail, warnings := mcpPhase2Tail(PhaseRequest{}, &pack)
	// Should fall back to JSON tail but emit a warning.
	if !strings.Contains(tail, "Return JSON only") {
		t.Fatalf("expected JSON fallback tail, got %q", tail)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning about missing recording instructions")
	}
	if !strings.Contains(warnings[0], "missing phase2_recording_instructions") {
		t.Fatalf("warning text wrong: %q", warnings[0])
	}
}

func TestCLIPhase2Tail_DefaultBinary(t *testing.T) {
	tail, warnings := cliPhase2Tail(PhaseRequest{FindingsOutputPath: "/tmp/test.jsonl"}, nil)
	if !strings.Contains(tail, "dreamer record-finding") {
		t.Fatalf("tail missing default binary: %q", tail)
	}
	if !strings.Contains(tail, "/tmp/test.jsonl") {
		t.Fatalf("tail missing output path: %q", tail)
	}
	if !strings.Contains(tail, "cat <<'ENDOFFINDING'") {
		t.Fatalf("tail missing heredoc: %q", tail)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestCLIPhase2Tail_CustomBinary(t *testing.T) {
	req := PhaseRequest{
		FindingsOutputPath: "/tmp/out.jsonl",
		CLIBinaryPath:      "/usr/local/bin/dreamer",
	}
	tail, _ := cliPhase2Tail(req, nil)
	if !strings.Contains(tail, "/usr/local/bin/dreamer record-finding") {
		t.Fatalf("tail missing custom binary: %q", tail)
	}
}

// --- Lookup tests ---

func TestLookupPhase2Decoder_AllModes(t *testing.T) {
	modes := []Phase2Mode{Phase2ModeNone, Phase2ModeMCP, Phase2ModeCLI}
	for _, mode := range modes {
		d := lookupPhase2Decoder(mode)
		if d.tail == nil || d.decode == nil {
			t.Errorf("mode %v: nil tail or decode", mode)
		}
	}
}

func TestLookupPhase2Decoder_UnknownFallsBack(t *testing.T) {
	d := lookupPhase2Decoder(Phase2Mode("unknown-mode"))
	none := lookupPhase2Decoder(Phase2ModeNone)
	// Should fall back to JSON decoder (Phase2ModeNone).
	if d.decode == nil || none.decode == nil {
		t.Fatal("nil decoder")
	}
	// Both should produce the same tail for the same input.
	tailA, _ := d.tail(PhaseRequest{}, nil)
	tailB, _ := none.tail(PhaseRequest{}, nil)
	if tailA != tailB {
		t.Fatalf("fallback tail mismatch: %q vs %q", tailA, tailB)
	}
}

// --- filePhase2Decode tests ---

func TestFilePhase2Decode_EmptyPath(t *testing.T) {
	_, _, err := filePhase2Decode("", PhaseRequest{}, nil)
	if err == nil || !strings.Contains(err.Error(), "FindingsOutputPath is empty") {
		t.Fatalf("expected 'empty path' error, got: %v", err)
	}
}

func TestFilePhase2Decode_MissingFileReturnsEmpty(t *testing.T) {
	// A missing file means the model recorded zero findings — not an error.
	findings, warnings, err := filePhase2Decode("", PhaseRequest{FindingsOutputPath: "/nonexistent/path.jsonl"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for missing file, got %d", len(findings))
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for missing file, got %d", len(warnings))
	}
}

func TestFilePhase2Decode_EmptyFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	findings, warnings, err := filePhase2Decode("", PhaseRequest{FindingsOutputPath: f}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings from empty file, got %d", len(findings))
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %v", warnings)
	}
}

func TestFilePhase2Decode_WithFindings(t *testing.T) {
	f := filepath.Join(t.TempDir(), "findings.jsonl")
	lines := []string{
		`{"category":"lint-rule","mistake":"no nil check","guardrail":{"kind":"lint-rule","tool":"golangci-lint","rule":"nilcheck"},"confidence":0.9}`,
		`{"category":"test","mistake":"flaky assertion","guardrail":{"kind":"test","tool":"go test","rule":"assert"},"confidence":0.8}`,
	}
	if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	packs := []RulePack{
		{Category: RuleCategoryLintRule, Enabled: true},
		{Category: RuleCategoryTest, Enabled: true},
	}
	findings, warnings, err := filePhase2Decode("", PhaseRequest{FindingsOutputPath: f}, packs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %v", warnings)
	}
	if len(findings[RuleCategoryLintRule]) != 1 {
		t.Fatalf("expected 1 lint-rule finding, got %d", len(findings[RuleCategoryLintRule]))
	}
	if len(findings[RuleCategoryTest]) != 1 {
		t.Fatalf("expected 1 test finding, got %d", len(findings[RuleCategoryTest]))
	}
}

func TestFilePhase2Decode_UnknownCategoryWarns(t *testing.T) {
	f := filepath.Join(t.TempDir(), "findings.jsonl")
	line := `{"category":"unknown-cat","mistake":"something","guardrail":{"kind":"unknown-cat","tool":"x","rule":"y"},"confidence":0.5}`
	if err := os.WriteFile(f, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	packs := []RulePack{{Category: RuleCategoryLintRule, Enabled: true}}
	_, warnings, err := filePhase2Decode("", PhaseRequest{FindingsOutputPath: f}, packs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "unknown/disabled") {
		t.Fatalf("expected unknown-category warning, got: %v", warnings)
	}
}

func TestFilePhase2Decode_DuplicateDedup(t *testing.T) {
	f := filepath.Join(t.TempDir(), "findings.jsonl")
	// Same finding twice — should be deduped by hash.
	line := `{"category":"lint-rule","mistake":"no nil check","guardrail":{"kind":"lint-rule","tool":"golangci-lint","rule":"nilcheck"},"confidence":0.9}`
	if err := os.WriteFile(f, []byte(line+"\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	packs := []RulePack{{Category: RuleCategoryLintRule, Enabled: true}}
	findings, _, err := filePhase2Decode("", PhaseRequest{FindingsOutputPath: f}, packs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings[RuleCategoryLintRule]) != 1 {
		t.Fatalf("expected 1 finding after dedup, got %d", len(findings[RuleCategoryLintRule]))
	}
}

// --- materializeRecordedFindings tests ---

func TestMaterializeRecordedFindings_Empty(t *testing.T) {
	findings, warnings, err := materializeRecordedFindings(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 || len(warnings) != 0 {
		t.Fatalf("expected empty result, got findings=%v warnings=%v", findings, warnings)
	}
}

func TestMaterializeRecordedFindings_SkipsEmptyMistake(t *testing.T) {
	raw := []mcpserver.FindingInput{
		{Category: "lint-rule", Mistake: "  ", Guardrail: mcpserver.GuardrailInput{Kind: "lint-rule"}},
	}
	packs := []RulePack{{Category: RuleCategoryLintRule, Enabled: true}}
	findings, _, err := materializeRecordedFindings(raw, packs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings[RuleCategoryLintRule]) != 0 {
		t.Fatalf("expected 0 findings (empty mistake), got %d", len(findings[RuleCategoryLintRule]))
	}
}

func TestMaterializeRecordedFindings_WithEvidence(t *testing.T) {
	raw := []mcpserver.FindingInput{
		{
			Category:  "lint-rule",
			Mistake:   "no error check",
			Confidence: 0.95,
			Guardrail: mcpserver.GuardrailInput{Kind: "lint-rule", Tool: "go vet", Rule: "errcheck"},
			CodebaseEvidence: []mcpserver.EvidenceInput{
				{Path: "main.go", Lines: "10-15", Symbol: "doStuff"},
			},
		},
	}
	packs := []RulePack{{Category: RuleCategoryLintRule, Enabled: true}}
	findings, _, err := materializeRecordedFindings(raw, packs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings[RuleCategoryLintRule]) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings[RuleCategoryLintRule]))
	}
	f := findings[RuleCategoryLintRule][0]
	if len(f.Evidence) != 1 || f.Evidence[0].Path != "main.go" {
		t.Fatalf("evidence mismatch: %+v", f.Evidence)
	}
	if f.Confidence != 0.95 {
		t.Fatalf("confidence = %f, want 0.95", f.Confidence)
	}
}

func TestMaterializeRecordedFindings_DefaultGuardrailKind(t *testing.T) {
	// When Guardrail.Kind is empty, buildFinding should default to category.
	raw := []mcpserver.FindingInput{
		{Category: "test", Mistake: "flaky test", Guardrail: mcpserver.GuardrailInput{Tool: "go test", Rule: "flaky"}},
	}
	packs := []RulePack{{Category: RuleCategoryTest, Enabled: true}}
	findings, _, err := materializeRecordedFindings(raw, packs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f := findings[RuleCategoryTest][0]
	if f.Guardrail.Kind != string(RuleCategoryTest) {
		t.Fatalf("guardrail kind = %q, want %q", f.Guardrail.Kind, RuleCategoryTest)
	}
}

// --- applyFindingRedactor tests ---

func TestApplyFindingRedactor_NilRedactor(t *testing.T) {
	findings := map[RuleCategory][]Finding{
		RuleCategoryLintRule: {{Mistake: "secret-password"}},
	}
	applyFindingRedactor(findings, nil)
	if findings[RuleCategoryLintRule][0].Mistake != "secret-password" {
		t.Fatal("nil redactor should not modify findings")
	}
}

func TestApplyFindingRedactor_ScrubsAllTextFields(t *testing.T) {
	redact := func(s string) string { return "[REDACTED]" }
	apply := &ApplySpec{Snippet: "secret code", Anchor: "secret anchor"}
	findings := map[RuleCategory][]Finding{
		RuleCategoryLintRule: {{
			Mistake:    "secret mistake",
			Guardrail:  Guardrail{Kind: "k", Tool: "secret tool", Rule: "secret rule", ConfigSnippet: "secret config", Apply: apply},
			Evidence:   []CodebaseEvidence{{Path: "main.go", Lines: "1-2", Symbol: "secret sym"}},
		}},
	}
	applyFindingRedactor(findings, redact)
	f := findings[RuleCategoryLintRule][0]

	if f.Mistake != "[REDACTED]" {
		t.Errorf("Mistake not redacted: %q", f.Mistake)
	}
	if f.Guardrail.Tool != "[REDACTED]" {
		t.Errorf("Tool not redacted: %q", f.Guardrail.Tool)
	}
	if f.Guardrail.Rule != "[REDACTED]" {
		t.Errorf("Rule not redacted: %q", f.Guardrail.Rule)
	}
	if f.Guardrail.ConfigSnippet != "[REDACTED]" {
		t.Errorf("ConfigSnippet not redacted: %q", f.Guardrail.ConfigSnippet)
	}
	if f.Guardrail.Apply.Snippet != "[REDACTED]" {
		t.Errorf("Apply.Snippet not redacted: %q", f.Guardrail.Apply.Snippet)
	}
	if f.Guardrail.Apply.Anchor != "[REDACTED]" {
		t.Errorf("Apply.Anchor not redacted: %q", f.Guardrail.Apply.Anchor)
	}
	if f.Evidence[0].Symbol != "[REDACTED]" {
		t.Errorf("Symbol not redacted: %q", f.Evidence[0].Symbol)
	}
	// Paths and lines should NOT be redacted.
	if f.Evidence[0].Path != "main.go" {
		t.Errorf("Path should not be redacted: %q", f.Evidence[0].Path)
	}
	if f.Evidence[0].Lines != "1-2" {
		t.Errorf("Lines should not be redacted: %q", f.Evidence[0].Lines)
	}
}

func TestApplyFindingRedactor_NilApply(t *testing.T) {
	redact := func(s string) string { return "[R]" }
	findings := map[RuleCategory][]Finding{
		RuleCategoryTest: {{Mistake: "x", Guardrail: Guardrail{Tool: "y", Rule: "z"}}},
	}
	// Should not panic with nil Apply.
	applyFindingRedactor(findings, redact)
	if findings[RuleCategoryTest][0].Mistake != "[R]" {
		t.Fatal("Mistake not redacted")
	}
}

// --- buildPhase2Tail integration ---

func TestBuildPhase2Tail_DispatchesToRegisteredMode(t *testing.T) {
	// MCP mode with instructions should use mcpPhase2Tail.
	pack := &RulePack{Phase2RecordingInstructions: "use the tool"}
	req := PhaseRequest{Phase2Mode: Phase2ModeMCP}
	tail, warnings := buildPhase2Tail(req, pack)
	if !strings.Contains(tail, "use the tool") {
		t.Fatalf("MCP tail wrong: %q", tail)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

func TestBuildPhase2Tail_CLIContainsHeredoc(t *testing.T) {
	req := PhaseRequest{Phase2Mode: Phase2ModeCLI, FindingsOutputPath: "/tmp/out.jsonl", CLIBinaryPath: "dreamer"}
	tail, _ := buildPhase2Tail(req, nil)
	if !strings.Contains(tail, "cat <<'ENDOFFINDING'") {
		t.Fatalf("CLI tail missing heredoc: %q", tail)
	}
}

func TestBuildPhase2Tail_NoneUsesJSON(t *testing.T) {
	pack := &RulePack{Phase2ResponseSchema: `{"findings":[]}`}
	req := PhaseRequest{Phase2Mode: Phase2ModeNone}
	tail, _ := buildPhase2Tail(req, pack)
	if !strings.Contains(tail, "Return JSON only") {
		t.Fatalf("None tail should use JSON: %q", tail)
	}
}
