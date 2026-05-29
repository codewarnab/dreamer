//go:build linux

// Linux sandbox using bubblewrap (bwrap). The entire host filesystem is
// mounted read-only via --ro-bind / /, with selective --bind for writable
// directories. User and PID namespaces isolate the child process.
//
// File layout:
//
//	linux.go        — Available(), prepare(), postStart()
//	linux_bwrap.go  — bwrapPath(), userNamespacesEnabled(), isWSL1(), buildBwrapArgs()
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Available reports whether bubblewrap sandboxing is supported on this
// system. Returns true only when bwrap is in PATH, user namespaces are
// enabled, and the host is not WSL1 (which lacks user namespace support).
func Available() bool {
	return bwrapPath() != "" && userNamespacesEnabled() && !isWSL1()
}

// prepare wraps cmd with bwrap to sandbox the child process. The original
// binary and args are preserved after the "--" separator in the bwrap
// argument list. cmd.Path and cmd.Args are modified in-place.
//
// Writable dirs are resolved once here (Abs + EvalSymlinks + MkdirAll +
// containment validation) and passed as pre-resolved paths to
// buildBwrapArgs, which becomes a pure string-assembly function.
//
// Returns a no-op cleanup — bwrap handles its own lifecycle via
// --die-with-parent and --unshare-pid.
func prepare(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	projectDir, err := filepath.Abs(cfg.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("sandbox: resolve project dir: %w", err)
	}
	projectDir, err = filepath.EvalSymlinks(projectDir)
	if err != nil {
		return nil, fmt.Errorf("sandbox: resolve symlinks in project dir: %w", err)
	}

	// Resolve and validate writable dirs once. buildBwrapArgs receives
	// pre-resolved paths and is a pure string-assembly function.
	resolvedDirs, err := resolveAndValidateWritableDirs(cfg.WritableDirs, projectDir, cfg.ProjectWrite)
	if err != nil {
		return nil, err
	}

	bwrapBinPath := bwrapPath()
	originalBinary := cmd.Path
	var originalArgs []string
	if len(cmd.Args) > 1 {
		originalArgs = cmd.Args[1:]
	}

	// Compile seccomp BPF filter and create a memfd for --seccomp.
	// The memfd is added to cmd.ExtraFiles so Go's exec package gives
	// it a deterministic child fd (3 + index). MFD_CLOEXEC ensures the
	// parent fd is closed on exec; the child inherits via ExtraFiles dup.
	var seccompFile *os.File
	if cfg.Seccomp != SeccompOff {
		var profile SeccompProfile
		switch cfg.Seccomp {
		case SeccompFull:
			profile = profileFull
		default:
			profile = profileMinimal
		}
		raw, err := compileSeccompBPF(profile)
		if err != nil {
			return nil, fmt.Errorf("seccomp compile: %w", err)
		}
		fd, err := createSeccompFD(raw)
		if err != nil {
			return nil, fmt.Errorf("seccomp fd: %w", err)
		}
		if fd > 0 {
			seccompFile = os.NewFile(fd, "seccomp-bpf")
			cmd.ExtraFiles = append(cmd.ExtraFiles, seccompFile)
		}
	}

	// Child fd for --seccomp: stdin=0, stdout=1, stderr=2, ExtraFiles start at 3.
	var seccompChildFD uintptr
	if seccompFile != nil {
		seccompChildFD = uintptr(3 + len(cmd.ExtraFiles) - 1)
	}

	cmd.Path = bwrapBinPath
	cmd.Args = append(
		[]string{bwrapBinPath},
		buildBwrapArgs(cfg, projectDir, resolvedDirs, originalBinary, originalArgs, seccompChildFD, bwrapSupportsRlimit() == rlimitSupported)...,
	)

	cleanup = func() {
		if seccompFile != nil {
			seccompFile.Close()
		}
	}
	return cleanup, nil
}

// postStart is a no-op on Linux. Unlike Windows (where Job Objects manage
// child lifecycle), bwrap's --die-with-parent and --unshare-pid ensure all
// descendants are killed when the parent exits. No kernel handles to release.
func postStart(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	return func() {}, nil
}
