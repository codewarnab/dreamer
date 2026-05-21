package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Toolchain captures the languages, linters, and test frameworks detected
// in a project root.
type Toolchain struct {
	Languages      []string
	Linters        []LinterConfig
	TestFrameworks []string
	ConfigFiles    []string
}

// LinterConfig describes one detected linter.
type LinterConfig struct {
	Tool         string
	ConfigPath   string
	EnabledRules []string
}

// linterCandidate describes how to detect one linter for a detection rule.
type linterCandidate struct {
	configFiles []string // sentinel config files to search for (OR-combined)
	tool        string   // linter tool name when config is found
	fallbacks   []string // linters to register when no config is found
}

// detectionRule defines one ecosystem to detect in a project root.
type detectionRule struct {
	sentinels      []string          // trigger files (OR-combined)
	languages      []string          // languages to register
	testFrameworks []string          // literal test frameworks (empty if callback used)
	linters        []linterCandidate // linters to probe
	configFiles    []string          // always-recorded config paths (relative to root)
	detectTestFW   func(root string) string // override for test framework detection
}

var detectionRules = []detectionRule{
	{
		sentinels:      []string{"go.mod"},
		languages:      []string{"go"},
		testFrameworks: []string{"go test"},
		linters: []linterCandidate{
			{
				configFiles: []string{".golangci.yml", ".golangci.yaml", ".golangci.toml"},
				tool:        "golangci-lint",
				fallbacks:   []string{"go vet", "staticcheck"},
			},
		},
	},
	{
		sentinels:   []string{"package.json"},
		languages:   []string{"javascript"},
		configFiles: []string{"package.json"},
		linters: []linterCandidate{
			{
				configFiles: []string{
					"eslint.config.js", "eslint.config.cjs", "eslint.config.mjs", "eslint.config.ts",
					".eslintrc", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc.json", ".eslintrc.yaml", ".eslintrc.yml",
				},
				tool: "eslint",
			},
			{
				configFiles: []string{"biome.json", "biome.jsonc"},
				tool:        "biome",
			},
		},
		detectTestFW: deriveJSTestFramework,
	},
	{
		sentinels:      []string{"pyproject.toml", "ruff.toml"},
		languages:      []string{"python"},
		testFrameworks: []string{"pytest"},
		linters: []linterCandidate{
			{
				configFiles: []string{"ruff.toml", "pyproject.toml"},
				tool:        "ruff",
			},
		},
	},
	{
		sentinels: []string{".flake8", "setup.cfg"},
		languages: []string{"python"},
		linters: []linterCandidate{
			{
				configFiles: []string{".flake8", "setup.cfg"},
				tool:        "flake8",
			},
		},
	},
	{
		sentinels: []string{"mypy.ini"},
		linters: []linterCandidate{
			{
				configFiles: []string{"mypy.ini"},
				tool:        "mypy",
			},
		},
	},
	{
		sentinels:      []string{"Cargo.toml"},
		languages:      []string{"rust"},
		testFrameworks: []string{"cargo test"},
		linters: []linterCandidate{
			{tool: "clippy"},
		},
		configFiles: []string{"Cargo.toml"},
	},
}

// Detect inspects a project root and returns a best-effort Toolchain.
func Detect(projectRoot string) Toolchain {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		return Toolchain{}
	}
	var t Toolchain
	for _, rule := range detectionRules {
		if !anyPresent(root, rule.sentinels) {
			continue
		}
		for _, lang := range rule.languages {
			t.Languages = appendUnique(t.Languages, lang)
		}
		for _, fw := range rule.testFrameworks {
			t.TestFrameworks = appendUnique(t.TestFrameworks, fw)
		}
		if rule.detectTestFW != nil {
			t.TestFrameworks = appendUnique(t.TestFrameworks, rule.detectTestFW(root))
		}
		for _, l := range rule.linters {
			if len(l.configFiles) == 0 {
				t.Linters = append(t.Linters, LinterConfig{Tool: l.tool})
			} else if cfg := findFirst(root, l.configFiles...); cfg != "" {
				t.Linters = append(t.Linters, LinterConfig{Tool: l.tool, ConfigPath: cfg})
				t.ConfigFiles = append(t.ConfigFiles, cfg)
			} else {
				for _, fb := range l.fallbacks {
					t.Linters = append(t.Linters, LinterConfig{Tool: fb})
				}
			}
		}
		for _, cf := range rule.configFiles {
			t.ConfigFiles = append(t.ConfigFiles, filepath.Join(root, cf))
		}
	}
	// JS conditional: typescript language when tsconfig.json present.
	if has(root, "package.json") && has(root, "tsconfig.json") {
		t.Languages = appendUnique(t.Languages, "typescript")
		t.ConfigFiles = append(t.ConfigFiles, filepath.Join(root, "tsconfig.json"))
	}
	sort.Strings(t.Languages)
	sort.Strings(t.TestFrameworks)
	sort.Strings(t.ConfigFiles)
	return t
}

func anyPresent(root string, names []string) bool {
	for _, n := range names {
		if has(root, n) {
			return true
		}
	}
	return false
}

// PrimaryLinter returns the first detected linter tool, or empty.
func (t Toolchain) PrimaryLinter() string {
	for _, l := range t.Linters {
		if strings.TrimSpace(l.Tool) != "" {
			return l.Tool
		}
	}
	return ""
}

// PrimaryTestFramework returns the first detected test framework, or empty.
func (t Toolchain) PrimaryTestFramework() string {
	for _, f := range t.TestFrameworks {
		if strings.TrimSpace(f) != "" {
			return f
		}
	}
	return ""
}

// Summary returns a one-line description suitable for prompt injection.
func (t Toolchain) Summary() string {
	var parts []string
	if len(t.Languages) > 0 {
		parts = append(parts, "languages: "+strings.Join(t.Languages, ", "))
	}
	if len(t.Linters) > 0 {
		names := make([]string, 0, len(t.Linters))
		for _, l := range t.Linters {
			names = appendUnique(names, l.Tool)
		}
		parts = append(parts, "linters: "+strings.Join(names, ", "))
	}
	if len(t.TestFrameworks) > 0 {
		parts = append(parts, "tests: "+strings.Join(t.TestFrameworks, ", "))
	}
	if len(parts) == 0 {
		return "no toolchain detected"
	}
	return strings.Join(parts, "; ")
}

func has(root string, name string) bool {
	path := filepath.Join(root, name)
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func findFirst(root string, names ...string) string {
	for _, n := range names {
		path := filepath.Join(root, n)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func appendUnique(slice []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return slice
	}
	for _, existing := range slice {
		if existing == value {
			return slice
		}
	}
	return append(slice, value)
}

func deriveJSTestFramework(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return "npm test"
	}
	content := string(data)
	switch {
	case strings.Contains(content, "vitest"):
		return "vitest"
	case strings.Contains(content, "jest"):
		return "jest"
	case strings.Contains(content, "playwright"):
		return "playwright"
	case strings.Contains(content, "mocha"):
		return "mocha"
	}
	return "npm test"
}

// String formats a Toolchain for logging.
func (t Toolchain) String() string {
	return fmt.Sprintf("languages=%v linters=%d tests=%v configs=%d",
		t.Languages, len(t.Linters), t.TestFrameworks, len(t.ConfigFiles))
}
