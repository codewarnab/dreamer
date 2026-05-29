package fsutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

func TestListProjectFilesWalkDir(t *testing.T) {
	root := t.TempDir()

	// Create a small project tree.
	for _, f := range []string{
		"main.go",
		"src/app.ts",
		"src/utils/helper.go",
		"README.md",
	} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Create a directory that should be skipped.
	skipDir := filepath.Join(root, "node_modules", "pkg")
	if err := os.MkdirAll(skipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skipDir, "index.js"), []byte("skip"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a binary file that should be skipped.
	if err := os.WriteFile(filepath.Join(root, "image.png"), []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}

	// WalkDir path (not a git repo).
	files, err := listViaWalkDir(root)
	if err != nil {
		t.Fatalf("listViaWalkDir: %v", err)
	}

	want := []string{
		"README.md",
		"main.go",
		"src/app.ts",
		"src/utils/helper.go",
	}
	sort.Strings(want)
	sort.Strings(files)

	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(files), len(want), files)
	}
	for i, w := range want {
		if files[i] != w {
			t.Errorf("files[%d] = %q, want %q", i, files[i], w)
		}
	}
}

func TestListProjectFilesProtectedDirsSkipped(t *testing.T) {
	root := t.TempDir()

	for _, dir := range []string{".dreamer", ".claude", ".codex", ".copilot", ".gemini"} {
		p := filepath.Join(root, dir)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "secret.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Add a normal file.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := listViaWalkDir(root)
	if err != nil {
		t.Fatalf("listViaWalkDir: %v", err)
	}

	if len(files) != 1 || files[0] != "main.go" {
		t.Fatalf("expected only main.go, got: %v", files)
	}
}

func TestListProjectFilesGitRepo(t *testing.T) {
	// Skip if git is not available.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}

	root := t.TempDir()

	// Initialize a git repo.
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}

	// Create files.
	for _, f := range []string{"main.go", "lib/util.go", ".gitignore"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// .gitignore that ignores build/
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an ignored directory.
	buildDir := filepath.Join(root, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "output.bin"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := ListProjectFiles(root, 0)
	if err != nil {
		t.Fatalf("ListProjectFiles: %v", err)
	}

	// build/output.bin should be ignored by .gitignore.
	want := []string{".gitignore", "lib/util.go", "main.go"}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(files), len(want), files)
	}
	for i, w := range want {
		if files[i] != w {
			t.Errorf("files[%d] = %q, want %q", i, files[i], w)
		}
	}
}

func TestListProjectFilesCap(t *testing.T) {
	root := t.TempDir()

	// Create 10 files.
	for i := 0; i < 10; i++ {
		name := filepath.Join(root, "file"+string(rune('a'+i))+".go")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := ListProjectFiles(root, 3)
	if err != nil {
		t.Fatalf("ListProjectFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files (cap), got %d", len(files))
	}
}

func TestListProjectFilesEmptyRoot(t *testing.T) {
	files, err := ListProjectFiles("", 0)
	if err != nil {
		t.Fatalf("ListProjectFiles empty: %v", err)
	}
	if files != nil {
		t.Fatalf("expected nil, got %v", files)
	}
}

func TestListProjectFilesEmptyDir(t *testing.T) {
	root := t.TempDir()
	files, err := ListProjectFiles(root, 0)
	if err != nil {
		t.Fatalf("ListProjectFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
}
