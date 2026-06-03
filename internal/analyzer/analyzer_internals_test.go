package analyzer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"dreamer/internal/logging"
)

// --- normalizeMistakes ---

func TestNormalizeMistakesFiltersEmptySummary(t *testing.T) {
	in := []Mistake{
		{Summary: "real issue", Confidence: 0.8},
		{Summary: "", Confidence: 0.9},
		{Summary: "   ", Confidence: 0.7},
	}
	out := normalizeMistakes(in, RuleCategoryTest)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	if out[0].Summary != "real issue" {
		t.Fatalf("got %q, want %q", out[0].Summary, "real issue")
	}
}

func TestNormalizeMistakesDefaultCategory(t *testing.T) {
	in := []Mistake{
		{Summary: "no category"},
	}
	out := normalizeMistakes(in, RuleCategoryLintRule)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	if out[0].Category != RuleCategoryLintRule {
		t.Fatalf("got %q, want %q", out[0].Category, RuleCategoryLintRule)
	}
}

func TestNormalizeMistakesKeepsExplicitCategory(t *testing.T) {
	in := []Mistake{
		{Summary: "has category", Category: RuleCategoryConfig},
	}
	out := normalizeMistakes(in, RuleCategoryTest)
	if out[0].Category != RuleCategoryConfig {
		t.Fatalf("got %q, want %q", out[0].Category, RuleCategoryConfig)
	}
}

func TestNormalizeMistakesTrimsWhitespace(t *testing.T) {
	in := []Mistake{
		{Summary: "  spaced  ", EvidenceExcerpt: "  excerpt  "},
	}
	out := normalizeMistakes(in, RuleCategoryTest)
	if out[0].Summary != "spaced" {
		t.Fatalf("Summary = %q, want %q", out[0].Summary, "spaced")
	}
	if out[0].EvidenceExcerpt != "excerpt" {
		t.Fatalf("EvidenceExcerpt = %q, want %q", out[0].EvidenceExcerpt, "excerpt")
	}
}

func TestNormalizeMistakesEmpty(t *testing.T) {
	out := normalizeMistakes(nil, RuleCategoryTest)
	if len(out) != 0 {
		t.Fatalf("expected 0, got %d", len(out))
	}
}

// --- stripCodeFence ---

func TestStripCodeFenceNoFence(t *testing.T) {
	got := stripCodeFence(`{"key":"value"}`)
	if got != `{"key":"value"}` {
		t.Fatalf("got %q", got)
	}
}

func TestStripCodeFenceWithFence(t *testing.T) {
	input := "```json\n{\"key\":\"value\"}\n```"
	got := stripCodeFence(input)
	if got != `{"key":"value"}` {
		t.Fatalf("got %q", got)
	}
}

func TestStripCodeFenceFenceOnly(t *testing.T) {
	input := "```\nraw content\n```"
	got := stripCodeFence(input)
	if got != "raw content" {
		t.Fatalf("got %q", got)
	}
}

