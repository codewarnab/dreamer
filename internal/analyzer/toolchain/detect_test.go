package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectGoProject(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")

	tc := Detect(root)
	assertContains(t, tc.Languages, "go")
	assertContains(t, tc.TestFrameworks, "go test")
	assertLinterTool(t, tc.Linters, "go vet")
	assertLinterTool(t, tc.Linters, "staticcheck")
}

func TestDetectGoProjectWithGolangciLint(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	writeFile(t, filepath.Join(root, ".golangci.yml"), "linters:\n  enable:\n    - errcheck\n")

	tc := Detect(root)
	assertContains(t, tc.Languages, "go")
	assertLinterTool(t, tc.Linters, "golangci-lint")
	assertNotLinterTool(t, tc.Linters, "go vet")
}

func TestDetectJavaScriptProject(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"test"}`)

	tc := Detect(root)
	assertContains(t, tc.Languages, "javascript")
	assertContains(t, tc.TestFrameworks, "npm test") // default when no match
}

func TestDetectTypeScriptWithTsconfig(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"test"}`)
	writeFile(t, filepath.Join(root, "tsconfig.json"), `{}`)

	tc := Detect(root)
	assertContains(t, tc.Languages, "javascript")
	assertContains(t, tc.Languages, "typescript")
}

func TestDetectJavaScriptWithVitest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"vitest":"^1.0.0"}}`)

	tc := Detect(root)
	assertContains(t, tc.TestFrameworks, "vitest")
}

func TestDetectJavaScriptWithEslint(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"test"}`)
	writeFile(t, filepath.Join(root, "eslint.config.js"), `export default []`)

	tc := Detect(root)
	assertLinterTool(t, tc.Linters, "eslint")
}

func TestDetectPythonWithPyproject(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pyproject.toml"), "[project]\nname = 'test'\n")

	tc := Detect(root)
	assertContains(t, tc.Languages, "python")
	assertContains(t, tc.TestFrameworks, "pytest")
	assertLinterTool(t, tc.Linters, "ruff")
}

func TestDetectPythonWithFlake8(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".flake8"), "[flake8]\nmax-line-length = 120\n")

	tc := Detect(root)
	assertContains(t, tc.Languages, "python")
	assertLinterTool(t, tc.Linters, "flake8")
}

func TestDetectMypyStandalone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "mypy.ini"), "[mypy]\nstrict = true\n")

	tc := Detect(root)
	assertLinterTool(t, tc.Linters, "mypy")
	// mypy alone does not add a language
	assertNotContains(t, tc.Languages, "python")
}

func TestDetectRustProject(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'test'\n")

	tc := Detect(root)
	assertContains(t, tc.Languages, "rust")
	assertContains(t, tc.TestFrameworks, "cargo test")
	assertLinterTool(t, tc.Linters, "clippy")
}

func TestDetectEmptyRoot(t *testing.T) {
	tc := Detect("")
	if len(tc.Languages) != 0 || len(tc.Linters) != 0 || len(tc.TestFrameworks) != 0 {
		t.Fatalf("expected empty toolchain for empty root, got %+v", tc)
	}
}

func TestDetectNoSentinels(t *testing.T) {
	root := t.TempDir()
	tc := Detect(root)
	if len(tc.Languages) != 0 || len(tc.Linters) != 0 || len(tc.TestFrameworks) != 0 {
		t.Fatalf("expected empty toolchain for empty dir, got %+v", tc)
	}
}

func TestDetectMultipleEcosystems(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"test"}`)

	tc := Detect(root)
	assertContains(t, tc.Languages, "go")
	assertContains(t, tc.Languages, "javascript")
}

func TestDetectLanguagesSorted(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n")
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"test"}`)
	writeFile(t, filepath.Join(root, "tsconfig.json"), `{}`)

	tc := Detect(root)
	if tc.Languages[0] != "go" || tc.Languages[1] != "javascript" || tc.Languages[2] != "typescript" {
		t.Fatalf("expected sorted languages, got %v", tc.Languages)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func assertContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Fatalf("expected %v to contain %q", slice, want)
}

func assertNotContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			t.Fatalf("expected %v to NOT contain %q", slice, want)
		}
	}
}

func assertLinterTool(t *testing.T, linters []LinterConfig, want string) {
	t.Helper()
	for _, l := range linters {
		if l.Tool == want {
			return
		}
	}
	t.Fatalf("expected linters to contain tool %q, got %+v", want, linters)
}

