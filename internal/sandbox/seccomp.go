//go:build linux && amd64

// Seccomp BPF filter for Linux sandbox (x86_64 only).
//
// ARM64 CONTRIBUTOR NOTES: To add arm64 support:
//  1. Replace hardcoded syscall numbers with arch-aware constants (e.g.,
//     SYS_PTRACE_arm64 = 117 vs SYS_PTRACE_amd64 = 101).
//  2. Add an arch check in compileBlockSyscalls: load offset 4 (arch field
//     in seccomp_data), branch on AUDIT_ARCH_AARCH64 (0xC00000B7) vs
//     AUDIT_ARCH_X86_64 (0xC000003E).
//  3. Consider using golang.org/x/sys/unix for portable syscall numbers.
//  4. The memfd_create and write syscalls use SYS_MEMFD_CREATE and SYS_WRITE
//     which have different numbers on arm64 — use unix.MemfdCreate instead.

package sandbox

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/net/bpf"
)

// compileSeccompBPF compiles a SeccompProfile into raw BPF instructions
// suitable for passing to bwrap via --seccomp fd.
func compileSeccompBPF(profile SeccompProfile) ([]bpf.RawInstruction, error) {
	// maxRules caps the number of syscall rules. BPF skip distances are
	// encoded as uint8, so the maximum skip is 255. With 2 instructions
	// per rule + 2 terminal instructions (ALLOW + KILL), the first rule's
	// skip = totalLen - 2 - 0 = 2*(maxRules+1). For skip <= 255 we need
	// maxRules <= 126; we use 127 as a safe ceiling (skip=256 would
	// overflow uint8 to 0, making the first rule a no-op).
	const maxRules = 127
	if len(profile.Rules) > maxRules {
		return nil, fmt.Errorf("seccomp: too many rules (%d, max %d)", len(profile.Rules), maxRules)
	}
	// An empty profile has nothing to block — return an empty program so
	// createSeccompFD yields fd=0 and buildBwrapArgs omits --seccomp entirely.
	if len(profile.Rules) == 0 {
		return nil, nil
	}

	var insns []bpf.Instruction
	for _, rule := range profile.Rules {
		nr := rule.NR
		if nr < 0 {
			return nil, fmt.Errorf("seccomp: rule %q has invalid NR %d", rule.Name, nr)
		}
		// Load syscall number from offset 0 in seccomp_data.
		insns = append(insns,
			bpf.LoadAbsolute{Off: 0, Size: 4},
			bpf.JumpIf{Cond: bpf.JumpEqual, Val: uint32(nr), SkipTrue: 1, SkipFalse: 0},
		)
	}
	// Default: allow.
	insns = append(insns, bpf.RetConstant{Val: 0x7FFF0000}) // SECCOMP_RET_ALLOW

	// Fill in skip distances: each JEQ jumps to RET KILL_PROCESS (last instruction).
	// Use KILL_PROCESS (0x80000000) not KILL_THREAD (0x00000000) so a multithreaded
	// child that hits a blocked syscall is fully terminated, not just one thread.
	insns = append(insns, bpf.RetConstant{Val: 0x80000000}) // SECCOMP_RET_KILL_PROCESS
	totalLen := len(insns)
	for i := range insns {
		if ji, ok := insns[i].(bpf.JumpIf); ok {
			skip := totalLen - 2 - i
			if skip > 255 {
				return nil, fmt.Errorf("seccomp: skip distance %d exceeds uint8 max (too many rules for current layout)", skip)
			}
			ji.SkipTrue = uint8(skip)
			insns[i] = ji
		}
	}

	raw, err := bpf.Assemble(insns)
	if err != nil {
		return nil, fmt.Errorf("seccomp: bpf assemble: %w", err)
	}
	return raw, nil
}

// createSeccompFD writes the compiled BPF program to a memfd and returns
// the fd. bwrap applies this filter to the child process via --seccomp <fd>.
//
// bwrap --seccomp <fd> expects the fd to contain ONLY the raw struct sock_filter
// array (8 bytes per instruction). bwrap derives the instruction count from
// file size / 8. We do NOT prepend a sock_fprog header (2-byte count + 2-byte
// padding) — bwrap rejects data that isn't a multiple of 8 bytes.
func createSeccompFD(raw []bpf.RawInstruction) (uintptr, error) {
	if len(raw) == 0 {
		return 0, nil
	}

	// Encode raw sock_filter instructions (no sock_fprog header).
	// Each instruction is seccompInstrBytes bytes: u16 opcode, u8 jt, u8 jf,
	// u32 k.
	prog := make([]byte, len(raw)*seccompInstrBytes)
	for i, inst := range raw {
		off := i * seccompInstrBytes
		binary.NativeEndian.PutUint16(prog[off:off+2], inst.Op)
		prog[off+2] = inst.Jt
		prog[off+3] = inst.Jf
		binary.NativeEndian.PutUint32(prog[off+4:off+8], inst.K)
	}

	fd, _, errno := syscall.Syscall(uintptr(sysMemfdCreate),
		uintptr(unsafe.Pointer(&[]byte("seccomp-bpf\x00")[0])), 0, 1) // 1 = MFD_CLOEXEC
	if errno != 0 {
		return 0, fmt.Errorf("memfd_create: %v", errno)
	}

	n, _, errno := syscall.Syscall(syscall.SYS_WRITE, fd,
		uintptr(unsafe.Pointer(&prog[0])), uintptr(len(prog)))
	if errno != 0 {
		syscall.Close(int(fd))
		return 0, fmt.Errorf("write seccomp fd: %v", errno)
	}
	if int(n) != len(prog) {
		syscall.Close(int(fd))
		return 0, fmt.Errorf("short write seccomp fd: %d/%d", n, len(prog))
	}

	// Seek back to start for bwrap to read.
	if _, _, errno := syscall.Syscall(syscall.SYS_LSEEK, fd, 0, 0); errno != 0 {
		syscall.Close(int(fd))
		return 0, fmt.Errorf("lseek seccomp fd: %v", errno)
	}

	return fd, nil
}
