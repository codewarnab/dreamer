// Package sysinfo provides lightweight host-platform introspection that is
// not tied to any single subsystem: currently, Windows Subsystem for Linux
// (WSL) detection and interop-path classification.
package sysinfo

import (
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
)

// procVersionPath is the file inspected for the WSL signature. It is a
// variable so tests can point it at fixture content.
var procVersionPath = "/proc/version"

var (
	wslOnce   sync.Once
	wslResult bool
)

// IsWSL reports whether this process runs under the Windows Subsystem for
// Linux (v1 or v2). Always false on non-Linux platforms. The /proc/version
// probe runs once and is cached.
func IsWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	wslOnce.Do(func() {
		data, err := os.ReadFile(procVersionPath)
		if err != nil {
			wslResult = false
			return
		}
		wslResult = isWSLVersionReport(string(data))
	})
	return wslResult
}

// isWSLVersionReport recognizes the WSL markers in /proc/version content.
// WSL2 embeds "microsoft-standard" in the kernel release; WSL1 reports
// "Microsoft" without it. Case-insensitive matching covers both.
func isWSLVersionReport(content string) bool {
	return strings.Contains(strings.ToLower(content), "microsoft")
}

// IsWindowsInteropPath reports whether an absolute POSIX path addresses files
// through the WSL interop mounts (/mnt/<drive>/...). Only single-letter drive
// segments count; kernel-managed mounts like /mnt/wsl and /mnt/host do not.
// Always false on non-Linux platforms.
func IsWindowsInteropPath(p string) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	cleaned := path.Clean("/" + p)
	if !strings.HasPrefix(cleaned, "/mnt/") {
		return false
	}
	rest := cleaned[len("/mnt/"):]
	drive := rest
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		drive = rest[:idx]
	}
	return len(drive) == 1 && (drive[0] >= 'a' && drive[0] <= 'z' || drive[0] >= 'A' && drive[0] <= 'Z')
}
