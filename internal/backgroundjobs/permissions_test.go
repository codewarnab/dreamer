package backgroundjobs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateWritablePathsAbsolute(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "src"),
		filepath.Join(root, "out", "report.md"),
	}
	if err := ValidateWritablePaths(root, paths); err != nil {
		t.Fatalf("ValidateWritablePaths: %v", err)
	}
}

func TestValidateWritablePathsRelative(t *testing.T) {
	root := t.TempDir()
	paths := []string{"src/main.go", "output/report.md"}
	if err := ValidateWritablePaths(root, paths); err != nil {
		t.Fatalf("ValidateWritablePaths relative: %v", err)
	}
}

func TestValidateWritablePathsOutsideProject(t *testing.T) {
	root := t.TempDir()
	otherDir := t.TempDir()
	paths := []string{filepath.Join(otherDir, "evil.txt")}
	err := ValidateWritablePaths(root, paths)
	if err == nil {
		t.Fatal("expected error for path outside project root")
	}
}

func TestValidateWritablePathsProtectedSuffixes(t *testing.T) {
	root := t.TempDir()
	protected := []string{
		filepath.Join(root, ".git", "config"),
		filepath.Join(root, "src", ".codex", "secret"),
		filepath.Join(root, ".claude", "settings"),
		filepath.Join(root, ".dreamer", "state.json"),
		filepath.Join(root, ".gemini", "auth"),
		filepath.Join(root, ".copilot", "token"),
	}
	for _, p := range protected {
		err := ValidateWritablePaths(root, []string{p})
		if err == nil {
			t.Errorf("expected error for protected path %q", p)
		}
	}
}

func TestValidateWritablePathsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")

	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	err := ValidateWritablePaths(root, []string{link})
	if err == nil {
		t.Fatal("expected error for symlink escaping project root")
	}
}

func TestValidateWritablePathsEmpty(t *testing.T) {
	root := t.TempDir()
	if err := ValidateWritablePaths(root, nil); err != nil {
		t.Fatalf("ValidateWritablePaths empty: %v", err)
	}
}

func TestValidateWritablePathsTraversalInName(t *testing.T) {
	root := t.TempDir()
	// Path with ".." that stays inside root after cleaning
	paths := []string{filepath.Join(root, "src", "..", "src", "main.go")}
	if err := ValidateWritablePaths(root, paths); err != nil {
		t.Fatalf("ValidateWritablePaths traversal: %v", err)
	}
}
