package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dreamer/internal/analyzer"
)

func TestGenerateTodosCreatesFileWithGroupedMarkdown(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	runAt := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	findings := []analyzer.Finding{
		{
			Category:    analyzer.RuleCategoryPerformance,
			Description: "Query for dashboard blocks request handling",
		},
		{
			Category:    analyzer.RuleCategoryBugs,
			Description: "Nil pointer when processing empty chat payload",
		},
	}

	result, err := GenerateTodos("project-a", findings, GenerateOptions{
		Now: func() time.Time { return runAt },
	})
	if err != nil {
		t.Fatalf("GenerateTodos returned error: %v", err)
	}

	expectedPath := filepath.Join(home, defaultOutputDirName, "project-a", todosFileName)
	if got, want := result.Path, expectedPath; got != want {
		t.Fatalf("result.Path = %q, want %q", got, want)
	}
	if got, want := result.AddedFindings, 2; got != want {
		t.Fatalf("result.AddedFindings = %d, want %d", got, want)
	}

	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)

	assertContains(t, content, "## Run 2025-02-03T04:05:06Z")
	assertContains(t, content, "### Bugs")
	assertContains(t, content, "### Performance")
	assertContains(t, content, "- [ ] Nil pointer when processing empty chat payload")
	assertContains(t, content, "- [ ] Query for dashboard blocks request handling")
	assertContains(t, content, "<!-- dreamer:finding:")
}

func TestGenerateTodosDeduplicatesAgainstExistingEntries(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	firstRunAt := time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC)
	_, err := GenerateTodos("project-a", []analyzer.Finding{
		{
			Category:    analyzer.RuleCategoryBugs,
			Description: "Nil pointer when processing empty chat payload",
		},
	}, GenerateOptions{
		Now: func() time.Time { return firstRunAt },
	})
	if err != nil {
		t.Fatalf("GenerateTodos first run returned error: %v", err)
	}

	secondRunAt := time.Date(2025, 1, 2, 9, 0, 0, 0, time.UTC)
	result, err := GenerateTodos("project-a", []analyzer.Finding{
		{
			Category:    analyzer.RuleCategory(" BUGS "),
			Description: " nil pointer   when processing empty chat payload ",
		},
		{
			Category:    analyzer.RuleCategoryBugs,
			Description: "Panic when analyzer output is empty",
		},
	}, GenerateOptions{
		Now: func() time.Time { return secondRunAt },
	})
	if err != nil {
		t.Fatalf("GenerateTodos second run returned error: %v", err)
	}

	if got, want := result.AddedFindings, 1; got != want {
		t.Fatalf("result.AddedFindings = %d, want %d", got, want)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)

	duplicateHash := findingHash("bugs", "Nil pointer when processing empty chat payload")
	newHash := findingHash("bugs", "Panic when analyzer output is empty")

	if got, want := strings.Count(content, "dreamer:finding:"+duplicateHash), 1; got != want {
		t.Fatalf("duplicate finding marker count = %d, want %d", got, want)
	}
	if got, want := strings.Count(content, "dreamer:finding:"+newHash), 1; got != want {
		t.Fatalf("new finding marker count = %d, want %d", got, want)
	}
}

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func assertContains(t *testing.T, content string, expected string) {
	t.Helper()
	if !strings.Contains(content, expected) {
		t.Fatalf("content missing %q\ncontent:\n%s", expected, content)
	}
}
