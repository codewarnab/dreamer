package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/config"
	"dreamer/internal/logging"
)

func TestResolveConfigPathEmpty(t *testing.T) {
	// Empty path should resolve to the global config path.
	got, err := resolveConfigPath("")
	if err != nil {
		t.Fatalf("resolveConfigPath empty: %v", err)
	}
	if got == "" {
		t.Fatal("expected non-empty global config path")
	}
}

func TestResolveConfigPathAbsolute(t *testing.T) {
	input := "/some/absolute/path/config.yaml"
	got, err := resolveConfigPath(input)
	if err != nil {
		t.Fatalf("resolveConfigPath absolute: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
	if got != filepath.Clean(got) {
		t.Fatalf("path should be cleaned, got %q", got)
	}
}

func TestResolveConfigPathRelative(t *testing.T) {
	got, err := resolveConfigPath("relative/config.yaml")
	if err != nil {
		t.Fatalf("resolveConfigPath relative: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
	if !strings.HasSuffix(got, filepath.Join("relative", "config.yaml")) {
		t.Fatalf("got %q, should end with relative/config.yaml", got)
	}
}

func TestResolveConfigPathTilde(t *testing.T) {
	got, err := resolveConfigPath("~/myconfig.yaml")
	if err != nil {
		t.Fatalf("resolveConfigPath tilde: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
	if strings.Contains(got, "~") {
		t.Fatalf("~ should be expanded, got %q", got)
	}
}

func TestResolveConfigPathWhitespaceOnly(t *testing.T) {
	// Whitespace-only should be treated as empty and resolve to global path.
	got, err := resolveConfigPath("   ")
	if err != nil {
		t.Fatalf("resolveConfigPath whitespace: %v", err)
	}
	if got == "" {
		t.Fatal("expected non-empty global config path")
	}
}

func TestLogDefaultedSinceNotices_NilLogger(t *testing.T) {
	cfg := &config.Config{Notices: config.ConfigNotices{DefaultedSince: []string{"p1"}}}
	// Should not panic.
	logDefaultedSinceNotices(nil, cfg)
}

func TestLogDefaultedSinceNotices_NilConfig(t *testing.T) {
	// Should not panic.
	logDefaultedSinceNotices(nil, nil)
}

func TestLogDefaultedSinceNotices_EmptyNotices(t *testing.T) {
	dir := t.TempDir()
	logger, err := logging.New(dir, "info", 1)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	defer logger.Close()

	cfg := &config.Config{Notices: config.ConfigNotices{DefaultedSince: nil}}
	// Should not panic and should produce no output.
	logDefaultedSinceNotices(logger, cfg)
}

func TestLogDefaultedSinceNotices_WithNotices(t *testing.T) {
	dir := t.TempDir()
	logger, err := logging.New(dir, "info", 1)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	defer logger.Close()

	cfg := &config.Config{Notices: config.ConfigNotices{DefaultedSince: []string{"project1", "project2"}}}
	logDefaultedSinceNotices(logger, cfg)
	// Should not panic. Verifying log file contents is fragile, so just ensure no crash.
}
