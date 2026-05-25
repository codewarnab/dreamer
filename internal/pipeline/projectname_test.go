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

// TestDeriveProjectName_CollisionWithDifferentNames tests that two configured
// projects with different names but the same path basename produce distinct
// derived names when seeded with the pipeline's dual-key collision map.
func TestDeriveProjectName_CollisionWithDifferentNames(t *testing.T) {
	pathA := filepath.Join(t.TempDir(), "myapp")
	pathB := filepath.Join(t.TempDir(), "myapp")

	// Simulate pipeline.go's dual-key seeding: "project-<safe(base)>"
	// plus bare name.
	usedNames := map[string]string{
		"project-myapp":  pathA, // from "project-" + safe(filepath.Base(path))
		"custom-name-a":  pathA, // bare configured Name
	}

	nameB := DeriveProjectName(pathB, usedNames)
	nameA := DeriveProjectName(pathA, nil) // no collision without map

	if nameA == nameB {
		t.Fatalf("names collided: both got %q", nameA)
	}
	if !strings.HasPrefix(nameB, "project-myapp-") {
		t.Fatalf("expected hash-suffixed name, got %q", nameB)
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
