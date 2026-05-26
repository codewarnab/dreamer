package cmd

import (
	"path/filepath"
	"strings"
	"testing"
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
