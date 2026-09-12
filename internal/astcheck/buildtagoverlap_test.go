package astcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOverlapFile creates one Go source file under dir.
func writeOverlapFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// TestBuildTagOverlap_FlagsIssue102Shape reproduces the exact #102 collision:
// a `linux`-tagged file and a `linux && !amd64`-tagged file declaring the
// same type must be reported, since both are selected on linux/arm64.
func TestBuildTagOverlap_FlagsIssue102Shape(t *testing.T) {
	dir := t.TempDir()
	writeOverlapFile(t, dir, "seccomp_types.go", `//go:build linux

package sandbox

type SeccompProfile struct {
	Name string
}
`)
	writeOverlapFile(t, dir, "seccomp_stub.go", `//go:build linux && !amd64

package sandbox

type SeccompProfile struct {
	Name string
}
`)

	findings, err := CheckBuildTagOverlaps(dir)
	if err != nil {
		t.Fatalf("CheckBuildTagOverlaps: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(findings), findings)
	}
	got := findings[0]
	if got.Check != BuildTagOverlapCheck {
		t.Errorf("check = %q, want %q", got.Check, BuildTagOverlapCheck)
	}
	if got.Severity != SevError {
		t.Errorf("severity = %v, want error", got.Severity)
	}
	if !strings.Contains(got.Message, "SeccompProfile") {
		t.Errorf("message %q does not name the redeclared type", got.Message)
	}
	// Occurrences are ordered by path, so the finding lands on the
	// alphabetically later file and names the earlier one; accept either
	// direction as long as both files appear across pos + message.
	posBase := filepath.Base(got.Pos.Filename)
	if posBase != "seccomp_types.go" && posBase != "seccomp_stub.go" {
		t.Errorf("pos = %v, want one of the colliding files", got.Pos.Filename)
	}
	other := "seccomp_types.go"
	if posBase == "seccomp_types.go" {
		other = "seccomp_stub.go"
	}
	if !strings.Contains(got.Message, other) {
		t.Errorf("message %q does not name the other file %s", got.Message, other)
	}
	if !strings.Contains(got.Message, "//go:build linux") {
		t.Errorf("message %q does not show the colliding constraint", got.Message)
	}
}

// TestBuildTagOverlap_DisjointConstraintsSilent verifies that the same name
// under mutually exclusive constraints (linux vs windows) is not reported.
func TestBuildTagOverlap_DisjointConstraintsSilent(t *testing.T) {
	dir := t.TempDir()
	writeOverlapFile(t, dir, "a_linux.go", `//go:build linux

package sandbox

type SeccompProfile struct {
	Name string
}
`)
	writeOverlapFile(t, dir, "b_windows.go", `//go:build windows

package sandbox

type SeccompProfile struct {
	Name string
}
`)

	findings, err := CheckBuildTagOverlaps(dir)
	if err != nil {
		t.Fatalf("CheckBuildTagOverlaps: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %v", findings)
	}
}

// TestBuildTagOverlap_FilenameSuffixOverlap verifies filename-implied
// constraints participate: foo_linux.go and foo_arm64.go overlap on
// linux/arm64 even with no //go:build lines.
func TestBuildTagOverlap_FilenameSuffixOverlap(t *testing.T) {
	dir := t.TempDir()
	writeOverlapFile(t, dir, "feature_linux.go", "package sandbox\n\nfunc enable() {}\n")
	writeOverlapFile(t, dir, "feature_arm64.go", "package sandbox\n\nfunc enable() {}\n")

	findings, err := CheckBuildTagOverlaps(dir)
	if err != nil {
		t.Fatalf("CheckBuildTagOverlaps: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(findings), findings)
	}

	// Disjoint suffixes must stay silent.
	silent := t.TempDir()
	writeOverlapFile(t, silent, "feature_linux.go", "package sandbox\n\nfunc enable() {}\n")
	writeOverlapFile(t, silent, "feature_windows.go", "package sandbox\n\nfunc enable() {}\n")
	quiet, err := CheckBuildTagOverlaps(silent)
	if err != nil {
		t.Fatalf("CheckBuildTagOverlaps: %v", err)
	}
	if len(quiet) != 0 {
		t.Fatalf("expected no findings for linux/windows suffixes, got %v", quiet)
	}
}

// TestBuildTagOverlap_UnconstrainedFileOverlaps verifies a file with no
// constraint collides with any same-package redeclaration, since it is
// always included.
func TestBuildTagOverlap_UnconstrainedFileOverlaps(t *testing.T) {
	dir := t.TempDir()
	writeOverlapFile(t, dir, "common.go", "package sandbox\n\nvar profileName = \"x\"\n")
	writeOverlapFile(t, dir, "extra_linux.go", "//go:build linux\n\npackage sandbox\n\nvar profileName = \"y\"\n")

	findings, err := CheckBuildTagOverlaps(dir)
	if err != nil {
		t.Fatalf("CheckBuildTagOverlaps: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(findings), findings)
	}
}

// TestBuildTagOverlap_ExternalTestPackageSilent verifies package scoping:
// foo.go (package foo) and foo_test.go (package foo_test) share no scope,
// so identical names across them are legal.
func TestBuildTagOverlap_ExternalTestPackageSilent(t *testing.T) {
	dir := t.TempDir()
	writeOverlapFile(t, dir, "helper.go", "package foo\n\nfunc helper() {}\n")
	writeOverlapFile(t, dir, "helper_test.go", "package foo_test\n\nfunc helper() {}\n")

	findings, err := CheckBuildTagOverlaps(dir)
	if err != nil {
		t.Fatalf("CheckBuildTagOverlaps: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings across test package boundary, got %v", findings)
	}
}

// TestBuildTagOverlap_MethodReceivers verifies methods collide only when
// both receiver and name match under overlapping constraints.
func TestBuildTagOverlap_MethodReceivers(t *testing.T) {
	dir := t.TempDir()
	writeOverlapFile(t, dir, "a_linux.go", "//go:build linux\n\npackage p\n\ntype T struct{}\n\nfunc (t T) Close() {}\ntype U struct{}\n\nfunc (u U) Close() {}\n")
	writeOverlapFile(t, dir, "b_other.go", "//go:build linux && !amd64\n\npackage p\n\ntype T struct{}\n\nfunc (t T) Close() {}\ntype V struct{}\n\nfunc (v V) Close() {}\n")

	findings, err := CheckBuildTagOverlaps(dir)
	if err != nil {
		t.Fatalf("CheckBuildTagOverlaps: %v", err)
	}
	// T.Close collides; U.Close vs V.Close do not; type T collides too.
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings (type T + T.Close), got %d: %v", len(findings), findings)
	}
}

// TestBuildTagOverlap_BlankIdentifierSilent verifies `_` assignments never
// collide.
func TestBuildTagOverlap_BlankIdentifierSilent(t *testing.T) {
	dir := t.TempDir()
	writeOverlapFile(t, dir, "a.go", "package p\n\nvar _ = 1\n")
	writeOverlapFile(t, dir, "b.go", "package p\n\nvar _ = 2\n")

	findings, err := CheckBuildTagOverlaps(dir)
	if err != nil {
		t.Fatalf("CheckBuildTagOverlaps: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings for blank identifiers, got %v", findings)
	}
}

// TestBuildTagOverlap_RepeatedInitSilent verifies bare init functions never
// collide: the Go spec explicitly allows multiple init funcs per package,
// and this repo uses one per analyzer file.
func TestBuildTagOverlap_RepeatedInitSilent(t *testing.T) {
	dir := t.TempDir()
	writeOverlapFile(t, dir, "a.go", "package p\n\nfunc init() {}\n")
	writeOverlapFile(t, dir, "b_linux.go", "//go:build linux\n\npackage p\n\nfunc init() {}\n")

	findings, err := CheckBuildTagOverlaps(dir)
	if err != nil {
		t.Fatalf("CheckBuildTagOverlaps: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings for repeated init, got %v", findings)
	}
}

// TestBuildTagOverlap_SandboxRegression sweeps the real sandbox package:
// after the #102 fix no overlap may remain. This test fails if the
// duplication is ever reintroduced.
func TestBuildTagOverlap_SandboxRegression(t *testing.T) {
	findings, err := CheckBuildTagOverlaps(filepath.Join("..", "sandbox"))
	if err != nil {
		t.Fatalf("CheckBuildTagOverlaps: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("sandbox package has overlapping redeclarations: %v", findings)
	}
}
