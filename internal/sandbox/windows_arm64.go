//go:build windows && arm64

// ARM64 Windows stub. The getLogonSID function in windows.go uses
// unsafe.Pointer casts that assume x86-64 alignment (8 bytes). On ARM64
// the TOKEN_GROUPS struct alignment is different and the cast would crash.
// Until a proper ARM64-safe implementation using Token.GetTokenGroups()
// is added, the sandbox is disabled on ARM64 Windows.
package sandbox

import "os/exec"

// Available reports whether the OS-level sandbox is supported.
// Returns false on ARM64 Windows until the unsafe.Pointer alignment
// issue in getLogonSID is resolved.
func Available() bool { return false }

func prepare(cmd *exec.Cmd, cfg Config) (func(), error) {
	return func() {}, nil
}

func postStart(cmd *exec.Cmd, cfg Config) (func(), error) {
	return func() {}, nil
}

func postStartWithHandle(cmd *exec.Cmd, cfg Config) (uintptr, func(), error) {
	return 0, func() {}, nil
}
