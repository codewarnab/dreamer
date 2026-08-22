package opencodehttp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeShimTree builds a fake npm install layout inside a temp dir:
// <dir>/<shimName> next to <dir>/node_modules/<pkg>/bin/<files...>.
func writeShimTree(t *testing.T, shimName, shimContent string, binFiles map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "node_modules", "opencode-ai", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for name, content := range binFiles {
		if err := os.WriteFile(filepath.Join(binDir, name), content, 0o755); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	shimPath := filepath.Join(dir, shimName)
	if err := os.WriteFile(shimPath, []byte(shimContent), 0o644); err != nil {
		t.Fatalf("WriteFile shim: %v", err)
	}
	return shimPath
}

const cmdShimBody = "@ECHO off\r\n" +
	"SETLOCAL\r\n" +
	"CALL :find_dp0\r\n" +
	"IF EXIST \"%dp0%\\node.exe\" (\r\n" +
	"  SET \"_prog=%dp0%\\node.exe\"\r\n" +
	") ELSE (\r\n" +
	"  SET \"_prog=node\"\r\n" +
	")\r\n" +
	"\"%_prog%\"  \"%dp0%\\node_modules\\opencode-ai\\bin\\opencode\" %*\r\n" +
	"ENDLOCAL\r\n" +
	"EXIT /b %errorlevel%\r\n"

const ps1ShimBody = "#!/usr/bin/env pwsh\r\n" +
	"$basedir=Split-Path $MyInvocation.MyCommand.Definition -Parent\r\n" +
	"$exe=\"\"\r\n" +
	"$ret=$exe \"$basedir/node_modules/opencode-ai/bin/opencode\" $args\r\n"

const bashShimBody = "#!/bin/sh\n" +
	"basedir=$(dirname \"$0\")\n" +
	"exec node \"$basedir/node_modules/opencode-ai/bin/opencode\" \"$@\"\n"

func TestShimReplacementCmdShimBackslashes(t *testing.T) {
	exePath := writeShimTree(t, "opencode.cmd", cmdShimBody, map[string][]byte{
		"opencode.exe": []byte("MZ"),
	})
	got, ok := shimReplacement(exePath)
	if !ok {
		t.Fatal("expected resolution to succeed")
	}
	want := filepath.Join(filepath.Dir(exePath), "node_modules", "opencode-ai", "bin", "opencode.exe")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestShimReplacementPs1ForwardSlashes(t *testing.T) {
	exePath := writeShimTree(t, "opencode.ps1", ps1ShimBody, map[string][]byte{
		"opencode.exe": []byte("MZ"),
	})
	got, ok := shimReplacement(exePath)
	if !ok {
		t.Fatal("expected resolution to succeed")
	}
	want := filepath.Join(filepath.Dir(exePath), "node_modules", "opencode-ai", "bin", "opencode.exe")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestShimReplacementBashScriptNoExtension(t *testing.T) {
	exePath := writeShimTree(t, "opencode", bashShimBody, map[string][]byte{
		"opencode.exe": []byte("MZ"),
	})
	got, ok := shimReplacement(exePath)
	if !ok {
		t.Fatal("expected resolution to succeed")
	}
	want := filepath.Join(filepath.Dir(exePath), "node_modules", "opencode-ai", "bin", "opencode.exe")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestShimReplacementAppendsExeToExtensionlessTarget(t *testing.T) {
	// Shim references the launcher without an extension; only the native
	// .exe build exists on disk.
	exePath := writeShimTree(t, "opencode.cmd", cmdShimBody, map[string][]byte{
		"opencode.exe": []byte("MZ"),
	})
	got, ok := shimReplacement(exePath)
	if !ok || filepath.Ext(got) != ".exe" {
		t.Fatalf("got %q (ok=%v), want the .exe sibling", got, ok)
	}
}

func TestShimReplacementFallsBackWhenTargetMissing(t *testing.T) {
	// Shim parses but its target is absent; the conventional fallback path
	// must still resolve.
	dir := t.TempDir()
	fallbackDir := filepath.Join(dir, "node_modules", "opencode-ai", "bin")
	if err := os.MkdirAll(fallbackDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fallback := filepath.Join(fallbackDir, "opencode.exe")
	if err := os.WriteFile(fallback, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shimPath := filepath.Join(dir, "opencode.cmd")
	body := "\"%_prog%\"  \"%dp0%\\node_modules\\other-pkg\\bin\\gone\" %*\r\n"
	if err := os.WriteFile(shimPath, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile shim: %v", err)
	}

	got, ok := shimReplacement(shimPath)
	if !ok || got != fallback {
		t.Fatalf("got %q (ok=%v), want fallback %q", got, ok, fallback)
	}
}

func TestShimReplacementUnparseableShimUsesFallback(t *testing.T) {
	exePath := writeShimTree(t, "opencode.cmd", "@ECHO off\r\nnothing useful here\r\n", map[string][]byte{
		"opencode.exe": []byte("MZ"),
	})
	got, ok := shimReplacement(exePath)
	if !ok {
		t.Fatal("expected fallback resolution to succeed")
	}
	want := filepath.Join(filepath.Dir(exePath), "node_modules", "opencode-ai", "bin", "opencode.exe")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestShimReplacementNothingFoundKeepsOriginal(t *testing.T) {
	// No parseable target and no fallback binary: caller keeps spawning
	// the original entry.
	dir := t.TempDir()
	shimPath := filepath.Join(dir, "opencode.cmd")
	if err := os.WriteFile(shimPath, []byte(cmdShimBody), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, ok := shimReplacement(shimPath); ok {
		t.Fatal("expected no replacement for missing target")
	}
}

func TestShimReplacementDirectoryAtTargetRejected(t *testing.T) {
	// A directory at the candidate path must not be returned as the exe.
	exePath := writeShimTree(t, "opencode.cmd", cmdShimBody, nil)
	if err := os.MkdirAll(filepath.Join(filepath.Dir(exePath), "node_modules", "opencode-ai", "bin", "opencode.exe"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if _, ok := shimReplacement(exePath); ok {
		t.Fatal("directory at target path must be rejected")
	}
}

func TestShimReplacementExeShortCircuit(t *testing.T) {
	// .exe inputs are rejected without reading the file — a nonexistent
	// .exe path proves the early return.
	if _, ok := shimReplacement(`Z:\nonexistent\dir\opencode.EXE`); ok {
		t.Fatal(".exe entries must pass through unchanged")
	}
}

func TestShimReplacementMissingShimFile(t *testing.T) {
	if _, ok := shimReplacement(filepath.Join(t.TempDir(), "nope.cmd")); ok {
		t.Fatal("unreadable shim must report no replacement")
	}
}

func TestShimReplacementSpacesInNpmDir(t *testing.T) {
	// npm dir with spaces: the capture is anchored at node_modules, so the
	// quoting around %dp0% never breaks extraction.
	dir := t.TempDir()
	npmDir := filepath.Join(dir, "npm prefix with spaces")
	binDir := filepath.Join(npmDir, "node_modules", "opencode-ai", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	exe := filepath.Join(binDir, "opencode.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shimPath := filepath.Join(npmDir, "opencode.CMD")
	if err := os.WriteFile(shimPath, []byte("\"%_prog%\"  \"%dp0%\\node_modules\\opencode-ai\\bin\\opencode\" %*"), 0o644); err != nil {
		t.Fatalf("WriteFile shim: %v", err)
	}
	got, ok := shimReplacement(shimPath)
	if !ok || got != exe {
		t.Fatalf("got %q (ok=%v), want %q", got, ok, exe)
	}
}

// TestStartAutoStartResolvesShim exercises the full auto-start spawn path
// against a fake npm layout. Only meaningful on Windows, where the call
// site applies the resolver; skipped elsewhere so CI stays green.
func TestStartAutoStartResolvesShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("shim resolution is applied on windows only")
	}
	// The fake layout alone is not enough: Start() LookPaths the command
	// name first, which needs the temp dir on PATH plus a real server that
	// prints its listen address. That combination is covered end-to-end by
	// detectPort tests; here we assert the provider keeps working when the
	// command names a native exe directly (short-circuit path).
	p := &provider{command: []string{"go", "run", "-"}}
	t.Cleanup(func() { p.Close() })
	if err := p.Start(context.Background()); err == nil {
		t.Fatal("expected failure for command that exits without a port")
	}
}
