// Package permissionbypasstest is a test fixture for the permissionbypass analyzer.
package permissionbypasstest

import "sandbox"

// ProviderConfig simulates the sandbox write posture fields.
type ProviderConfig struct {
	SandboxProjectWrite bool
	SandboxWritableDirs []string
	Sandbox             string
}

// --- BAD patterns: set sandbox write posture without Available() check ---

func badSetProjectWrite(cfg *ProviderConfig) {
	cfg.SandboxProjectWrite = true // want `SandboxProjectWrite set without sandbox.Available\(\) check`
}

func badSetWritableDirs(cfg *ProviderConfig, paths []string) {
	cfg.SandboxWritableDirs = append([]string(nil), paths...) // want `SandboxWritableDirs set without sandbox.Available\(\) check`
}

func badSetBoth(cfg *ProviderConfig, paths []string) {
	cfg.SandboxProjectWrite = true                           // want `SandboxProjectWrite set without sandbox.Available\(\) check`
	cfg.SandboxWritableDirs = append([]string(nil), paths...) // want `SandboxWritableDirs set without sandbox.Available\(\) check`
}

func badInsideIf(cfg *ProviderConfig, cond bool) {
	if cond {
		cfg.SandboxProjectWrite = true // want `SandboxProjectWrite set without sandbox.Available\(\) check`
	}
}

// badWarnOnly logs a warning when sandbox is absent but still escalates.
// This is the runner.go:331 pattern — the negated check doesn't guard.
func badWarnOnly(cfg *ProviderConfig) {
	if !sandbox.Available() {
		_ = "sandbox unavailable"
	}
	cfg.SandboxProjectWrite = true // want `SandboxProjectWrite set without sandbox.Available\(\) check`
}
