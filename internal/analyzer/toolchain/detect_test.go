package toolchain

import (
	"os"
	"path/filepath"
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
