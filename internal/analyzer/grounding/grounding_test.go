package grounding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectFiles_Basic(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "lib/util.go", "package lib\n")
	writeFile(t, root, "README.md", "# test\n")

	files, err := DetectFiles(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"README.md", "lib/util.go", "main.go"}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(files), len(want), files)
	}
	for i, w := range want {
		if files[i] != w {
			t.Errorf("files[%d] = %q, want %q", i, files[i], w)
		}
	}
}

func TestDetectFiles_SkipsNoiseDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "vendor/foo.go", "package vendor\n")
	writeFile(t, root, "node_modules/bar.js", "console.log()\n")
	writeFile(t, root, ".git/config", "[core]\n")
	writeFile(t, root, ".hidden/secret", "secret\n")

	files, err := DetectFiles(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "vendor/foo.go" || f == "node_modules/bar.js" || f == ".git/config" || f == ".hidden/secret" {
			t.Errorf("expected %q to be skipped", f)
		}
	}
	if len(files) != 1 || files[0] != "main.go" {
		t.Errorf("expected [main.go], got %v", files)
	}
}

func TestDetectFiles_Cap(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "")
	writeFile(t, root, "b.go", "")
	writeFile(t, root, "c.go", "")

	files, err := DetectFiles(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
}

func TestDetectFiles_EmptyRoot(t *testing.T) {
	files, err := DetectFiles("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if files != nil {
		t.Errorf("got %v, want nil", files)
	}
}

func TestBuildContext_Basic(t *testing.T) {
	files := []string{"main.go", "lib.go"}
	ctx := BuildContext(files, nil, 0, 0)
	if ctx == "" {
		t.Fatal("expected non-empty context")
	}
	if !strings.Contains(ctx, "Files (2 shown):") {
		t.Errorf("expected file count header, got: %s", ctx)
	}
	if !strings.Contains(ctx, "main.go") {
		t.Errorf("expected main.go in context")
	}
}

func TestBuildContext_WithSymbols(t *testing.T) {
	files := []string{"main.go"}
	symbols := []Symbol{
		{Path: "main.go", Line: 5, Kind: "func", Name: "Run"},
		{Path: "main.go", Line: 10, Kind: "type", Name: "Config"},
	}
	ctx := BuildContext(files, symbols, 0, 0)
	if !strings.Contains(ctx, "Exported symbols:") {
		t.Errorf("expected symbols header")
	}
	if !strings.Contains(ctx, "Run") {
		t.Errorf("expected Run in context")
	}
}

func TestBuildContext_FileCap(t *testing.T) {
	files := []string{"a.go", "b.go", "c.go"}
	ctx := BuildContext(files, nil, 2, 0)
	if !strings.Contains(ctx, "Files (2 shown):") {
		t.Errorf("expected capped count, got: %s", ctx)
	}
}

func TestFormatSymbolList_Basic(t *testing.T) {
	symbols := []Symbol{
		{Path: "main.go", Line: 5, Kind: "func", Name: "Run"},
		{Path: "main.go", Line: 10, Kind: "type", Name: "Config"},
	}
	got := FormatSymbolList(symbols, 0)
	want := "main.go:5  func Run\nmain.go:10  type Config"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatSymbolList_WithCap(t *testing.T) {
	symbols := []Symbol{
		{Path: "a.go", Line: 1, Kind: "func", Name: "A"},
		{Path: "b.go", Line: 2, Kind: "func", Name: "B"},
		{Path: "c.go", Line: 3, Kind: "func", Name: "C"},
	}
	got := FormatSymbolList(symbols, 2)
	if !strings.Contains(got, "A") || !strings.Contains(got, "B") {
		t.Errorf("expected first 2 symbols, got: %q", got)
	}
	if strings.Contains(got, "C") {
		t.Errorf("expected C to be capped, got: %q", got)
	}
}

func TestBuildSymbolIndex_GoFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", `package main

import "fmt"

// Run is the entry point.
func Run() {
	fmt.Println("hello")
}

// Config holds settings.
type Config struct {
	Name string
}

const MaxRetries = 3

var Version = "1.0"
`)
	writeFile(t, root, "main_test.go", `package main

func TestRun(t *testing.T) {}
`)

	files := []string{"main.go", "main_test.go"}
	symbols := BuildSymbolIndex(root, files, 0)

	// Should include exported symbols from main.go but not test file
	names := make(map[string]bool)
	for _, s := range symbols {
		names[s.Name] = true
		if s.Path == "main_test.go" {
			t.Error("should not include test files")
		}
	}
	for _, want := range []string{"Run", "Config", "MaxRetries", "Version"} {
		if !names[want] {
			t.Errorf("missing symbol %q", want)
		}
	}
}

func TestBuildSymbolIndex_NonGoFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "# test\n")
	symbols := BuildSymbolIndex(root, []string{"README.md"}, 0)
	if len(symbols) != 0 {
		t.Errorf("expected no symbols from non-Go files, got %d", len(symbols))
	}
}

func TestBuildSymbolIndex_EmptyRoot(t *testing.T) {
	symbols := BuildSymbolIndex("", []string{"main.go"}, 0)
	if symbols != nil {
		t.Errorf("expected nil, got %v", symbols)
	}
}

func TestBuildSymbolIndex_WithCap(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", `package a
func Alpha() {}
func Beta() {}
func Gamma() {}
`)
	symbols := BuildSymbolIndex(root, []string{"a.go"}, 2)
	if len(symbols) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(symbols))
	}
}

func TestBuildSymbolIndex_CapReturnsSorted(t *testing.T) {
	root := t.TempDir()
	// c.go comes before a.go in the file list, but sorts after it.
	writeFile(t, root, "c.go", `package c
func Charlie() {}
`)
	writeFile(t, root, "a.go", `package a
func Alpha() {}
`)

	// Pass files out of order; cap=2 should return both, sorted by (Path, Line).
	symbols := BuildSymbolIndex(root, []string{"c.go", "a.go"}, 2)
	if len(symbols) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(symbols))
	}
	if symbols[0].Path != "a.go" || symbols[0].Name != "Alpha" {
		t.Errorf("symbols[0] = {%s, %s}, want {a.go, Alpha}", symbols[0].Path, symbols[0].Name)
	}
	if symbols[1].Path != "c.go" || symbols[1].Name != "Charlie" {
		t.Errorf("symbols[1] = {%s, %s}, want {c.go, Charlie}", symbols[1].Path, symbols[1].Name)
	}
}

func TestDetectFiles_SkipsBinaryExtensions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "image.png", "\x89PNG\n")
	writeFile(t, root, "archive.zip", "PK\x03\x04\n")
	writeFile(t, root, "lib.dll", "\x00\n")

	files, err := DetectFiles(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "image.png" || f == "archive.zip" || f == "lib.dll" {
			t.Errorf("expected %q to be skipped", f)
		}
	}
	if len(files) != 1 || files[0] != "main.go" {
		t.Errorf("expected only main.go, got %v", files)
	}
}

// writeFile creates a file with the given content, including parent dirs.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFormatSymbolList_Empty(t *testing.T) {
	got := FormatSymbolList(nil, 0)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestBuildSymbolIndex_AllUnexported(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "internal.go", `package internal
func alpha() {}
func beta() {}
`)
	symbols := BuildSymbolIndex(root, []string{"internal.go"}, 0)
	if len(symbols) != 0 {
		t.Errorf("expected 0 symbols for unexported names, got %d", len(symbols))
	}
}
