//go:build !windows || (windows && arm64)

package sandbox

import (
	"os/exec"
	"testing"
)

func TestPrepare_ModeOnUnavailable_ReturnsError(t *testing.T) {
	cmd := exec.Command("dreamer-test-placeholder")
	_, err := Prepare(cmd, Config{Mode: ModeOn, ProjectDir: t.TempDir()})
	if err == nil {
		t.Fatal("Prepare with ModeOn should fail when native sandbox is unavailable")
	}
}

func TestPrepare_ModeAutoUnavailable_ReturnsNoop(t *testing.T) {
	cmd := exec.Command("dreamer-test-placeholder")
	cleanup, err := Prepare(cmd, Config{Mode: ModeAuto, ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Prepare with ModeAuto should fall back to provider policy flags, got: %v", err)
	}
	if cleanup == nil {
		t.Fatal("Prepare with ModeAuto should return cleanup")
	}
	cleanup()
}
