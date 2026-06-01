// Package permissionbypasstest — good patterns that should NOT trigger findings.
package permissionbypasstest

import "sandbox"

// goodCheckFirst guards with an if-check on Available.
func goodCheckFirst(cfg *ProviderConfig) {
	if sandbox.Available() {
		cfg.SandboxProjectWrite = true
	}
}

// goodReadOnly does not set any write posture.
func goodReadOnly(cfg *ProviderConfig) {
	cfg.Sandbox = "auto"
}

// goodGuardedByAvailable guards both fields with early return.
func goodGuardedByAvailable(cfg *ProviderConfig, paths []string) {
	if !sandbox.Available() {
		return
	}
	cfg.SandboxProjectWrite = true
	cfg.SandboxWritableDirs = append([]string(nil), paths...)
}
