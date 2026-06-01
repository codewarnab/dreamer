//go:build windows

package procutil

import (
	"os/exec"
	"testing"
)

func TestSetNoWindow(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit", "0")
	SetNoWindow(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr should not be nil after SetNoWindow")
	}
	if cmd.SysProcAttr.CreationFlags&CreateNoWindow == 0 {
		t.Errorf("CreationFlags 0x%x missing CreateNoWindow (0x%x)", cmd.SysProcAttr.CreationFlags, CreateNoWindow)
	}
}

func TestSetNoWindow_Idempotent(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit", "0")
	SetNoWindow(cmd)
	SetNoWindow(cmd) // second call should be harmless
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr should not be nil after SetNoWindow")
	}
	if cmd.SysProcAttr.CreationFlags&CreateNoWindow == 0 {
		t.Errorf("CreationFlags 0x%x missing CreateNoWindow after double call", cmd.SysProcAttr.CreationFlags)
	}
}
