package output

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dreamer/internal/analyzer"
)

func TestGenerateTodosCreatesFileWithGroupedMarkdown(t *testing.T) {
	runAt := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	findings := []analyzer.Finding{
		{
			Category: analyzer.RuleCategoryLintRule,
			Mistake:  "Query for dashboard blocks request handling",
		},
		{
			Category: analyzer.RuleCategoryTest,
			Mistake:  "Nil pointer when processing empty chat payload",
		},
	}

	content, result := MergeTodos("project-a", "", findings, GenerateOptions{
		Now: func() time.Time { return runAt },
	})
	if got, want := result.AddedFindings, 2; got != want {
		t.Fatalf("result.AddedFindings = %d, want %d", got, want)
	}

	assertContains(t, content, "## Run 2025-02-03T04:05:06Z")
	assertContains(t, content, "### Lint Rule")
	assertContains(t, content, "### Test")
	assertContains(t, content, "- [ ] Nil pointer when processing empty chat payload")
	assertContains(t, content, "- [ ] Query for dashboard blocks request handling")
	assertContains(t, content, "<!-- dreamer:finding:")
}

func TestGenerateTodosIncludesRunIDInHeading(t *testing.T) {
	runAt := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	findings := []analyzer.Finding{
		{
			Category: analyzer.RuleCategoryTest,
			Mistake:  "Nil pointer when processing empty chat payload",
		},
	}

	content, result := MergeTodos("project-a", "", findings, GenerateOptions{
		Now:   func() time.Time { return runAt },
		RunID: "a1b2c3d4",
	})
	if got, want := result.AddedFindings, 1; got != want {
		t.Fatalf("result.AddedFindings = %d, want %d", got, want)
	}

	assertContains(t, content, "## Run 2025-02-03T04:05:06Z [a1b2c3d4]")
}

func TestGenerateTodosDeduplicatesAgainstExistingEntries(t *testing.T) {
	firstRunAt := time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC)
	existingContent, _ := MergeTodos("project-a", "", []analyzer.Finding{
		{
			Category: analyzer.RuleCategoryTest,
			Mistake:  "Nil pointer when processing empty chat payload",
		},
	}, GenerateOptions{
		Now: func() time.Time { return firstRunAt },
	})

	secondRunAt := time.Date(2025, 1, 2, 9, 0, 0, 0, time.UTC)
	newContent, result := MergeTodos("project-a", existingContent, []analyzer.Finding{
		{
			Category: analyzer.RuleCategoryTest,
			Mistake:  " nil pointer   when processing empty chat payload ",
		},
		{
			Category: analyzer.RuleCategoryTest,
			Mistake:  "Panic when analyzer output is empty",
		},
	}, GenerateOptions{
		Now: func() time.Time { return secondRunAt },
	})

	if got, want := result.AddedFindings, 1; got != want {
		t.Fatalf("result.AddedFindings = %d, want %d", got, want)
	}

	duplicateHash := analyzer.ComputeFindingHash(analyzer.Finding{
		Category: analyzer.RuleCategoryTest,
		Mistake:  "Nil pointer when processing empty chat payload",
	})
	newHash := analyzer.ComputeFindingHash(analyzer.Finding{
		Category: analyzer.RuleCategoryTest,
		Mistake:  "Panic when analyzer output is empty",
	})

	if got, want := strings.Count(newContent, "dreamer:finding:"+duplicateHash), 1; got != want {
		t.Fatalf("duplicate finding marker count = %d, want %d", got, want)
	}
	if got, want := strings.Count(newContent, "dreamer:finding:"+newHash), 1; got != want {
		t.Fatalf("new finding marker count = %d, want %d", got, want)
	}
}

