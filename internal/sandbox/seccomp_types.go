//go:build linux

package sandbox

import "syscall"

// Syscall numbers not always exported by Go's syscall package.
// These are the x86_64 values; arm64 differs but seccomp is only
// compiled on amd64 (seccomp.go has //go:build linux && amd64).
// On non-amd64, these constants exist but are unused — compileSeccompBPF
// and createSeccompFD are no-ops via seccomp_stub.go.
const (
	sysMemfdCreate = 319 // SYS_MEMFD_CREATE (x86_64)
	sysSetns       = 308 // SYS_SETNS (x86_64)
)

// seccompInstrBytes is the size of one struct sock_filter BPF instruction
// on the wire: u16 opcode + u8 jt + u8 jf + u32 k. bwrap derives the
// instruction count from fd size / 8, so the serialized program must be a
// multiple of this.
const seccompInstrBytes = 8

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
			{Name: "setns", NR: sysSetns},
			{Name: "unshare", NR: syscall.SYS_UNSHARE},
		},
	}
)
