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

// Detect inspects a project root and returns a best-effort Toolchain.
func Detect(projectRoot string) Toolchain {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		return Toolchain{}
	}
	t := Toolchain{}
	if has(root, "go.mod") {
		t.Languages = appendUnique(t.Languages, "go")
		t.TestFrameworks = appendUnique(t.TestFrameworks, "go test")
		if cfg := findFirst(root, ".golangci.yml", ".golangci.yaml", ".golangci.toml"); cfg != "" {
			t.Linters = append(t.Linters, LinterConfig{Tool: "golangci-lint", ConfigPath: cfg})
			t.ConfigFiles = append(t.ConfigFiles, cfg)
		} else {
			t.Linters = append(t.Linters,
				LinterConfig{Tool: "go vet"},
				LinterConfig{Tool: "staticcheck"},
			)
		}
	}
	if has(root, "package.json") {
		t.Languages = appendUnique(t.Languages, "javascript")
		if has(root, "tsconfig.json") {
			t.Languages = appendUnique(t.Languages, "typescript")
			t.ConfigFiles = append(t.ConfigFiles, filepath.Join(root, "tsconfig.json"))
		}
		if cfg := findFirst(root,
			"eslint.config.js", "eslint.config.cjs", "eslint.config.mjs", "eslint.config.ts",
			".eslintrc", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc.json", ".eslintrc.yaml", ".eslintrc.yml"); cfg != "" {
			t.Linters = append(t.Linters, LinterConfig{Tool: "eslint", ConfigPath: cfg})
			t.ConfigFiles = append(t.ConfigFiles, cfg)
		}
		if cfg := findFirst(root, "biome.json", "biome.jsonc"); cfg != "" {
			t.Linters = append(t.Linters, LinterConfig{Tool: "biome", ConfigPath: cfg})
			t.ConfigFiles = append(t.ConfigFiles, cfg)
		}
		t.ConfigFiles = append(t.ConfigFiles, filepath.Join(root, "package.json"))
		t.TestFrameworks = appendUnique(t.TestFrameworks, deriveJSTestFramework(root))
	}
	if has(root, "pyproject.toml") || has(root, "ruff.toml") {
		t.Languages = appendUnique(t.Languages, "python")
		if cfg := findFirst(root, "ruff.toml", "pyproject.toml"); cfg != "" {
			t.Linters = append(t.Linters, LinterConfig{Tool: "ruff", ConfigPath: cfg})
			t.ConfigFiles = append(t.ConfigFiles, cfg)
		}
		t.TestFrameworks = appendUnique(t.TestFrameworks, "pytest")
	}
	if has(root, ".flake8") || has(root, "setup.cfg") {
		t.Languages = appendUnique(t.Languages, "python")
		if cfg := findFirst(root, ".flake8", "setup.cfg"); cfg != "" {
			t.Linters = append(t.Linters, LinterConfig{Tool: "flake8", ConfigPath: cfg})
			t.ConfigFiles = append(t.ConfigFiles, cfg)
		}
	}
	if has(root, "mypy.ini") {
		t.Linters = append(t.Linters, LinterConfig{Tool: "mypy", ConfigPath: filepath.Join(root, "mypy.ini")})
		t.ConfigFiles = append(t.ConfigFiles, filepath.Join(root, "mypy.ini"))
	}
	if has(root, "Cargo.toml") {
		t.Languages = appendUnique(t.Languages, "rust")
		t.Linters = append(t.Linters, LinterConfig{Tool: "clippy"})
		t.TestFrameworks = appendUnique(t.TestFrameworks, "cargo test")
		t.ConfigFiles = append(t.ConfigFiles, filepath.Join(root, "Cargo.toml"))
	}
	sort.Strings(t.Languages)
	sort.Strings(t.TestFrameworks)
	sort.Strings(t.ConfigFiles)
	return t
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
