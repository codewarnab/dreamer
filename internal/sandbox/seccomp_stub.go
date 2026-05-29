//go:build linux && !amd64

// Non-amd64 Linux stub for seccomp BPF. The seccomp filter uses
// x86_64-specific syscall numbers and is only functional on amd64.
// On other architectures, seccomp is silently skipped — the sandbox
// still enforces filesystem isolation via bwrap's --ro-bind / /.

package sandbox

import (
	"syscall"

	"golang.org/x/net/bpf"
)

// SeccompProfile is a named set of syscall rules.
type SeccompProfile struct {
	Name  string
	Rules []SyscallRule
}

// SyscallRule blocks a specific syscall number.
type SyscallRule struct {
	// Name is the human-readable syscall name (for logging).
	Name string
	// NR is the syscall number.
	NR int
}

var (
	// profileMinimal blocks ptrace (process injection vector).
	profileMinimal = SeccompProfile{
		Name: "minimal",
		Rules: []SyscallRule{
			{Name: "ptrace", NR: syscall.SYS_PTRACE},
		},
	}
	// profileFull blocks ptrace + escalation syscalls.
	profileFull = SeccompProfile{
		Name: "full",
		Rules: []SyscallRule{
			{Name: "ptrace", NR: syscall.SYS_PTRACE},
			{Name: "mount", NR: syscall.SYS_MOUNT},
			{Name: "umount2", NR: syscall.SYS_UMOUNT2},
			{Name: "pivot_root", NR: syscall.SYS_PIVOT_ROOT},
			{Name: "chroot", NR: syscall.SYS_CHROOT},
			{Name: "reboot", NR: syscall.SYS_REBOOT},
			{Name: "unshare", NR: syscall.SYS_UNSHARE},
		},
	}
)

// compileSeccompBPF is a no-op on non-amd64 architectures.
func compileSeccompBPF(_ SeccompProfile) ([]bpf.RawInstruction, error) {
	return nil, nil
}

// createSeccompFD is a no-op on non-amd64 architectures.
func createSeccompFD(_ []bpf.RawInstruction) (uintptr, error) {
	return 0, nil
}