func assertNotLinterTool(t *testing.T, linters []LinterConfig, want string) {
	t.Helper()
	for _, l := range linters {
		if l.Tool == want {
			t.Fatalf("expected linters to NOT contain tool %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// PrimaryLinter
// ---------------------------------------------------------------------------

func TestPrimaryLinterEmpty(t *testing.T) {
	tc := Toolchain{}
	if got := tc.PrimaryLinter(); got != "" {
		t.Errorf("PrimaryLinter on empty toolchain = %q, want empty", got)
	}
}

func TestPrimaryLinterReturnsFirst(t *testing.T) {
	tc := Toolchain{
		Linters: []LinterConfig{{Tool: "golangci-lint"}, {Tool: "staticcheck"}},
	}
	if got := tc.PrimaryLinter(); got != "golangci-lint" {
		t.Errorf("PrimaryLinter = %q, want golangci-lint", got)
	}
}

func TestPrimaryLinterSkipsBlankTool(t *testing.T) {
	tc := Toolchain{
		Linters: []LinterConfig{{Tool: "  "}, {Tool: "ruff"}},
	}
	if got := tc.PrimaryLinter(); got != "ruff" {
		t.Errorf("PrimaryLinter = %q, want ruff", got)
	}
}

func TestPrimaryLinterAllBlankTools(t *testing.T) {
	tc := Toolchain{
		Linters: []LinterConfig{{Tool: ""}, {Tool: "  "}},
	}
	if got := tc.PrimaryLinter(); got != "" {
		t.Errorf("PrimaryLinter = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// PrimaryTestFramework
// ---------------------------------------------------------------------------

func TestPrimaryTestFrameworkEmpty(t *testing.T) {
	tc := Toolchain{}
	if got := tc.PrimaryTestFramework(); got != "" {
		t.Errorf("PrimaryTestFramework on empty toolchain = %q, want empty", got)
	}
}

func TestPrimaryTestFrameworkReturnsFirst(t *testing.T) {
	tc := Toolchain{TestFrameworks: []string{"vitest", "jest"}}
	if got := tc.PrimaryTestFramework(); got != "vitest" {
		t.Errorf("PrimaryTestFramework = %q, want vitest", got)
	}
}

func TestPrimaryTestFrameworkSkipsBlank(t *testing.T) {
	tc := Toolchain{TestFrameworks: []string{"  ", "go test"}}
	if got := tc.PrimaryTestFramework(); got != "go test" {
		t.Errorf("PrimaryTestFramework = %q, want go test", got)
	}
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------

func TestSummaryEmptyToolchain(t *testing.T) {
	tc := Toolchain{}
	if got := tc.Summary(); got != "no toolchain detected" {
		t.Errorf("Summary = %q, want %q", got, "no toolchain detected")
	}
}

func TestSummaryAllFieldsPopulated(t *testing.T) {
	tc := Toolchain{
		Languages:      []string{"go"},
		Linters:        []LinterConfig{{Tool: "golangci-lint"}},
		TestFrameworks: []string{"go test"},
		ConfigFiles:    []string{"/root/.golangci.yml"},
	}
	got := tc.Summary()
	for _, want := range []string{"languages: go", "linters: golangci-lint", "tests: go test"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary = %q, want it to contain %q", got, want)
		}
	}
}

func TestSummaryLanguagesOnly(t *testing.T) {
	tc := Toolchain{Languages: []string{"go", "javascript"}}
	got := tc.Summary()
	if got != "languages: go, javascript" {
		t.Errorf("Summary = %q, want %q", got, "languages: go, javascript")
	}
}

func TestSummaryLintersOnly(t *testing.T) {
	tc := Toolchain{Linters: []LinterConfig{{Tool: "eslint"}, {Tool: "prettier"}}}
	got := tc.Summary()
	for _, want := range []string{"linters:", "eslint", "prettier"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary = %q, want it to contain %q", got, want)
		}
	}
}

func TestSummaryDeduplicatesLinterNames(t *testing.T) {
	tc := Toolchain{
		Linters: []LinterConfig{
			{Tool: "eslint"},
			{Tool: "eslint"},
			{Tool: "biome"},
		},
	}
	got := tc.Summary()
	if !strings.Contains(got, "eslint") || !strings.Contains(got, "biome") {
		t.Errorf("Summary = %q, expected deduplicated linter names", got)
	}
}

// ---------------------------------------------------------------------------
// String
// ---------------------------------------------------------------------------

func TestStringEmpty(t *testing.T) {
	tc := Toolchain{}
	got := tc.String()
	want := "languages=[] linters=0 tests=[] configs=0"
	if got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

func TestStringPopulated(t *testing.T) {
	tc := Toolchain{
		Languages:      []string{"go"},
		Linters:        []LinterConfig{{Tool: "golangci-lint"}, {Tool: "staticcheck"}},
		TestFrameworks: []string{"go test"},
		ConfigFiles:    []string{".golangci.yml"},
	}
	got := tc.String()
	if !strings.Contains(got, "languages=[go]") {
		t.Errorf("String = %q, want it to contain 'languages=[go]'", got)
	}
	if !strings.Contains(got, "linters=2") {
		t.Errorf("String = %q, want it to contain 'linters=2'", got)
	}
	if !strings.Contains(got, "configs=1") {
		t.Errorf("String = %q, want it to contain 'configs=1'", got)
	}
}

// ---------------------------------------------------------------------------
// deriveJSTestFramework
// ---------------------------------------------------------------------------

func TestDeriveJSTestFrameworkVitest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"vitest":"^1.0.0"}}`)
	if got := deriveJSTestFramework(root); got != "vitest" {
		t.Errorf("deriveJSTestFramework = %q, want vitest", got)
	}
}

func TestDeriveJSTestFrameworkJest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"jest":"^29.0.0"}}`)
	if got := deriveJSTestFramework(root); got != "jest" {
		t.Errorf("deriveJSTestFramework = %q, want jest", got)
	}
}

func TestDeriveJSTestFrameworkPlaywright(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"@playwright/test":"^1.0.0"}}`)
	if got := deriveJSTestFramework(root); got != "playwright" {
		t.Errorf("deriveJSTestFramework = %q, want playwright", got)
	}
}

func TestDeriveJSTestFrameworkMocha(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"mocha":"^10.0.0"}}`)
	if got := deriveJSTestFramework(root); got != "mocha" {
		t.Errorf("deriveJSTestFramework = %q, want mocha", got)
	}
}