func TestStripCodeFenceEmpty(t *testing.T) {
	got := stripCodeFence("")
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestStripCodeFenceWhitespace(t *testing.T) {
	got := stripCodeFence("   ")
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestStripCodeFencePartialFence(t *testing.T) {
	// Has opening but no closing — stripCodeFence strips the opening fence
	// line and returns the rest.
	input := "```json\n{\"key\":\"value\"}"
	got := stripCodeFence(input)
	// The opening ``` line is stripped even without a closing fence.
	if !strings.Contains(got, `"key"`) {
		t.Fatalf("expected content after partial fence strip: got %q", got)
	}
}

// --- normalizeForHash ---

func TestNormalizeForHash(t *testing.T) {
	got := normalizeForHash("  hello   world  ")
	if got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestNormalizeForHashTabs(t *testing.T) {
	got := normalizeForHash("\thello\t\tworld\t")
	if got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestNormalizeForHashEmpty(t *testing.T) {
	if got := normalizeForHash(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

// --- writeGroundingPreamble ---

func TestWriteGroundingPreambleAllFields(t *testing.T) {
	var sb strings.Builder
	req := PhaseRequest{
		ProjectRoot:      "/tmp/project",
		ToolchainSummary: "Go 1.22",
		PrimaryLinter:    "golangci-lint",
		TestFramework:    "go test",
		RunID:            "run-1",
	}
	writeGroundingPreamble(&sb, req)
	got := sb.String()
	for _, want := range []string{"Project root: /tmp/project", "Toolchain: Go 1.22", "Primary linter: golangci-lint", "Test framework: go test", "Session ID: run-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}

func TestWriteGroundingPreambleEmptyFields(t *testing.T) {
	var sb strings.Builder
	writeGroundingPreamble(&sb, PhaseRequest{})
	if sb.Len() != 0 {
		t.Fatalf("expected empty output for empty request, got %q", sb.String())
	}
}

func TestWriteGroundingPreambleWhitespaceFields(t *testing.T) {
	var sb strings.Builder
	writeGroundingPreamble(&sb, PhaseRequest{ProjectRoot: "   "})
	if sb.Len() != 0 {
		t.Fatalf("expected empty output for whitespace-only fields, got %q", sb.String())
	}
}

// --- firstEnabledPack ---

func TestFirstEnabledPack(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryLintRule, Enabled: false},
		{Category: RuleCategoryTest, Enabled: true},
		{Category: RuleCategoryCICheck, Enabled: true},
	}
	got := firstEnabledPack(packs)
	if got == nil || got.Category != RuleCategoryTest {
		t.Fatalf("got %+v, want test pack", got)
	}
}

func TestFirstEnabledPackNoneEnabled(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryLintRule, Enabled: false},
	}
	if got := firstEnabledPack(packs); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestFirstEnabledPackEmpty(t *testing.T) {
	if got := firstEnabledPack(nil); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

// --- EnabledCategories ---

func TestEnabledCategories(t *testing.T) {
	b := NewPromptBuilder([]RulePack{
		{Category: RuleCategoryLintRule, Enabled: true},
		{Category: RuleCategoryTest, Enabled: false},
		{Category: RuleCategoryCICheck, Enabled: true},
	})
	got := b.EnabledCategories()
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0] != RuleCategoryLintRule || got[1] != RuleCategoryCICheck {
		t.Fatalf("got %v", got)
	}
}

func TestEnabledCategoriesNone(t *testing.T) {
	b := NewPromptBuilder([]RulePack{
		{Category: RuleCategoryTest, Enabled: false},
	})
	got := b.EnabledCategories()
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

// --- categoryLookup ---

func TestCategoryLookup(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryLintRule, Enabled: true},
		{Category: RuleCategoryTest, Enabled: true},
	}
	lookup := categoryLookup(packs)
	if len(lookup) != 2 {
		t.Fatalf("expected 2, got %d", len(lookup))
	}
	if _, ok := lookup["lint-rule"]; !ok {
		t.Fatal("missing lint-rule")
	}
}

// --- sortedCategoryKeys ---

func TestSortedCategoryKeys(t *testing.T) {
	m := map[RuleCategory][]Mistake{
		RuleCategoryCICheck:  {{Summary: "a"}},
		RuleCategoryLintRule: {{Summary: "b"}},
		RuleCategoryTest:     {{Summary: "c"}},
	}
	got := sortedCategoryKeys(m)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	// Should be alphabetical
	if got[0] >= got[1] || got[1] >= got[2] {
		t.Fatalf("not sorted: %v", got)
	}
}

func TestSortedCategoryKeysEmpty(t *testing.T) {
	got := sortedCategoryKeys(nil)
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

// --- validateAndBuildFindings ---

func TestValidateAndBuildFindingsFiltersEmptyMistake(t *testing.T) {
	raws := []unvalidatedFinding{
		{Mistake: "", Confidence: 0.9},
		{Mistake: "  ", Confidence: 0.9},
		{Mistake: "real", Confidence: 0.8},
	}
	findings := validateAndBuildFindings(raws, RuleCategoryTest)
	if len(findings) != 1 {
		t.Fatalf("expected 1, got %d", len(findings))
	}
	if findings[0].Mistake != "real" {
		t.Fatalf("got %q", findings[0].Mistake)
	}
}

func TestValidateAndBuildFindingsEmpty(t *testing.T) {
	findings := validateAndBuildFindings(nil, RuleCategoryTest)
	if len(findings) != 0 {
		t.Fatalf("expected 0, got %d", len(findings))
	}
}

func TestValidateAndBuildFindingsWithEvidence(t *testing.T) {
	raws := []unvalidatedFinding{
		{
			Mistake:    "issue",
			Confidence: 0.8,
			Guardrail:  Guardrail{Tool: "go-vet"},
			CodebaseEvidence: []CodebaseEvidence{
				{Path: "main.go", Lines: "10"},
			},
		},
	}
	findings := validateAndBuildFindings(raws, RuleCategoryLintRule)
	if len(findings) != 1 {
		t.Fatalf("expected 1, got %d", len(findings))
	}
	if findings[0].Guardrail.Tool != "go-vet" {
		t.Fatalf("tool = %q", findings[0].Guardrail.Tool)
	}
	if len(findings[0].Evidence) != 1 {
		t.Fatalf("evidence len = %d", len(findings[0].Evidence))
	}
}

// --- clampSummary ---

func TestClampSummaryShort(t *testing.T) {
	got := clampSummary("short summary")
	if got != "short summary" {
		t.Fatalf("got %q", got)
	}
}

func TestClampSummaryLong(t *testing.T) {
	long := strings.Repeat("a", 3000)
	got := clampSummary(long)
	runes := []rune(got)
	if len(runes) > phase1SummaryMaxRunes {
		t.Fatalf("expected <= %d runes, got %d", phase1SummaryMaxRunes, len(runes))
	}
}

func TestClampSummaryEmpty(t *testing.T) {
	if got := clampSummary(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestClampSummaryWhitespace(t *testing.T) {
	if got := clampSummary("   "); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestClampSummaryExactBoundary(t *testing.T) {
	s := strings.Repeat("a", phase1SummaryMaxRunes)
	got := clampSummary(s)
	if got != s {
		t.Fatalf("boundary: expected unchanged")
	}
}

// --- ComputeFindingHash ---

func TestComputeFindingHashDeterministic(t *testing.T) {
	f := Finding{
		Mistake: "test issue",
		Guardrail: Guardrail{
			Tool: "go-vet",
			Rule: "nilcheck",
		},
	}
	h1 := ComputeFindingHash(f)
	h2 := ComputeFindingHash(f)
	if h1 != h2 {
		t.Fatalf("non-deterministic: %s != %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("hash length = %d, want 64 (SHA-256 hex)", len(h1))
	}
}

func TestComputeFindingHashDifferentForDifferentInput(t *testing.T) {
	f1 := Finding{Mistake: "issue a", Guardrail: Guardrail{Tool: "tool1"}}
	f2 := Finding{Mistake: "issue b", Guardrail: Guardrail{Tool: "tool1"}}
	if ComputeFindingHash(f1) == ComputeFindingHash(f2) {
		t.Fatal("different inputs should produce different hashes")
	}
}

// --- RuleCategory constants ---

func TestRuleCategoryConstants(t *testing.T) {
	cats := []struct {
		cat  RuleCategory
		want string
	}{
		{RuleCategoryLintRule, "lint-rule"},
		{RuleCategoryTest, "test"},
		{RuleCategoryCICheck, "ci-check"},
		{RuleCategoryDoc, "doc"},
		{RuleCategoryConfig, "config"},
		{RuleCategoryRefactorBoundary, "refactor-boundary"},
	}
	for _, tt := range cats {
		if string(tt.cat) != tt.want {
			t.Errorf("RuleCategory %v = %q, want %q", tt.cat, string(tt.cat), tt.want)
		}
	}
}

// --- RulePack.EffectivePhase1Preamble ---

func TestEffectivePhase1PreambleDefault(t *testing.T) {
	// EffectivePhase1Preamble returns the raw Phase1Preamble field.
	// Defaults are applied by LoadDefaultRulePacks/applyDefaults, not by the method itself.
	// An empty field returns empty.
	p := RulePack{Category: RuleCategoryTest}
	got := p.EffectivePhase1Preamble()
	if got != "" {
		t.Fatalf("empty Phase1Preamble should return empty, got %q", got)
	}
	// With a populated field it returns the value.
	p.Phase1Preamble = "my preamble"
	got = p.EffectivePhase1Preamble()
	if got != "my preamble" {
		t.Fatalf("got %q, want %q", got, "my preamble")
	}
}

func TestEffectivePhase1PreambleCustom(t *testing.T) {
	p := RulePack{
		Category:       RuleCategoryTest,
		Phase1Preamble: "custom preamble",
	}
	got := p.EffectivePhase1Preamble()
	if got != "custom preamble" {
		t.Fatalf("got %q, want %q", got, "custom preamble")
	}
}

func TestEffectivePhase1CategoryDescriptionDefault(t *testing.T) {
	p := RulePack{Category: RuleCategoryTest}
	got := p.EffectivePhase1CategoryDescription()
	if got == "" {
		t.Fatal("expected non-empty default description")
	}
}

func TestEffectivePhase1CategoryDescriptionCustom(t *testing.T) {
	p := RulePack{
		Category:                  RuleCategoryTest,
		Phase1CategoryDescription: "custom desc",
	}
	got := p.EffectivePhase1CategoryDescription()
	if got != "custom desc" {
		t.Fatalf("got %q, want %q", got, "custom desc")
	}
}

func TestEffectivePhase1ResponseSchemaDefault(t *testing.T) {
	// Like EffectivePhase1Preamble, defaults are applied by LoadDefaultRulePacks.
	// An empty field returns empty.
	p := RulePack{Category: RuleCategoryTest}
	got := p.EffectivePhase1ResponseSchema()
	if got != "" {
		t.Fatalf("empty Phase1ResponseSchema should return empty, got %q", got)
	}
	p.Phase1ResponseSchema = "my schema"
	got = p.EffectivePhase1ResponseSchema()
	if got != "my schema" {
		t.Fatalf("got %q, want %q", got, "my schema")
	}
}

func TestEffectivePhase1ResponseSchemaCustom(t *testing.T) {
	p := RulePack{
		Category:             RuleCategoryTest,
		Phase1ResponseSchema: "custom schema",
	}
	got := p.EffectivePhase1ResponseSchema()
	if got != "custom schema" {
		t.Fatalf("got %q, want %q", got, "custom schema")
	}
}

// --- parsePhase1Response ---

func TestParsePhase1ResponseValid(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryTest, Enabled: true},
	}
	raw := `{"summary":"test summary","mistakes":{"test":[{"summary":"an issue","confidence":0.8}]}}`
	mistakes, summary, warnings, err := parsePhase1Response(raw, packs)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if summary != "test summary" {
		t.Fatalf("summary = %q", summary)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if len(mistakes[RuleCategoryTest]) != 1 {
		t.Fatalf("mistakes len = %d", len(mistakes[RuleCategoryTest]))
	}
}

func TestParsePhase1ResponseEmpty(t *testing.T) {
	_, _, _, err := parsePhase1Response("", nil)
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestParsePhase1ResponseMalformed(t *testing.T) {
	_, _, _, err := parsePhase1Response(`{bad json`, nil)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParsePhase1ResponseWithCodeFence(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryTest, Enabled: true},
	}
	raw := "```json\n{\"summary\":\"s\",\"mistakes\":{\"test\":[{\"summary\":\"a\",\"confidence\":0.8}]}}\n```"
	_, summary, _, err := parsePhase1Response(raw, packs)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if summary != "s" {
		t.Fatalf("summary = %q", summary)
	}
}

func TestParsePhase1ResponseUnknownCategory(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryTest, Enabled: true},
	}
	raw := `{"summary":"s","mistakes":{"unknown-cat":[{"summary":"a","confidence":0.8}]}}`
	_, _, warnings, err := parsePhase1Response(raw, packs)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning for unknown category")
	}
}

// --- parsePhase2Response ---

func TestParsePhase2ResponseValid(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryTest, Enabled: true},
	}
	raw := `{"findings":{"test":[{"mistake":"an issue","confidence":0.8}]}}`
	findings, warnings, err := parsePhase2Response(raw, packs)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if len(findings[RuleCategoryTest]) != 1 {
		t.Fatalf("findings len = %d", len(findings[RuleCategoryTest]))
	}
}

func TestParsePhase2ResponseEmpty(t *testing.T) {
	findings, _, err := parsePhase2Response("", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected empty, got %d", len(findings))
	}
}

func TestParsePhase2ResponseMalformed(t *testing.T) {
	_, _, err := parsePhase2Response(`{bad`, nil)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// --- buildFinding ---

func TestBuildFinding(t *testing.T) {
	f := buildFinding(RuleCategoryTest, "an issue", 0.9,
		Guardrail{Tool: "go-vet"},
		[]CodebaseEvidence{{Path: "main.go"}},
	)
	if f.Category != RuleCategoryTest {
		t.Fatalf("category = %q", f.Category)
	}
	if f.Mistake != "an issue" {
		t.Fatalf("mistake = %q", f.Mistake)
	}
	if f.Confidence != 0.9 {
		t.Fatalf("confidence = %f", f.Confidence)
	}
	if f.Hash == "" {
		t.Fatal("expected non-empty hash")
	}
}

func TestBuildFindingTrimsMistake(t *testing.T) {
	// buildFinding does NOT trim — normalizeMistakes does the trimming
	// before calling buildFinding. This test verifies buildFinding
	// preserves whatever it receives.
	f := buildFinding(RuleCategoryTest, "  spaced  ", 0.8, Guardrail{}, nil)
	if f.Mistake != "  spaced  " {
		t.Fatalf("got %q", f.Mistake)
	}
}

// --- FormatTemplate ---

func TestFormatTemplateEmpty(t *testing.T) {
	if got := FormatTemplate("", nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestFormatTemplateNoPlaceholders(t *testing.T) {
	got := FormatTemplate("no placeholders", map[string]string{"key": "val"})
	if got != "no placeholders" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatTemplateReplacement(t *testing.T) {
	got := FormatTemplate("Hello {{name}}, welcome to {{place}}!", map[string]string{
		"name":  "World",
		"place": "Earth",
	})
	if got != "Hello World, welcome to Earth!" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatTemplateUnknownPlaceholder(t *testing.T) {
	got := FormatTemplate("Hello {{unknown}}", map[string]string{})
	if got != "Hello {{unknown}}" {
		t.Fatalf("got %q, want placeholder preserved", got)
	}
}

func TestFormatTemplateMultipleReplacements(t *testing.T) {
	got := FormatTemplate("{{x}} and {{x}}", map[string]string{"x": "A"})
	if got != "A and A" {
		t.Fatalf("got %q", got)
	}
}

// --- AllRuleCategories ---

func TestAllRuleCategories(t *testing.T) {
	cats := AllRuleCategories()
	if len(cats) != 6 {
		t.Fatalf("expected 6, got %d", len(cats))
	}
	seen := map[RuleCategory]bool{}
	for _, c := range cats {
		if seen[c] {
			t.Errorf("duplicate: %s", c)
		}
		seen[c] = true
	}
}

// --- RulePack.Effective* methods ---

func TestEffectivePhase2Preamble(t *testing.T) {
	p := RulePack{}
	if got := p.EffectivePhase2Preamble(); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	p.Phase2Preamble = "  hello  "
	if got := p.EffectivePhase2Preamble(); got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestEffectivePhase2ResponseSchema(t *testing.T) {
	p := RulePack{}
	if got := p.EffectivePhase2ResponseSchema(); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	p.Phase2ResponseSchema = "  schema  "
	if got := p.EffectivePhase2ResponseSchema(); got != "schema" {
		t.Fatalf("got %q, want %q", got, "schema")
	}
}

func TestEffectiveToolUseInstructions(t *testing.T) {
	p := RulePack{}
	if got := p.EffectiveToolUseInstructions(); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	p.ToolUseInstructions = "  instructions  "
	if got := p.EffectiveToolUseInstructions(); got != "instructions" {
		t.Fatalf("got %q", got)
	}
}

func TestEffectivePhase2RecordingInstructions(t *testing.T) {
	p := RulePack{}
	if got := p.EffectivePhase2RecordingInstructions(); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	p.Phase2RecordingInstructions = "  recording  "
	if got := p.EffectivePhase2RecordingInstructions(); got != "recording" {
		t.Fatalf("got %q", got)
	}
}

func TestRulePackTimeoutDefault(t *testing.T) {
	p := RulePack{}
	got := p.Timeout()
	if got <= 0 {
		t.Fatalf("expected positive default timeout, got %v", got)
	}
}

func TestRulePackTimeoutCustom(t *testing.T) {
	p := RulePack{TimeoutSeconds: 120}
	got := p.Timeout()
	if got != 120*time.Second {
		t.Fatalf("got %v, want %v", got, 120*time.Second)
	}
}

// --- NewOrchestrator ---

func TestNewOrchestratorWithPacks(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryTest, Enabled: true},
	}
	o := NewOrchestrator(packs)
	if len(o.Packs) != 1 {
		t.Fatalf("expected 1, got %d", len(o.Packs))
	}
}

func TestNewOrchestratorDefault(t *testing.T) {
	o := NewOrchestrator(nil)
	if len(o.Packs) == 0 {
		t.Fatal("expected default packs")
	}
}

func TestNewOrchestratorCopiesPacks(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryTest, Enabled: true},
	}
	o := NewOrchestrator(packs)
	packs[0].Enabled = false
	if !o.Packs[0].Enabled {
		t.Fatal("orchestrator should use a copy, not the original slice")
	}
}

// --- LoadDefaultRulePacks ---

func TestLoadDefaultRulePacks(t *testing.T) {
	packs, err := LoadDefaultRulePacks()
	if err != nil {
		t.Fatalf("LoadDefaultRulePacks: %v", err)
	}
	if len(packs) == 0 {
		t.Fatal("expected non-empty default packs")
	}
	// Each pack should have defaults applied.
	for _, p := range packs {
		if p.EffectivePhase1Preamble() == "" {
			t.Errorf("pack %s: empty Phase1Preamble after defaults", p.Category)
		}
	}
}

// --- toMistakes ---

func TestToMistakesDoesNotFilter(t *testing.T) {
	raws := []rawMistake{
		{Summary: "real", Confidence: 0.8},
		{Summary: "", Confidence: 0.9},
	}
	out := toMistakes(raws, RuleCategoryTest)
	// toMistakes does NOT filter empty summaries — normalizeMistakes does.
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
	if out[0].Summary != "real" {
		t.Fatalf("Summary[0] = %q", out[0].Summary)
	}
}

func TestToMistakesEmpty(t *testing.T) {
	out := toMistakes(nil, RuleCategoryTest)
	if len(out) != 0 {
		t.Fatalf("expected 0, got %d", len(out))
	}
}

// --- orderedByCategory ---

func TestOrderedByCategory(t *testing.T) {
	m := map[RuleCategory][]string{
		RuleCategoryTest:     {"a"},
		RuleCategoryLintRule: {"b"},
	}
	order := []RuleCategory{RuleCategoryLintRule, RuleCategoryTest}
	out := orderedByCategory(m, order)
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
	if out[0] != "b" || out[1] != "a" {
		t.Fatalf("got %v", out)
	}
}

func TestOrderedByCategoryEmpty(t *testing.T) {
	out := orderedByCategory(map[RuleCategory][]string{}, nil)
	if len(out) != 0 {
		t.Fatalf("expected 0, got %d", len(out))
	}
}

// --- Permission constants ---

func TestPermissionKindConstants(t *testing.T) {
	kinds := []struct {
		kind PermissionKind
		want string
	}{
		{PermissionKindRead, "read"},
		{PermissionKindURL, "url"},
		{PermissionKindShell, "shell"},
		{PermissionKindMCPTool, "mcp"},
		{PermissionKindCustomTool, "custom"},
	}
	for _, tt := range kinds {
		if string(tt.kind) != tt.want {
			t.Errorf("PermissionKind %v = %q, want %q", tt.kind, string(tt.kind), tt.want)
		}
	}
}

// --- ExecutionMode.String ---

func TestExecutionModeString(t *testing.T) {
	if got := ModeSequential.String(); got != "sequential" {
		t.Fatalf("got %q, want sequential", got)
	}
	if got := ModeParallel.String(); got != "parallel" {
		t.Fatalf("got %q, want parallel", got)
	}
}

// --- ProviderSupportsParallel ---

func TestProviderSupportsParallel(t *testing.T) {
	// Doesn't implement ParallelCapable
	if ProviderSupportsParallel(nil) {
		t.Fatal("nil should return false")
	}
}

// --- RunConfig ---

func TestRunConfigPhase1Factory(t *testing.T) {
	f := func() (Session, error) { return nil, nil }
	rc := RunConfig{Phase1SessionFactory: f}
	if rc.Phase1Factory() == nil {
		t.Fatal("expected non-nil Phase1Factory")
	}
}

func TestRunConfigPhase2FactoryFallback(t *testing.T) {
	f := func() (Session, error) { return nil, nil }
	// When Phase2SessionFactory is nil, falls back to Phase1
	rc := RunConfig{Phase1SessionFactory: f}
	if rc.Phase2Factory() == nil {
		t.Fatal("expected fallback Phase2Factory")
	}
}

func TestRunConfigPhase2FactoryOverride(t *testing.T) {
	f1 := func() (Session, error) { return nil, nil }
	f2 := func() (Session, error) { return nil, nil }
	rc := RunConfig{Phase1SessionFactory: f1, Phase2SessionFactory: f2}
	got := rc.Phase2Factory()
	if got == nil {
		t.Fatal("expected overridden Phase2Factory")
	}
	// Verify the override returns f2, not f1 (which would be the fallback).
	gotPtr := reflect.ValueOf(got).Pointer()
	f2Ptr := reflect.ValueOf(f2).Pointer()
	if gotPtr != f2Ptr {
		t.Fatal("Phase2Factory returned Phase1 fallback instead of Phase2 override")
	}
}

// --- NewLoggingSession ---

func TestLoggingSessionRun(t *testing.T) {
	lg, _ := logging.New(t.TempDir(), "info", 0)
	defer lg.Close()
	fake := &fakeSession{handler: func(prompt string) (string, error) { return "hello", nil }}
	sess := NewLoggingSession(fake, lg, "test-provider")
	result, err := sess.Run(context.Background(), "prompt", 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "hello" {
		t.Fatalf("result = %q, want hello", result)
	}
}

func TestLoggingSessionRunError(t *testing.T) {
	lg, _ := logging.New(t.TempDir(), "info", 0)
	defer lg.Close()
	fake := &fakeSession{handler: func(prompt string) (string, error) { return "", errors.New("boom") }}
	sess := NewLoggingSession(fake, lg, "test-provider")
	_, err := sess.Run(context.Background(), "prompt", 0)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoggingSessionClose(t *testing.T) {
	lg, _ := logging.New(t.TempDir(), "info", 0)
	defer lg.Close()
	fake := &fakeSession{}
	sess := NewLoggingSession(fake, lg, "test-provider")
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !fake.closed {
		t.Fatal("inner session should be closed")
	}
}

// MEDIUM #9 removed: TestPermissionDecisionApprovedDenied tested struct field
// access without exercising any production code. If PermissionDecision gains
// behavior (validation, serialization), add tests that exercise that behavior.
