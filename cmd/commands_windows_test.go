//go:build windows

package cmd

import (
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
	// killDaemon on a non-existent PID should not error on Windows because
	// taskkill reports "not found" and we treat that as success.
	err := killDaemon(9999999)
	if err != nil {
		t.Fatalf("expected nil for non-existent PID, got: %v", err)
	}
}