func TestDeriveJSTestFrameworkDefaultNpmTest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"echo ok"}}`)
	if got := deriveJSTestFramework(root); got != "npm test" {
		t.Errorf("deriveJSTestFramework = %q, want npm test", got)
	}
}

func TestDeriveJSTestFrameworkMissingPackageJSON(t *testing.T) {
	root := t.TempDir()
	if got := deriveJSTestFramework(root); got != "npm test" {
		t.Errorf("deriveJSTestFramework = %q, want npm test (missing package.json)", got)
	}
}

func TestDeriveJSTestFrameworkPriorityVitestOverJest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"vitest":"^1","jest":"^29"}}`)
	if got := deriveJSTestFramework(root); got != "vitest" {
		t.Errorf("deriveJSTestFramework = %q, want vitest (priority over jest)", got)
	}
}

func TestDeriveJSTestFrameworkPriorityJestOverPlaywright(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"jest":"^29","playwright":"^1"}}`)
	if got := deriveJSTestFramework(root); got != "jest" {
		t.Errorf("deriveJSTestFramework = %q, want jest (priority over playwright)", got)
	}
}

// ---------------------------------------------------------------------------
// appendUnique
// ---------------------------------------------------------------------------

func TestAppendUnique(t *testing.T) {
	t.Run("adds new value", func(t *testing.T) {
		got := appendUnique([]string{"a"}, "b")
		if len(got) != 2 || got[1] != "b" {
			t.Errorf("got %v, want [a b]", got)
		}
	})

	t.Run("skips duplicate", func(t *testing.T) {
		got := appendUnique([]string{"a", "b"}, "a")
		if len(got) != 2 {
			t.Errorf("got %v, want [a b] (no duplicate)", got)
		}
	})

	t.Run("skips blank value", func(t *testing.T) {
		got := appendUnique([]string{"a"}, "  ")
		if len(got) != 1 {
			t.Errorf("got %v, want [a] (blank skipped)", got)
		}
	})

	t.Run("trims value before comparing", func(t *testing.T) {
		got := appendUnique([]string{"a"}, " a ")
		if len(got) != 1 {
			t.Errorf("got %v, want [a] (trimmed match)", got)
		}
	})

	t.Run("nil slice with valid value", func(t *testing.T) {
		got := appendUnique(nil, "x")
		if len(got) != 1 || got[0] != "x" {
			t.Errorf("got %v, want [x]", got)
		}
	})
}
