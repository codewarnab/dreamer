package webcheck

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testDataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "testdata")
}

func TestCheckHTMLFindsXHTML(t *testing.T) {
	dir := filepath.Join(testDataDir(), "templates")
	findings, err := Check(Config{TemplatesDir: dir})
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	var xhtmlFindings []Finding
	for _, f := range findings {
		if f.Check == "x-html" {
			xhtmlFindings = append(xhtmlFindings, f)
		}
	}

	if len(xhtmlFindings) != 1 {
		t.Fatalf("expected 1 x-html finding, got %d: %v", len(xhtmlFindings), xhtmlFindings)
	}
	if xhtmlFindings[0].Line != 2 {
		t.Errorf("expected x-html on line 2, got line %d", xhtmlFindings[0].Line)
	}
}

func TestCheckHTMLGoodFile(t *testing.T) {
	dir := filepath.Join(testDataDir(), "templates")
	findings, err := Check(Config{TemplatesDir: dir})
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	for _, f := range findings {
		if filepath.Base(f.File) == "good.html" {
			t.Errorf("unexpected finding in good.html: %s", f)
		}
	}
}

func TestCheckCSSHardcodedColors(t *testing.T) {
	dir := filepath.Join(testDataDir(), "css")
	findings, err := Check(Config{CSSDir: dir})
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	var colorFindings []Finding
	for _, f := range findings {
		if f.Check == "css-hardcoded-color" {
			colorFindings = append(colorFindings, f)
		}
	}

	// bad_tokens.css has 2 hardcoded colors: #ef476f and #ffd166.
	// #ffffff is skipped (whitelist), #131313 and #3cffd0 are in :root (token defs).
	if len(colorFindings) != 2 {
		t.Fatalf("expected 2 hardcoded color findings, got %d: %v", len(colorFindings), colorFindings)
	}
}

func TestCheckCSSUndefinedTokens(t *testing.T) {
	tmpDir := t.TempDir()
	cssContent := `:root { --defined: #fff; }
.box { color: var(--defined); background: var(--undefined); }
`
	if err := os.WriteFile(filepath.Join(tmpDir, "test.css"), []byte(cssContent), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, err := Check(Config{CSSDir: tmpDir})
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	var tokenFindings []Finding
	for _, f := range findings {
		if f.Check == "css-undefined-token" {
			tokenFindings = append(tokenFindings, f)
		}
	}

	if len(tokenFindings) != 1 {
		t.Fatalf("expected 1 undefined token finding, got %d: %v", len(tokenFindings), tokenFindings)
	}
}

func TestCheckCSSGoodFile(t *testing.T) {
	dir := filepath.Join(testDataDir(), "css")
	findings, err := Check(Config{CSSDir: dir})
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	for _, f := range findings {
		if filepath.Base(f.File) == "good.css" {
			t.Errorf("unexpected finding in good.css: %s", f)
		}
	}
}

func TestCheckAlpineInitFindsBadPattern(t *testing.T) {
	dir := filepath.Join(testDataDir(), "js")
	findings, err := Check(Config{JSDir: dir})
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	var alpineFindings []Finding
	for _, f := range findings {
		if f.Check == "alpine-init-listener" {
			alpineFindings = append(alpineFindings, f)
		}
	}

	if len(alpineFindings) != 1 {
		t.Fatalf("expected 1 alpine-init-listener finding, got %d: %v", len(alpineFindings), alpineFindings)
	}
	if alpineFindings[0].Line != 2 {
		t.Errorf("expected alpine:init on line 2, got line %d", alpineFindings[0].Line)
	}
}

func TestCheckAlpineInitGoodFile(t *testing.T) {
	dir := filepath.Join(testDataDir(), "js")
	findings, err := Check(Config{JSDir: dir})
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	for _, f := range findings {
		if filepath.Base(f.File) == "good_stores.js" {
			t.Errorf("unexpected finding in good_stores.js: %s", f)
		}
	}
}

func TestCheckEmptyConfig(t *testing.T) {
	findings, err := Check(Config{})
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for empty config, got %d", len(findings))
	}
}

func TestCheckNonexistentDir(t *testing.T) {
	findings, err := Check(Config{TemplatesDir: "/nonexistent/path"})
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings, got %d", len(findings))
	}
}

// TestGlobDirRecursive verifies that globDir descends into subdirectories.
// This guards against regressions where the linter silently scans zero files
// because the production template tree uses nested directories.
func TestGlobDirRecursive(t *testing.T) {
	dir := t.TempDir()

	// Create a nested structure mirroring the real template layout.
	for _, rel := range []string{
		"pages/foo.html",
		"partials/bar/baz.html",
		"layouts/base.html",
	} {
		fullPath := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(`<p>ok</p>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := globDir(dir, ".html")
	if err != nil {
		t.Fatalf("globDir error: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files from nested dirs, got %d: %v", len(files), files)
	}
}

// TestCheckRealTemplates runs webcheck against the actual web template tree
// to ensure the linter can find files and that known x-html usages in the
// production templates are detected (they are safe today, but the linter
// must be able to see them).
func TestCheckRealTemplates(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot determine source file path")
	}
	// Walk up from internal/webcheck/ to the repo root, then into internal/web/templates.
	repoRoot := filepath.Join(filepath.Dir(filename), "..", "..")
	templatesDir := filepath.Join(repoRoot, "internal", "web", "templates")

	if _, err := os.Stat(templatesDir); os.IsNotExist(err) {
		t.Skipf("real templates dir not found at %s", templatesDir)
	}

	findings, err := Check(Config{TemplatesDir: templatesDir})
	if err != nil {
		t.Fatalf("Check() against real templates: %v", err)
	}

	// Count x-html findings. We know at least two exist in the real tree
	// (logs/viewer.html and pages/projects/findings.html). If this count is
	// zero the linter has regressed to scanning nothing.
	xhtmlCount := 0
	for _, f := range findings {
		if f.Check == "x-html" {
			xhtmlCount++
		}
	}
	if xhtmlCount == 0 {
		t.Error("webcheck found zero x-html usages in the real template tree — linter is likely not recursing into subdirectories")
	}
}
