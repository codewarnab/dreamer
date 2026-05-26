//go:build windows

package cmd

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestDetachedProcessAttr(t *testing.T) {
	attr := detachedProcessAttr()
	if attr == nil {
		t.Fatal("detachedProcessAttr returned nil")
	}
	expected := syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008 // DETACHED_PROCESS
	if attr.CreationFlags != uint32(expected) {
		t.Fatalf("CreationFlags = 0x%x, want 0x%x", attr.CreationFlags, expected)
	}
}

func TestKillDaemon_ProcessNotRunning(t *testing.T) {
	// Start a short-lived process, wait for it to exit, then use its
	// guaranteed-defunct PID — avoids hardcoding a PID that might collide
	// with a real process on long-running hosts.
	cmd := exec.Command("cmd", "/c", "exit", "0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("cmd.Run: %v", err)
	}
	err := killDaemon(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("expected nil for defunct PID, got: %v", err)
	}
}
