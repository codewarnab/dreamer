package output

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"dreamer/internal/analyzer"
)

const (
	defaultOutputDirName = ".dreamer"
	todosFileName        = "todos.md"
	dirPerms             = 0o755
	filePerms            = 0o644
)

var findingHashPattern = regexp.MustCompile(`dreamer:finding:([a-fA-F0-9]{64})`)

type GenerateOptions struct {
	OutputRoot string
	Now        func() time.Time
}

type GenerateResult struct {
	Path          string
	AddedFindings int
}

type todoEntry struct {
	CategoryNormalized string
	CategoryHeading    string
	Description        string
	Hash               string
}

func GenerateTodos(projectName string, findings []analyzer.Finding, opts GenerateOptions) (GenerateResult, error) {
	todosPath, err := todosPathForProject(projectName, opts.OutputRoot)
	if err != nil {
		return GenerateResult{}, err
	}

	existingContent, err := readExistingTodos(todosPath)
	if err != nil {
		return GenerateResult{}, err
	}

	existingHashes := extractExistingFindingHashes(existingContent)
	newEntries := buildNewEntries(findings, existingHashes)
	result := GenerateResult{
		Path:          todosPath,
		AddedFindings: len(newEntries),
	}
	if len(newEntries) == 0 {
		return result, nil
	}

	grouped := groupEntriesByCategory(newEntries)
	renderedSection := renderRunSection(grouped, nowUTC(opts.Now))
	updatedContent := mergeExistingContent(existingContent, renderedSection)

	if err := os.MkdirAll(filepath.Dir(todosPath), dirPerms); err != nil {
		return GenerateResult{}, fmt.Errorf("create todos directory %q: %w", filepath.Dir(todosPath), err)
	}
	if err := os.WriteFile(todosPath, []byte(updatedContent), filePerms); err != nil {
		return GenerateResult{}, fmt.Errorf("write todos file %q: %w", todosPath, err)
	}

	return result, nil
}

func todosPathForProject(projectName string, outputRoot string) (string, error) {
	name := strings.TrimSpace(projectName)
	if name == "" {
		return "", fmt.Errorf("project name is required")
	}
	if strings.ContainsAny(name, `\/`) {
		return "", fmt.Errorf("project name contains invalid path separator: %q", projectName)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("project name is invalid: %q", projectName)
	}

	root := strings.TrimSpace(outputRoot)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for output root: %w", err)
		}
		root = filepath.Join(home, defaultOutputDirName)
	}

	return filepath.Join(root, name, todosFileName), nil
}

func readExistingTodos(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read todos file %q: %w", path, err)
	}
	return string(data), nil
}

func extractExistingFindingHashes(content string) map[string]struct{} {
	existing := make(map[string]struct{})
	currentCategory := "uncategorized"
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if heading, ok := strings.CutPrefix(trimmed, "### "); ok {
			currentCategory = normalizeCategory(heading)
			if currentCategory == "" {
				currentCategory = "uncategorized"
			}
		}

		matches := findingHashPattern.FindAllStringSubmatch(trimmed, -1)
		for _, match := range matches {
			if len(match) > 1 {
				existing[strings.ToLower(match[1])] = struct{}{}
			}
		}

		description, ok := strings.CutPrefix(trimmed, "- [ ] ")
		if !ok {
			continue
		}

		if idx := strings.Index(description, "<!--"); idx >= 0 {
			description = description[:idx]
		}
		description = strings.TrimSpace(description)
		if description == "" {
			continue
		}

		hash := findingHash(currentCategory, description)
		existing[hash] = struct{}{}
	}

	return existing
}

func buildNewEntries(findings []analyzer.Finding, existingHashes map[string]struct{}) []todoEntry {
	runHashes := make(map[string]struct{}, len(findings))
	entries := make([]todoEntry, 0, len(findings))

	for _, finding := range findings {
		description := strings.TrimSpace(finding.Description)
		if description == "" {
			continue
		}

		categoryNormalized := normalizeCategory(string(finding.Category))
		if categoryNormalized == "" {
			categoryNormalized = "uncategorized"
		}

		hash := findingHash(categoryNormalized, description)
		if _, exists := existingHashes[hash]; exists {
			continue
		}
		if _, exists := runHashes[hash]; exists {
			continue
		}
		runHashes[hash] = struct{}{}

		entries = append(entries, todoEntry{
			CategoryNormalized: categoryNormalized,
			CategoryHeading:    renderCategoryHeading(categoryNormalized),
			Description:        renderDescription(description),
			Hash:               hash,
		})
	}

	return entries
}

func groupEntriesByCategory(entries []todoEntry) map[string][]todoEntry {
	grouped := make(map[string][]todoEntry, len(entries))
	for _, entry := range entries {
		grouped[entry.CategoryHeading] = append(grouped[entry.CategoryHeading], entry)
	}
	return grouped
}

func renderRunSection(grouped map[string][]todoEntry, runAt time.Time) string {
	categories := make([]string, 0, len(grouped))
	for category := range grouped {
		categories = append(categories, category)
	}
	sort.Strings(categories)

	var builder strings.Builder
	builder.WriteString("## Run ")
	builder.WriteString(runAt.UTC().Format(time.RFC3339))
	builder.WriteString("\n\n")

	for i, category := range categories {
		if i > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("### ")
		builder.WriteString(category)
		builder.WriteString("\n")

		for _, entry := range grouped[category] {
			builder.WriteString(renderTodoItem(entry))
		}
	}

	return builder.String()
}

func renderTodoItem(entry todoEntry) string {
	return fmt.Sprintf("- [ ] %s <!-- dreamer:finding:%s -->\n", entry.Description, entry.Hash)
}

func mergeExistingContent(existing string, section string) string {
	if strings.TrimSpace(existing) == "" {
		return section
	}

	var builder strings.Builder
	builder.WriteString(existing)
	if !strings.HasSuffix(existing, "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	builder.WriteString(section)
	return builder.String()
}

func normalizeCategory(category string) string {
	replaced := strings.NewReplacer("_", " ", "-", " ").Replace(category)
	return normalizeText(replaced)
}

func normalizeDescription(description string) string {
	return normalizeText(description)
}

func normalizeText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func renderCategoryHeading(normalizedCategory string) string {
	if normalizedCategory == "" {
		return "Uncategorized"
	}

	words := strings.Fields(normalizedCategory)
	for i := range words {
		words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
	}
	return strings.Join(words, " ")
}

func renderDescription(description string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(description)), " ")
}

func findingHash(category string, description string) string {
	key := normalizeCategory(category) + "|" + normalizeDescription(description)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func nowUTC(nowFn func() time.Time) time.Time {
	if nowFn == nil {
		return time.Now().UTC()
	}
	return nowFn().UTC()
}
