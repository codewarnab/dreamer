package output

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
	"dreamer/internal/fsutil"
)

const (
	todosFileName = "todos.md"

	versionMarker = "<!-- dreamer:version:1 -->"
)

var findingHashPattern = regexp.MustCompile(`dreamer:finding:([a-fA-F0-9]{64})`)

// GenerateOptions controls how a run section is rendered and where it goes.
type GenerateOptions struct {
	OutputRoot   string
	ProjectTitle string
	Warnings     []string
	Now          func() time.Time
}

// GenerateResult reports what GenerateTodos produced.
type GenerateResult struct {
	Path           string
	AddedFindings  int
	WroteWarnings  bool
	ExistingHashes map[string]struct{}
}

// GenerateTodos reads, merges, and writes the per-project todos.md file.
//
// The function is intentionally a thin I/O shell around MergeTodos. It resolves
// the target path, reads the current file if one exists, delegates all merging
// and rendering decisions to the pure function, then writes only when there is
// new content to persist.
func GenerateTodos(projectName string, findings []analyzer.Finding, opts GenerateOptions) (GenerateResult, error) {
	todosPath, err := todosPathForProject(projectName, opts.OutputRoot)
	if err != nil {
		return GenerateResult{}, err
	}

	existingContent, err := readExistingTodos(todosPath)
	if err != nil {
		return GenerateResult{}, err
	}

	merged, mergeResult := MergeTodos(projectName, existingContent, findings, opts)
	mergeResult.Path = todosPath
	if merged == "" {
		return mergeResult, nil
	}
	if err := os.MkdirAll(filepath.Dir(todosPath), fsutil.DirPerms); err != nil {
		return GenerateResult{}, fmt.Errorf("create todos directory %q: %w", filepath.Dir(todosPath), err)
	}
	if err := fsutil.WriteFileAtomic(todosPath, []byte(merged), fsutil.FilePerms); err != nil {
		return GenerateResult{}, fmt.Errorf("write todos file %q: %w", todosPath, err)
	}
	return mergeResult, nil
}

// MergeTodos returns the updated todos.md content for a run.
//
// Inputs are plain values so callers can test merge behavior without touching
// the filesystem. If there are no new findings and no warnings to render, the
// returned mergedContent is empty and result still reports the existing hashes
// observed in existingContent.
func MergeTodos(projectName, existingContent string, findings []analyzer.Finding, opts GenerateOptions) (mergedContent string, mergeResult GenerateResult) {
	existingHashes := extractExistingFindingHashes(existingContent)
	newFindings := filterNewFindings(findings, existingHashes)

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	runAt := now().UTC()

	var sections []string
	if len(newFindings) > 0 {
		sections = append(sections, renderRunSection(newFindings, runAt))
	}
	if len(opts.Warnings) > 0 {
		sections = append(sections, renderWarningsSection(opts.Warnings, runAt))
	}

	mergeResult = GenerateResult{
		AddedFindings:  len(newFindings),
		WroteWarnings:  len(opts.Warnings) > 0,
		ExistingHashes: existingHashes,
	}
	if len(sections) == 0 {
		return "", mergeResult
	}
	return mergeContent(existingContent, projectTitle(projectName, opts.ProjectTitle), sections), mergeResult
}

func todosPathForProject(projectName string, outputRoot string) (string, error) {
	if err := config.ValidateProjectName(projectName); err != nil {
		return "", err
	}

	name := strings.TrimSpace(projectName)
	root := strings.TrimSpace(outputRoot)
	if root == "" {
		configRoot, err := config.UserConfigRoot()
		if err != nil {
			return "", fmt.Errorf("resolve user config dir for output root: %w", err)
		}
		root = configRoot
	}
	return filepath.Join(root, name, todosFileName), nil
}

func readExistingTodos(path string) (string, error) {
	todoBytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read todos file %q: %w", path, err)
	}
	return string(todoBytes), nil
}

func extractExistingFindingHashes(content string) map[string]struct{} {
	existing := make(map[string]struct{})
	for _, match := range findingHashPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			existing[strings.ToLower(match[1])] = struct{}{}
		}
	}
	return existing
}

func filterNewFindings(findings []analyzer.Finding, existing map[string]struct{}) []analyzer.Finding {
	seen := make(map[string]struct{}, len(findings))
	out := make([]analyzer.Finding, 0, len(findings))
	for _, finding := range findings {
		if strings.TrimSpace(finding.Mistake) == "" {
			continue
		}
		hash := finding.Hash
		if hash == "" {
			hash = analyzer.ComputeFindingHash(finding)
		}
		hash = strings.ToLower(hash)
		if _, dup := existing[hash]; dup {
			continue
		}
		if _, dup := seen[hash]; dup {
			continue
		}
		seen[hash] = struct{}{}
		finding.Hash = hash
		out = append(out, finding)
	}
	return out
}

func renderRunSection(findings []analyzer.Finding, runAt time.Time) string {
	grouped := groupByCategory(findings)
	categories := sortedCategoryHeadings(grouped)

	var builder strings.Builder
	fmt.Fprintf(&builder, "## Run %s\n\n", runAt.Format(time.RFC3339))
	for i, heading := range categories {
		if i > 0 {
			builder.WriteByte('\n')
		}
		fmt.Fprintf(&builder, "### %s\n", heading)
		for _, finding := range grouped[heading] {
			builder.WriteString(renderFinding(finding))
		}
	}
	return builder.String()
}

