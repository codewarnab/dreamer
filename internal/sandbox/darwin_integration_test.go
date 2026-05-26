//go:build darwin

package sandbox

import (
	"errors"
	"os/exec"
	"testing"
)

func skipIfNoSandboxExec(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("sandbox-exec not available")
	}
}

func TestIntegration_WriteToProjectDirBlocked(t *testing.T) {
	skipIfNoSandboxExec(t)
	project := t.TempDir()
	writable := t.TempDir()
	cmd := exec.Command("touch", project+"/blocked.txt")
	cfg := Config{ProjectDir: project, WritableDirs: []string{writable}, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected sandbox denial (exit 1), got: %v", err)
	}
}

func TestIntegration_WriteToWritableDirSucceeds(t *testing.T) {
	skipIfNoSandboxExec(t)
	project := t.TempDir()
	writable := t.TempDir()
	cmd := exec.Command("touch", writable+"/allowed.txt")
	cfg := Config{ProjectDir: project, WritableDirs: []string{writable}, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if err := cmd.Run(); err != nil {
		t.Fatalf("write to writable dir should succeed: %v", err)
	}
}

func TestIntegration_WriteToTmpSucceeds(t *testing.T) {
	skipIfNoSandboxExec(t)
	project := t.TempDir()
	cmd := exec.Command("touch", "/tmp/darwin_sandbox_test.txt")
	cfg := Config{ProjectDir: project, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if err := cmd.Run(); err != nil {
		t.Fatalf("write to /tmp should succeed: %v", err)
	}
}

func TestIntegration_ReadEverywhereSucceeds(t *testing.T) {
	skipIfNoSandboxExec(t)
	project := t.TempDir()
	cmd := exec.Command("cat", "/etc/hosts")
	cfg := Config{ProjectDir: project, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if err := cmd.Run(); err != nil {
		t.Fatalf("read from /etc/hosts should succeed: %v", err)
	}
}

func TestIntegration_ProcessExecutionWorks(t *testing.T) {
	skipIfNoSandboxExec(t)
	project := t.TempDir()
	cmd := exec.Command("echo", "hello")
	cfg := Config{ProjectDir: project, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if err := cmd.Run(); err != nil {
		t.Fatalf("echo should succeed: %v", err)
	}
}

func TestIntegration_ChildInheritsSandbox(t *testing.T) {
	skipIfNoSandboxExec(t)
	project := t.TempDir()
	writable := t.TempDir()
	// Try to write via a child process (sh -c touch).
	cmd := exec.Command("sh", "-c", "touch "+project+"/child_blocked.txt")
	cfg := Config{ProjectDir: project, WritableDirs: []string{writable}, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected sandbox denial (exit 1) from child, got: %v", err)
	}
}

func TestIntegration_ChildRunsToCompletion(t *testing.T) {
	skipIfNoSandboxExec(t)
	project := t.TempDir()
	writable := t.TempDir()
	cmd := exec.Command("echo", "done")
	cfg := Config{ProjectDir: project, WritableDirs: []string{writable}, Mode: ModeOn}

	cleanup, err := Prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	postCleanup, err := PostStart(cmd, cfg)
	if err != nil {
		t.Fatalf("PostStart: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	postCleanup()
	cleanup()
}

func TestIntegration_DynamicBinary(t *testing.T) {
	skipIfNoSandboxExec(t)
	project := t.TempDir()
	// /bin/ls is dynamically linked on macOS — catches dylib loading issues.
	cmd := exec.Command("/bin/ls", project)
	cfg := Config{ProjectDir: project, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if err := cmd.Run(); err != nil {
		t.Fatalf("dynamically linked binary should work: %v", err)
	}
}

func TestIntegration_ModeOn_NoSandboxExec(t *testing.T) {
	orig := sandboxExecPath
	sandboxExecPath = "/nonexistent/sandbox-exec"
	defer func() { sandboxExecPath = orig }()

	cmd := exec.Command("echo")
	cfg := Config{ProjectDir: t.TempDir(), Mode: ModeOn}
	_, err := Prepare(cmd, cfg)
	if err == nil {
		t.Fatal("Prepare with ModeOn should error when sandbox-exec missing")
	}
}

func TestIntegration_ModeAuto_NoSandboxExec(t *testing.T) {
	orig := sandboxExecPath
	sandboxExecPath = "/nonexistent/sandbox-exec"
	defer func() { sandboxExecPath = orig }()

	cmd := exec.Command("echo")
	cfg := Config{ProjectDir: t.TempDir(), Mode: ModeAuto}
	cleanup, err := Prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("Prepare with ModeAuto should fall back, got: %v", err)
	}
	if cleanup != nil {
		cleanup()
	}
}