func TestGenerateTodosWritesMergedContentToDisk(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	outputRoot := filepath.Join(home, ".config", "dreamer")

	result, err := GenerateTodos("project-a", []analyzer.Finding{
		{
			Category: analyzer.RuleCategoryTest,
			Mistake:  "Nil pointer when processing empty chat payload",
		},
	}, GenerateOptions{
		OutputRoot: outputRoot,
		Now:        func() time.Time { return time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("GenerateTodos returned error: %v", err)
	}

	todoBytes, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	assertContains(t, string(todoBytes), "Nil pointer when processing empty chat payload")
}

// B3: GenerateTodos must write atomically so a crash mid-write cannot leave a
// half-written todos.md that subsequent runs read back and dedupe against.
func TestGenerateTodosWritesAtomicallyLeavesNoTempFile(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	outputRoot := filepath.Join(home, ".config", "dreamer")

	// Pre-seed a stale .tmp from a hypothetical prior-crash run.
	staleDir := filepath.Join(outputRoot, "project-a")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	staleTmp := filepath.Join(staleDir, "todos.md.tmp")
	if err := os.WriteFile(staleTmp, []byte("garbage from crash"), 0o644); err != nil {
		t.Fatalf("seed stale tmp: %v", err)
	}

	if _, err := GenerateTodos("project-a", []analyzer.Finding{{
		Category: analyzer.RuleCategoryTest,
		Mistake:  "Nil pointer when processing empty chat payload",
	}}, GenerateOptions{
		OutputRoot: outputRoot,
		Now:        func() time.Time { return time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC) },
	}); err != nil {
		t.Fatalf("GenerateTodos returned error: %v", err)
	}
	if _, err := os.Stat(staleTmp); !os.IsNotExist(err) {
		t.Fatalf("stale .tmp must not survive an atomic write, stat err=%v", err)
	}
}

func TestRenderSnippetLeavesBlankLinesEmpty(t *testing.T) {
	snippet := renderSnippet("rules:\n\n  no-only-tests: true", "eslint")

	if strings.Contains(snippet, "\n    \n") {
		t.Fatalf("blank snippet line should not contain indentation: %q", snippet)
	}
	assertContains(t, snippet, "\n\n")
}

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

func assertContains(t *testing.T, content string, expected string) {
	t.Helper()
	if !strings.Contains(content, expected) {
		t.Fatalf("content missing %q\ncontent:\n%s", expected, content)
	}
}

func assertNotContains(t *testing.T, content string, unexpected string) {
	t.Helper()
	if strings.Contains(content, unexpected) {
		t.Fatalf("content unexpectedly contains %q\ncontent:\n%s", unexpected, content)
	}
}

// --- normalizeCategory ---

func TestNormalizeCategoryEmpty(t *testing.T) {
	if got := normalizeCategory(""); got != "" {
		t.Fatalf("normalizeCategory(\"\") = %q, want \"\"", got)
	}
}

func TestNormalizeCategoryUnderscores(t *testing.T) {
	if got := normalizeCategory("some_category"); got != "some category" {
		t.Fatalf("normalizeCategory(\"some_category\") = %q, want \"some category\"", got)
	}
}

func TestNormalizeCategoryHyphens(t *testing.T) {
	if got := normalizeCategory("some-category"); got != "some category" {
		t.Fatalf("normalizeCategory(\"some-category\") = %q, want \"some category\"", got)
	}
}

func TestNormalizeCategoryMixedSeparators(t *testing.T) {
	if got := normalizeCategory("some_mixed-category"); got != "some mixed category" {
		t.Fatalf("normalizeCategory(\"some_mixed-category\") = %q, want \"some mixed category\"", got)
	}
}

func TestNormalizeCategoryAlreadyLowercase(t *testing.T) {
	if got := normalizeCategory("lint_rule"); got != "lint rule" {
		t.Fatalf("normalizeCategory(\"lint_rule\") = %q, want \"lint rule\"", got)
	}
}

func TestNormalizeCategoryUpperCase(t *testing.T) {
	if got := normalizeCategory("LINT_RULE"); got != "lint rule" {
		t.Fatalf("normalizeCategory(\"LINT_RULE\") = %q, want \"lint rule\"", got)
	}
}

// --- normalizeText ---

func TestNormalizeTextEmpty(t *testing.T) {
	if got := normalizeText(""); got != "" {
		t.Fatalf("normalizeText(\"\") = %q, want \"\"", got)
	}
}

func TestNormalizeTextMultipleSpaces(t *testing.T) {
	if got := normalizeText("hello   world"); got != "hello world" {
		t.Fatalf("normalizeText(\"hello   world\") = %q, want \"hello world\"", got)
	}
}

func TestNormalizeTextTabs(t *testing.T) {
	if got := normalizeText("hello\t\tworld"); got != "hello world" {
		t.Fatalf("normalizeText(\"hello\\t\\tworld\") = %q, want \"hello world\"", got)
	}
}

func TestNormalizeTextLeadingTrailingWhitespace(t *testing.T) {
	if got := normalizeText("  hello world  "); got != "hello world" {
		t.Fatalf("normalizeText(\"  hello world  \") = %q, want \"hello world\"", got)
	}
}

func TestNormalizeTextMixedWhitespace(t *testing.T) {
	if got := normalizeText("  \t hello   world \t "); got != "hello world" {
		t.Fatalf("normalizeText with mixed whitespace = %q, want \"hello world\"", got)
	}
}

// --- snippetLanguage ---

func TestSnippetLanguageGolangciLint(t *testing.T) {
	if got := snippetLanguage("golangci-lint"); got != "yaml" {
		t.Fatalf("snippetLanguage(\"golangci-lint\") = %q, want \"yaml\"", got)
	}
}

func TestSnippetLanguageGolangci(t *testing.T) {
	if got := snippetLanguage("golangci"); got != "yaml" {
		t.Fatalf("snippetLanguage(\"golangci\") = %q, want \"yaml\"", got)
	}
}

func TestSnippetLanguageEslint(t *testing.T) {
	if got := snippetLanguage("eslint"); got != "json" {
		t.Fatalf("snippetLanguage(\"eslint\") = %q, want \"json\"", got)
	}
}

func TestSnippetLanguageBiome(t *testing.T) {
	if got := snippetLanguage("biome"); got != "json" {
		t.Fatalf("snippetLanguage(\"biome\") = %q, want \"json\"", got)
	}
}

func TestSnippetLanguageRuff(t *testing.T) {
	if got := snippetLanguage("ruff"); got != "toml" {
		t.Fatalf("snippetLanguage(\"ruff\") = %q, want \"toml\"", got)
	}
}

func TestSnippetLanguageFlake8(t *testing.T) {
	if got := snippetLanguage("flake8"); got != "toml" {
		t.Fatalf("snippetLanguage(\"flake8\") = %q, want \"toml\"", got)
	}
}

func TestSnippetLanguageMypy(t *testing.T) {
	if got := snippetLanguage("mypy"); got != "toml" {
		t.Fatalf("snippetLanguage(\"mypy\") = %q, want \"toml\"", got)
	}
}

func TestSnippetLanguageGitHubActions(t *testing.T) {
	if got := snippetLanguage("github-actions"); got != "yaml" {
		t.Fatalf("snippetLanguage(\"github-actions\") = %q, want \"yaml\"", got)
	}
}

func TestSnippetLanguageGitlabCI(t *testing.T) {
	if got := snippetLanguage("gitlab-ci"); got != "yaml" {
		t.Fatalf("snippetLanguage(\"gitlab-ci\") = %q, want \"yaml\"", got)
	}
}

func TestSnippetLanguageBuildkite(t *testing.T) {
	if got := snippetLanguage("buildkite"); got != "yaml" {
		t.Fatalf("snippetLanguage(\"buildkite\") = %q, want \"yaml\"", got)
	}
}

func TestSnippetLanguageUnknownTool(t *testing.T) {
	if got := snippetLanguage("some-unknown-tool"); got != "" {
		t.Fatalf("snippetLanguage(\"some-unknown-tool\") = %q, want \"\"", got)
	}
}

func TestSnippetLanguageEmpty(t *testing.T) {
	if got := snippetLanguage(""); got != "" {
		t.Fatalf("snippetLanguage(\"\") = %q, want \"\"", got)
	}
}

func TestSnippetLanguageYamlSuffix(t *testing.T) {
	if got := snippetLanguage("custom.yaml"); got != "yaml" {
		t.Fatalf("snippetLanguage(\"custom.yaml\") = %q, want \"yaml\"", got)
	}
}

func TestSnippetLanguageYmlSuffix(t *testing.T) {
	if got := snippetLanguage("custom.yml"); got != "yaml" {
		t.Fatalf("snippetLanguage(\"custom.yml\") = %q, want \"yaml\"", got)
	}
}

func TestSnippetLanguageJsonSuffix(t *testing.T) {
	if got := snippetLanguage("custom.json"); got != "json" {
		t.Fatalf("snippetLanguage(\"custom.json\") = %q, want \"json\"", got)
	}
}

func TestSnippetLanguageTomlSuffix(t *testing.T) {
	if got := snippetLanguage("custom.toml"); got != "toml" {
		t.Fatalf("snippetLanguage(\"custom.toml\") = %q, want \"toml\"", got)
	}
}

// --- renderMistakeLine ---

func TestRenderMistakeLineNoGuardrail(t *testing.T) {
	f := analyzer.Finding{Mistake: "something went wrong"}
	got := renderMistakeLine(f)
	if got != "something went wrong" {
		t.Fatalf("renderMistakeLine with no guardrail = %q, want \"something went wrong\"", got)
	}
}

func TestRenderMistakeLineToolOnly(t *testing.T) {
	f := analyzer.Finding{
		Mistake:   "bad config",
		Guardrail: analyzer.Guardrail{Tool: "eslint"},
	}
	got := renderMistakeLine(f)
	assertContains(t, got, "bad config")
	assertContains(t, got, "eslint")
}

func TestRenderMistakeLineRuleOnly(t *testing.T) {
	f := analyzer.Finding{
		Mistake:   "bad config",
		Guardrail: analyzer.Guardrail{Rule: "no-console"},
	}
	got := renderMistakeLine(f)
	assertContains(t, got, "bad config")
	assertContains(t, got, "no-console")
	assertNotContains(t, got, "/")
}

func TestRenderMistakeLineToolAndRule(t *testing.T) {
	f := analyzer.Finding{
		Mistake:   "bad config",
		Guardrail: analyzer.Guardrail{Tool: "eslint", Rule: "no-console"},
	}
	got := renderMistakeLine(f)
	assertContains(t, got, "bad config")
	assertContains(t, got, "eslint/no-console")
}

// --- renderEvidenceLine ---

func TestRenderEvidenceLinePathOnly(t *testing.T) {
	ev := analyzer.CodebaseEvidence{Path: "src/main.go"}
	got := renderEvidenceLine(ev)
	if got != "`src/main.go`" {
		t.Fatalf("renderEvidenceLine path only = %q, want \"`src/main.go`\"", got)
	}
}

func TestRenderEvidenceLinePathAndLines(t *testing.T) {
	ev := analyzer.CodebaseEvidence{Path: "src/main.go", Lines: "10-20"}
	got := renderEvidenceLine(ev)
	if got != "`src/main.go:10-20`" {
		t.Fatalf("renderEvidenceLine path+lines = %q, want \"`src/main.go:10-20`\"", got)
	}
}

func TestRenderEvidenceLinePathAndSymbol(t *testing.T) {
	ev := analyzer.CodebaseEvidence{Path: "src/main.go", Symbol: "main"}
	got := renderEvidenceLine(ev)
	if got != "`src/main.go` (`main`)" {
		t.Fatalf("renderEvidenceLine path+symbol = %q, want \"`src/main.go` (`main`)\"", got)
	}
}

func TestRenderEvidenceLineAllThree(t *testing.T) {
	ev := analyzer.CodebaseEvidence{Path: "src/main.go", Lines: "10-20", Symbol: "main"}
	got := renderEvidenceLine(ev)
	if got != "`src/main.go:10-20` (`main`)" {
		t.Fatalf("renderEvidenceLine all three = %q, want \"`src/main.go:10-20` (`main`)\"", got)
	}
}

// --- categoryHeading ---

func TestCategoryHeadingEmpty(t *testing.T) {
	if got := categoryHeading(""); got != "Uncategorized" {
		t.Fatalf("categoryHeading(\"\") = %q, want \"Uncategorized\"", got)
	}
}

func TestCategoryHeadingSingleWord(t *testing.T) {
	if got := categoryHeading("lint"); got != "Lint" {
		t.Fatalf("categoryHeading(\"lint\") = %q, want \"Lint\"", got)
	}
}

func TestCategoryHeadingMultiWord(t *testing.T) {
	if got := categoryHeading("lint_rule"); got != "Lint Rule" {
		t.Fatalf("categoryHeading(\"lint_rule\") = %q, want \"Lint Rule\"", got)
	}
}

func TestCategoryHeadingAlreadyCapitalized(t *testing.T) {
	// normalizeText lowercases everything, so heading re-capitalizes
	if got := categoryHeading("LINT"); got != "Lint" {
		t.Fatalf("categoryHeading(\"LINT\") = %q, want \"Lint\"", got)
	}
}

// --- projectTitle ---

func TestProjectTitleEmptyOverride(t *testing.T) {
	if got := projectTitle("my-project", ""); got != "# dreamer todos — my-project" {
		t.Fatalf("projectTitle(\"my-project\", \"\") = %q, want \"# dreamer todos — my-project\"", got)
	}
}

func TestProjectTitleWhitespaceOverride(t *testing.T) {
	if got := projectTitle("my-project", "   "); got != "# dreamer todos — my-project" {
		t.Fatalf("projectTitle with whitespace override = %q, want \"# dreamer todos — my-project\"", got)
	}
}

func TestProjectTitleNonEmptyOverride(t *testing.T) {
	if got := projectTitle("my-project", "Custom Title"); got != "# dreamer todos — Custom Title" {
		t.Fatalf("projectTitle(\"my-project\", \"Custom Title\") = %q, want \"# dreamer todos — Custom Title\"", got)
	}
}

// --- extractExistingFindingHashes ---

func TestExtractExistingFindingHashesNoHashes(t *testing.T) {
	hashes := extractExistingFindingHashes("just some plain text without any markers")
	if len(hashes) != 0 {
		t.Fatalf("extractExistingFindingHashes: got %d hashes, want 0", len(hashes))
	}
}

func TestExtractExistingFindingHashesSingle(t *testing.T) {
	hash := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	content := fmt.Sprintf("<!-- dreamer:finding:%s -->", hash)
	hashes := extractExistingFindingHashes(content)
	if _, ok := hashes[hash]; !ok {
		t.Fatalf("extractExistingFindingHashes: expected hash %q not found", hash)
	}
	if len(hashes) != 1 {
		t.Fatalf("extractExistingFindingHashes: got %d hashes, want 1", len(hashes))
	}
}

func TestExtractExistingFindingHashesMultiple(t *testing.T) {
	hash1 := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	hash2 := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	content := fmt.Sprintf("<!-- dreamer:finding:%s -->\n<!-- dreamer:finding:%s -->", hash1, hash2)
	hashes := extractExistingFindingHashes(content)
	if _, ok := hashes[hash1]; !ok {
		t.Fatalf("extractExistingFindingHashes: expected hash %q not found", hash1)
	}
	if _, ok := hashes[hash2]; !ok {
		t.Fatalf("extractExistingFindingHashes: expected hash %q not found", hash2)
	}
	if len(hashes) != 2 {
		t.Fatalf("extractExistingFindingHashes: got %d hashes, want 2", len(hashes))
	}
}

func TestExtractExistingFindingHashesCaseInsensitive(t *testing.T) {
	hash := "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
	content := fmt.Sprintf("<!-- dreamer:finding:%s -->", hash)
	hashes := extractExistingFindingHashes(content)
	lower := strings.ToLower(hash)
	if _, ok := hashes[lower]; !ok {
		t.Fatalf("extractExistingFindingHashes: expected lowered hash %q not found in %v", lower, hashes)
	}
}

// --- filterNewFindings ---

func TestFilterNewFindingsEmpty(t *testing.T) {
	out := filterNewFindings(nil, nil)
	if len(out) != 0 {
		t.Fatalf("filterNewFindings(nil, nil) returned %d findings, want 0", len(out))
	}
}

func TestFilterNewFindingsSkipsEmptyMistake(t *testing.T) {
	findings := []analyzer.Finding{
		{Category: analyzer.RuleCategoryTest, Mistake: ""},
		{Category: analyzer.RuleCategoryTest, Mistake: "  "},
	}
	out := filterNewFindings(findings, nil)
	if len(out) != 0 {
		t.Fatalf("filterNewFindings with empty mistakes returned %d, want 0", len(out))
	}
}

func TestFilterNewFindingsDeduplicatesWithinBatch(t *testing.T) {
	findings := []analyzer.Finding{
		{Category: analyzer.RuleCategoryTest, Mistake: "same mistake"},
		{Category: analyzer.RuleCategoryTest, Mistake: "same mistake"},
	}
	out := filterNewFindings(findings, nil)
	if len(out) != 1 {
		t.Fatalf("filterNewFindings batch dedup: got %d, want 1", len(out))
	}
}

func TestFilterNewFindingsSkipsExisting(t *testing.T) {
	f := analyzer.Finding{Category: analyzer.RuleCategoryTest, Mistake: "known mistake"}
	hash := strings.ToLower(analyzer.ComputeFindingHash(f))
	existing := map[string]struct{}{hash: {}}

	findings := []analyzer.Finding{f}
	out := filterNewFindings(findings, existing)
	if len(out) != 0 {
		t.Fatalf("filterNewFindings skip existing: got %d, want 0", len(out))
	}
}

func TestFilterNewFindingsKeepsNew(t *testing.T) {
	existing := map[string]struct{}{
		"0000000000000000000000000000000000000000000000000000000000000000": {},
	}
	findings := []analyzer.Finding{
		{Category: analyzer.RuleCategoryTest, Mistake: "new mistake"},
	}
	out := filterNewFindings(findings, existing)
	if len(out) != 1 {
		t.Fatalf("filterNewFindings keep new: got %d, want 1", len(out))
	}
}

func TestFilterNewFindingsNormalizesHash(t *testing.T) {
	f := analyzer.Finding{Category: analyzer.RuleCategoryTest, Mistake: "test"}
	f.Hash = strings.ToUpper(analyzer.ComputeFindingHash(f))
	out := filterNewFindings([]analyzer.Finding{f}, nil)
	if len(out) != 1 {
		t.Fatalf("filterNewFindings: got %d, want 1", len(out))
	}
	if out[0].Hash != strings.ToLower(f.Hash) {
		t.Fatalf("filterNewFindings: hash not lowercased, got %q", out[0].Hash)
	}
}

// --- groupByCategory ---

func TestGroupByCategoryEmpty(t *testing.T) {
	grouped := groupByCategory(nil)
	if len(grouped) != 0 {
		t.Fatalf("groupByCategory(nil) returned %d groups, want 0", len(grouped))
	}
}

func TestGroupByCategorySingleCategory(t *testing.T) {
	findings := []analyzer.Finding{
		{Category: analyzer.RuleCategoryTest, Mistake: "a"},
		{Category: analyzer.RuleCategoryTest, Mistake: "b"},
	}
	grouped := groupByCategory(findings)
	if len(grouped) != 1 {
		t.Fatalf("groupByCategory single: got %d groups, want 1", len(grouped))
	}
	if got := len(grouped["Test"]); got != 2 {
		t.Fatalf("groupByCategory single: Test group has %d items, want 2", got)
	}
}

func TestGroupByCategoryMultipleCategories(t *testing.T) {
	findings := []analyzer.Finding{
		{Category: analyzer.RuleCategoryTest, Mistake: "a"},
		{Category: analyzer.RuleCategoryLintRule, Mistake: "b"},
		{Category: analyzer.RuleCategoryTest, Mistake: "c"},
	}
	grouped := groupByCategory(findings)
	if len(grouped) != 2 {
		t.Fatalf("groupByCategory multi: got %d groups, want 2", len(grouped))
	}
	if got := len(grouped["Test"]); got != 2 {
		t.Fatalf("groupByCategory multi: Test group has %d items, want 2", got)
	}
	if got := len(grouped["Lint Rule"]); got != 1 {
		t.Fatalf("groupByCategory multi: Lint Rule group has %d items, want 1", got)
	}
}

// --- sortedCategoryHeadings ---

func TestSortedCategoryHeadingsEmpty(t *testing.T) {
	headings := sortedCategoryHeadings(map[string][]analyzer.Finding{})
	if len(headings) != 0 {
		t.Fatalf("sortedCategoryHeadings empty: got %d, want 0", len(headings))
	}
}

func TestSortedCategoryHeadingsSingle(t *testing.T) {
	grouped := map[string][]analyzer.Finding{"Test": {}}
	headings := sortedCategoryHeadings(grouped)
	if len(headings) != 1 || headings[0] != "Test" {
		t.Fatalf("sortedCategoryHeadings single: got %v, want [Test]", headings)
	}
}

func TestSortedCategoryHeadingsMultiple(t *testing.T) {
	grouped := map[string][]analyzer.Finding{
		"Zebra":  {},
		"Alpha":  {},
		"Middle": {},
	}
	headings := sortedCategoryHeadings(grouped)
	want := []string{"Alpha", "Middle", "Zebra"}
	if len(headings) != len(want) {
		t.Fatalf("sortedCategoryHeadings: got %v, want %v", headings, want)
	}
	for i, h := range want {
		if headings[i] != h {
			t.Fatalf("sortedCategoryHeadings[%d] = %q, want %q", i, headings[i], h)
		}
	}
}

// --- renderWarningsSection ---

func TestRenderWarningsSectionEmpty(t *testing.T) {
	runAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got := renderWarningsSection([]string{}, runAt, "")
	assertContains(t, got, "## Warnings")
	// Should have the header but no bullet items
	if strings.Count(got, "- ") != 0 {
		t.Fatalf("renderWarningsSection empty: expected no bullet items, got:\n%s", got)
	}
}

func TestRenderWarningsSectionWithRunID(t *testing.T) {
	runAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got := renderWarningsSection([]string{"some warning"}, runAt, "abc123")
	assertContains(t, got, "[abc123]")
	assertContains(t, got, "- some warning")
}

func TestRenderWarningsSectionSkipsWhitespaceOnly(t *testing.T) {
	runAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got := renderWarningsSection([]string{"   ", "\t", "  "}, runAt, "")
	if strings.Count(got, "- ") != 0 {
		t.Fatalf("renderWarningsSection whitespace-only: expected no bullet items, got:\n%s", got)
	}
}

func TestRenderWarningsSectionMixedContent(t *testing.T) {
	runAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got := renderWarningsSection([]string{"valid warning", "   ", "another warning"}, runAt, "")
	assertContains(t, got, "- valid warning")
	assertContains(t, got, "- another warning")
	if strings.Count(got, "- ") != 2 {
		t.Fatalf("renderWarningsSection mixed: expected 2 bullet items, got:\n%s", got)
	}
}
