//go:build !windows

package fsutil

import "syscall"

func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}
