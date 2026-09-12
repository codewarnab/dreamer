//go:build linux && !amd64

// Non-amd64 Linux stub for seccomp BPF. The seccomp filter uses
// x86_64-specific syscall numbers and is only functional on amd64.
// On other architectures, seccomp is silently skipped — the sandbox
// still enforces filesystem isolation via bwrap's --ro-bind / /.

package sandbox

import (
	"golang.org/x/net/bpf"
)

// compileSeccompBPF is a no-op on non-amd64 architectures.
func compileSeccompBPF(_ SeccompProfile) ([]bpf.RawInstruction, error) {
	return nil, nil
}

// createSeccompFD is a no-op on non-amd64 architectures.
func createSeccompFD(_ []bpf.RawInstruction) (uintptr, error) {
	return 0, nil
}