func renderWarningsSection(warnings []string, runAt time.Time) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "## Warnings (Run %s)\n\n", runAt.Format(time.RFC3339))
	for _, warning := range warnings {
		text := strings.TrimSpace(warning)
		if text == "" {
			continue
		}
		builder.WriteString("- ")
		builder.WriteString(text)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func groupByCategory(findings []analyzer.Finding) map[string][]analyzer.Finding {
	grouped := make(map[string][]analyzer.Finding, len(findings))
	for _, finding := range findings {
		heading := categoryHeading(string(finding.Category))
		grouped[heading] = append(grouped[heading], finding)
	}
	return grouped
}

func sortedCategoryHeadings(grouped map[string][]analyzer.Finding) []string {
	headings := make([]string, 0, len(grouped))
	for k := range grouped {
		headings = append(headings, k)
	}
	sort.Strings(headings)
	return headings
}

func renderFinding(finding analyzer.Finding) string {
	var builder strings.Builder
	prefix := "- [ ] "
	if finding.Unverified {
		prefix = "- [ ] [unverified] "
	}
	builder.WriteString(prefix)
	builder.WriteString(renderMistakeLine(finding))
	builder.WriteByte('\n')
	if snippet := strings.TrimSpace(finding.Guardrail.ConfigSnippet); snippet != "" {
		builder.WriteString(renderSnippet(snippet, finding.Guardrail.Tool))
	}
	if len(finding.Evidence) > 0 {
		builder.WriteString("    Evidence:\n")
		for _, ev := range finding.Evidence {
			builder.WriteString("    - ")
			builder.WriteString(renderEvidenceLine(ev))
			builder.WriteByte('\n')
		}
	}
	fmt.Fprintf(&builder, "    <!-- dreamer:finding:%s -->\n", finding.Hash)
	if finding.Unverified {
		builder.WriteString("    <!-- dreamer:lintrule:unverified -->\n")
	}
	return builder.String()
}

func renderMistakeLine(finding analyzer.Finding) string {
	mistake := strings.TrimSpace(finding.Mistake)
	if finding.Guardrail.Tool == "" && finding.Guardrail.Rule == "" {
		return mistake
	}
	var builder strings.Builder
	builder.WriteString(mistake)
	if finding.Guardrail.Rule != "" || finding.Guardrail.Tool != "" {
		builder.WriteString(" — guardrail: ")
		if finding.Guardrail.Tool != "" {
			builder.WriteString(finding.Guardrail.Tool)
		}
		if finding.Guardrail.Rule != "" {
			if finding.Guardrail.Tool != "" {
				builder.WriteByte('/')
			}
			builder.WriteString(finding.Guardrail.Rule)
		}
		builder.WriteString(".")
	}
	return builder.String()
}

func renderSnippet(snippet string, tool string) string {
	language := snippetLanguage(tool)
	var builder strings.Builder
	builder.WriteString("    ```")
	if language != "" {
		builder.WriteString(language)
	}
	builder.WriteByte('\n')
	for _, line := range strings.Split(snippet, "\n") {
		if line != "" {
			builder.WriteString("    ")
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	builder.WriteString("    ```\n")
	return builder.String()
}

func snippetLanguage(tool string) string {
	normalizedTool := strings.ToLower(strings.TrimSpace(tool))
	switch normalizedTool {
	case "golangci-lint", "golangci":
		return "yaml"
	case "eslint", "biome":
		return "json"
	case "ruff", "flake8", "mypy":
		return "toml"
	case "github-actions", "gitlab-ci", "buildkite":
		return "yaml"
	}
	if strings.HasSuffix(normalizedTool, ".yaml") || strings.HasSuffix(normalizedTool, ".yml") {
		return "yaml"
	}
	if strings.HasSuffix(normalizedTool, ".json") {
		return "json"
	}
	if strings.HasSuffix(normalizedTool, ".toml") {
		return "toml"
	}
	return ""
}

func renderEvidenceLine(ev analyzer.CodebaseEvidence) string {
	var builder strings.Builder
	builder.WriteByte('`')
	builder.WriteString(ev.Path)
	if strings.TrimSpace(ev.Lines) != "" {
		builder.WriteByte(':')
		builder.WriteString(ev.Lines)
	}
	builder.WriteByte('`')
	if strings.TrimSpace(ev.Symbol) != "" {
		builder.WriteString(" (`")
		builder.WriteString(ev.Symbol)
		builder.WriteString("`)")
	}
	return builder.String()
}

func mergeContent(existing string, header string, sections []string) string {
	var builder strings.Builder
	if strings.TrimSpace(existing) == "" {
		builder.WriteString(header)
		builder.WriteString("\n\n")
		builder.WriteString(versionMarker)
		builder.WriteString("\n\n")
	} else {
		builder.WriteString(existing)
		if !strings.HasSuffix(existing, "\n") {
			builder.WriteByte('\n')
		}
		builder.WriteByte('\n')
	}
	for i, section := range sections {
		if i > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(section)
		if !strings.HasSuffix(section, "\n") {
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func projectTitle(projectName string, override string) string {
	if t := strings.TrimSpace(override); t != "" {
		return "# dreamer todos — " + t
	}
	return "# dreamer todos — " + projectName
}

func categoryHeading(category string) string {
	normalized := normalizeCategory(category)
	if normalized == "" {
		return "Uncategorized"
	}
	words := strings.Fields(normalized)
	for i := range words {
		if words[i] == "" {
			continue
		}
		words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
	}
	return strings.Join(words, " ")
}

func normalizeCategory(category string) string {
	replaced := strings.NewReplacer("_", " ", "-", " ").Replace(category)
	return normalizeText(replaced)
}

func normalizeText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}
