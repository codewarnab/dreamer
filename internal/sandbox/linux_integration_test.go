//go:build linux

package sandbox

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func skipIfNoBwrap(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("bwrap not available (missing binary, user namespaces disabled, or WSL1)")
	}
}

// TestIntegration_ChildRunsToCompletion verifies the sandboxed child
// process exits successfully through Prepare → Start → PostStart → Wait.
func TestIntegration_ChildRunsToCompletion(t *testing.T) {
	skipIfNoBwrap(t)

	projectDir := relocateTmpdirOutsideTmpfs(t)
	cmd := exec.Command("echo", "hello")
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: []string{relocateTmpdirOutsideTmpfs(t)},
		Mode:         ModeAuto,
	}

	cleanup, err := Prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer cleanup()

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	postCleanup, err := PostStart(cmd, cfg)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("PostStart: %v", err)
	}
	defer postCleanup()

	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: child failed: %v", err)
	}
}

// TestIntegration_WriteToReadOnlyDirFails verifies that writing to the
// project directory is blocked by bwrap's --ro-bind / /. The child
// process's attempt to create a file should fail with a non-zero exit.
func TestIntegration_WriteToReadOnlyDirFails(t *testing.T) {
	skipIfNoBwrap(t)

	projectDir := relocateTmpdirOutsideTmpfs(t)
	target := filepath.Join(projectDir, "forbidden.txt")
	cmd := exec.Command("touch", target)
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: []string{relocateTmpdirOutsideTmpfs(t)},
		Mode:         ModeAuto,
	}

	cleanup, err := Prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer cleanup()

	err = cmd.Run()
	if err == nil {
		t.Fatal("expected sandboxed write to project dir to fail, but it succeeded")
	}
}

// TestIntegration_WriteToWritableDirSucceeds verifies that writing to a
// writable directory is allowed through --bind.
func TestIntegration_WriteToWritableDirSucceeds(t *testing.T) {
	skipIfNoBwrap(t)

	projectDir := relocateTmpdirOutsideTmpfs(t)
	writableDir := relocateTmpdirOutsideTmpfs(t)
	target := filepath.Join(writableDir, "allowed.txt")

	cmd := exec.Command("touch", target)
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: []string{writableDir},
		Mode:         ModeAuto,
	}

	cleanup, err := Prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer cleanup()

	if err := cmd.Run(); err != nil {
		t.Fatalf("write to writable dir should succeed: %v", err)
	}
}

// TestIntegration_TmpIsWritable verifies that /tmp is writable inside the
// sandbox (via tmpfs).
func TestIntegration_TmpIsWritable(t *testing.T) {
	skipIfNoBwrap(t)

	projectDir := relocateTmpdirOutsideTmpfs(t)
	cmd := exec.Command("touch", "/tmp/sandbox-test-file")
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: nil,
		Mode:         ModeAuto,
	}

	cleanup, err := Prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer cleanup()

	if err := cmd.Run(); err != nil {
		t.Fatalf("write to /tmp should succeed inside sandbox: %v", err)
	}
}

// TestIntegration_DynamicBinary verifies that running a dynamically-linked
// binary (like /usr/bin/ls) works under --ro-bind / /. Library paths must
// resolve correctly through the read-only mount.
func TestIntegration_DynamicBinary(t *testing.T) {
	skipIfNoBwrap(t)

	lsPath, err := exec.LookPath("ls")
	if err != nil {
		t.Skip("ls not found in PATH")
	}

	projectDir := relocateTmpdirOutsideTmpfs(t)
	cmd := exec.Command(lsPath, projectDir)
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: nil,
		Mode:         ModeAuto,
	}

	cleanup, err := Prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer cleanup()

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ls failed inside sandbox: %v\nOutput: %s", err, string(out))
	}
}

// TestIntegration_ModeOn_NoBwrap verifies that ModeOn errors when bwrap
// is not available.
func TestIntegration_ModeOn_NoBwrap(t *testing.T) {
	if Available() {
		t.Skip("sandbox available, error path not triggered")
	}

	cmd := exec.Command("echo", "hello")
	cfg := Config{
		ProjectDir:   t.TempDir(),
		WritableDirs: nil,
		Mode:         ModeOn,
	}

	_, err := Prepare(cmd, cfg)
	if err == nil {
		t.Fatal("Prepare with ModeOn should error when bwrap unavailable")
	}
	if !strings.Contains(err.Error(), "not available on this platform") {
		t.Errorf("error %q should mention 'not available on this platform'", err.Error())
	}
}

// TestIntegration_ModeOn_WrapsCmdWithBwrap verifies that Prepare with
// ModeOn modifies cmd.Path to bwrap and returns a non-nil cleanup.
func TestIntegration_ModeOn_WrapsCmdWithBwrap(t *testing.T) {
	skipIfNoBwrap(t)

	projectDir := relocateTmpdirOutsideTmpfs(t)
	cmd := exec.Command("echo", "hello")
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: []string{relocateTmpdirOutsideTmpfs(t)},
		Mode:         ModeOn,
	}

	cleanup, err := Prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("Prepare with ModeOn: %v", err)
	}
	defer cleanup()

	if cleanup == nil {
		t.Fatal("Prepare with ModeOn should return non-nil cleanup")
	}
	// Prepare should have replaced cmd.Path with the bwrap binary.
	if !strings.Contains(cmd.Path, "bwrap") {
		t.Fatalf("cmd.Path = %q, expected bwrap binary path", cmd.Path)
	}
}

// TestIntegration_PostStart_NoOp verifies postStart is a no-op (no kernel
// handles to release, unlike Windows Job Objects).
func TestIntegration_PostStart_NoOp(t *testing.T) {
	skipIfNoBwrap(t)

	projectDir := relocateTmpdirOutsideTmpfs(t)
	cmd := exec.Command("echo", "hello")
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: nil,
		Mode:         ModeAuto,
	}

	cleanup, err := Prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer cleanup()

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	postCleanup, err := PostStart(cmd, cfg)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("PostStart: %v", err)
	}
	defer postCleanup()

	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}
