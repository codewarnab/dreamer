package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveProjectNameAddsHashForCollidingBasename(t *testing.T) {
	firstPath := filepath.Join(t.TempDir(), "same")
	secondPath := filepath.Join(t.TempDir(), "same")
	used := map[string]string{
		DeriveProjectName(firstPath, nil): firstPath,
	}

	secondName := DeriveProjectName(secondPath, used)

	if !strings.HasPrefix(secondName, "project-same-") {
		t.Fatalf("secondName = %q, want hash-suffixed project-same name", secondName)
	}
	if secondName == "project-same" {
		t.Fatalf("secondName collided with unsuffixed project name")
	}
}

func TestResolveAbsoluteProjectPathExpandsHomeAndCleans(t *testing.T) {
	home := t.TempDir()
	setPipelineTestHome(t, home)

	nested := filepath.Join(home, "repo")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	resolved, err := resolveAbsoluteProjectPath(filepath.Join("~", ".", "repo"))
	if err != nil {
		t.Fatalf("resolveAbsoluteProjectPath returned error: %v", err)
	}
	if resolved != filepath.Clean(nested) {
		t.Fatalf("resolved = %q, want %q", resolved, filepath.Clean(nested))
	}
}
