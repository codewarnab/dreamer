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
