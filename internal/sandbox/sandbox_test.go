package sandbox

// Platform coverage:
//
//   - Available() returns false on non-Windows, so prepare/postStart are no-ops.
//   - ModeOn tests that gate on Available() will skip on non-Windows.
//   - ModeOff tests are cross-platform (early-return before platform check).
//   - BuildConfig/ParseMode/ShouldUseNative are pure logic, fully cross-platform.
//   - PostStartOrKill cleanup-on-error path needs a real process; only the
//     nil-cmd and nil-process guards are testable without admin.
//   - Windows-specific token/ACL/Job Object tests live in sandbox_windows_test.go
//     and require admin (SeAssignPrimaryTokenPrivilege).
//
// Reviewer note: tests that call Available() and skip when true are testing the
// "sandbox unavailable" code path. On Windows with admin, those paths are
// covered by sandbox_windows_test.go. Neither set is a false positive — they
// complement each other across build constraints.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sandboxTestTmpfsPaths mirrors the Linux build's tmpPaths (linux_bwrap.go).
// Keep in sync — it decides whether the default temp location is covered by
// the in-sandbox tmpfs and therefore unusable as a --bind writable dir.
var sandboxTestTmpfsPaths = []string{"/tmp", "/var/tmp"}

// relocateTmpdirOutsideTmpfs returns a fresh directory suitable for use as
// a sandbox writable dir or project dir. When the default temp location is
// covered by the sandbox tmpfs paths, it points TMPDIR at a new directory
// under the user's home so BuildConfig's default os.TempDir writable entry
// no longer overlaps paths under /tmp. Skips when no such location exists.
func relocateTmpdirOutsideTmpfs(t *testing.T) string {
	t.Helper()
	if !underAnyPath(os.TempDir(), sandboxTestTmpfsPaths) {
		return t.TempDir()
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" || underAnyPath(home, sandboxTestTmpfsPaths) {
		t.Skip("no home directory outside sandbox tmpfs paths; cannot relocate TMPDIR")
	}
	base, err := os.MkdirTemp(home, ".dreamer-test-tmp-")
	if err != nil {
		t.Skipf("create temp base under home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	t.Setenv("TMPDIR", base)
	return t.TempDir()
}

// underAnyPath reports whether p equals or lives under any of prefixes.
func underAnyPath(p string, prefixes []string) bool {
	cleaned := filepath.Clean(p)
	for _, tp := range prefixes {
		if cleaned == tp || strings.HasPrefix(cleaned, tp+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		input string
		want  Mode
		err   bool
	}{
		{"", ModeAuto, false},
		{"auto", ModeAuto, false},
		{"AUTO", ModeAuto, false},
		{"true", ModeOn, false},
		{"on", ModeOn, false},
		{"require", ModeOn, false},
		{"false", ModeOff, false},
		{"off", ModeOff, false},
		{"disable", ModeOff, false},
		{"maybe", "", true},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got, err := ParseMode(c.input)
			if c.err {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", c.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.input, err)
			}
			if got != c.want {
				t.Errorf("ParseMode(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestPrepare_ModeOff_ReturnsNilOnNilCmd(t *testing.T) {
	cfg := Config{
		ProjectDir: "/tmp/test",
		Mode:       ModeOff,
	}
	// Prepare with nil cmd is fine when mode is off — it returns early.
	// (We can't test with a real cmd without starting a process.)
	cleanup, err := Prepare(nil, cfg)
	if err != nil {
		t.Fatalf("Prepare with ModeOff should return nil error, got: %v", err)
	}
	if cleanup == nil {
		t.Fatal("Prepare with ModeOff should return non-nil cleanup")
	}
	cleanup() // should be safe to call
}

func TestPostStart_ModeOff_ReturnsNilOnNilCmd(t *testing.T) {
	cfg := Config{
		ProjectDir: "/tmp/test",
		Mode:       ModeOff,
	}
	cleanup, err := PostStart(nil, cfg)
	if err != nil {
		t.Fatalf("PostStart with ModeOff should return nil error, got: %v", err)
	}
	if cleanup == nil {
		t.Fatal("PostStart with ModeOff should return non-nil cleanup")
	}
	cleanup() // should be safe to call
}

func TestBuildConfig(t *testing.T) {
	// Relocate TMPDIR when it covers the sandbox tmpfs paths, otherwise
	// BuildConfig's default writable dir (os.TempDir) overlaps the
	// /tmp-prefixed project dir and fails validation.
	relocateTmpdirOutsideTmpfs(t)
	cfg, err := BuildConfig("/tmp/project", ".claude", "auto")
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if cfg.ProjectDir != "/tmp/project" {
		t.Errorf("ProjectDir = %q, want %q", cfg.ProjectDir, "/tmp/project")
	}
	if cfg.Mode != ModeAuto {
		t.Errorf("Mode = %q, want %q", cfg.Mode, ModeAuto)
	}
	if len(cfg.WritableDirs) != 2 {
		t.Fatalf("WritableDirs len = %d, want 2", len(cfg.WritableDirs))
	}
}

func TestBuildConfig_InvalidMode(t *testing.T) {
	_, err := BuildConfig("/tmp/project", ".claude", "maybe")
	if err == nil {
		t.Fatal("BuildConfig with invalid mode should return error")
	}
}

func TestShouldUseNativeModeOff(t *testing.T) {
	if ShouldUseNative(ModeOff) {
		t.Fatal("ModeOff should never use native")
	}
}

func TestShouldUseNativeModeOn(t *testing.T) {
	// MEDIUM #11: ShouldUseNative(ModeOn) and ShouldUseNative(ModeAuto) both
	// collapse to Available() because the implementation is
	// `mode != ModeOff && Available()`. Distinguishing ModeOn from ModeAuto
	// requires platform-specific tests (Windows admin vs non-admin).
	// Here we test the cross-platform invariant: ModeOn delegates to Available().
	got := ShouldUseNative(ModeOn)
	if got != Available() {
		t.Fatalf("ShouldUseNative(ModeOn) = %v, want Available() = %v", got, Available())
	}
}

func TestShouldUseNativeModeAuto(t *testing.T) {
	got := ShouldUseNative(ModeAuto)
	if got != Available() {
		t.Fatalf("ShouldUseNative(ModeAuto) = %v, want Available() = %v", got, Available())
	}
}

func TestPostStartOrKill_NilCmd(t *testing.T) {
	_, err := PostStartOrKill(nil, Config{Mode: ModeOff}, nil, nil, "test")
	if err != nil {
		t.Fatalf("PostStartOrKill with nil cmd + ModeOff: %v", err)
	}
}

func TestPostStartOrKill_NilProcess(t *testing.T) {
	cmd := exec.Command("nonexistent-binary")
	// cmd.Process is nil because we haven't started it.
	cfg := Config{Mode: ModeAuto}
	cleanup, err := PostStartOrKill(cmd, cfg, nilCloser{}, nilCloser{}, "test")
	if err != nil {
		t.Fatalf("PostStartOrKill with nil Process: %v", err)
	}
	if cleanup != nil {
		cleanup()
	}
}

func TestPrepare_NilCmdModeAuto(t *testing.T) {
	if Available() {
		t.Skip("test requires sandbox unavailable")
	}
	cleanup, err := Prepare(nil, Config{Mode: ModeAuto})
	if err != nil {
		t.Fatalf("Prepare(nil, ModeAuto) on unavailable: %v", err)
	}
	if cleanup != nil {
		cleanup()
	}
}

func TestPostStart_NilCmdModeAuto(t *testing.T) {
	if Available() {
		t.Skip("test requires sandbox unavailable")
	}
	cleanup, err := PostStart(nil, Config{Mode: ModeAuto})
	if err != nil {
		t.Fatalf("PostStart(nil, ModeAuto) on unavailable: %v", err)
	}
	if cleanup != nil {
		cleanup()
	}
}

func TestPrepare_NilCmdModeOn(t *testing.T) {
	if Available() {
		t.Skip("test requires sandbox unavailable")
	}
	_, err := Prepare(nil, Config{Mode: ModeOn})
	if err == nil {
		t.Fatal("Prepare(nil, ModeOn) should error when unavailable")
	}
}

func TestPostStart_NilCmdModeOn(t *testing.T) {
	if Available() {
		t.Skip("test requires sandbox unavailable")
	}
	_, err := PostStart(nil, Config{Mode: ModeOn})
	if err == nil {
		t.Fatal("PostStart(nil, ModeOn) should error when unavailable")
	}
}

func TestParseModeWhitespace(t *testing.T) {
	// ParseMode trims whitespace.
	got, err := ParseMode("  auto  ")
	if err != nil {
		t.Fatalf("ParseMode('  auto  '): %v", err)
	}
	if got != ModeAuto {
		t.Fatalf("got %q, want %q", got, ModeAuto)
	}
}

func TestBuildConfigWritableDirs(t *testing.T) {
	relocateTmpdirOutsideTmpfs(t)
	cfg, err := BuildConfig("/tmp/project", ".myprovider", "off")
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if len(cfg.WritableDirs) != 2 {
		t.Fatalf("WritableDirs len = %d, want 2", len(cfg.WritableDirs))
	}
	if cfg.WritableDirs[0] == "" {
		t.Fatal("WritableDirs[0] (temp) should not be empty")
	}
}

// --- additional edge-case tests ---

func TestParseModeEmpty(t *testing.T) {
	got, err := ParseMode("")
	if err != nil {
		t.Fatalf("ParseMode empty: %v", err)
	}
	if got != ModeAuto {
		t.Fatalf("got %q, want %q", got, ModeAuto)
	}
}

func TestParseModeCaseInsensitive(t *testing.T) {
	cases := []string{"AUTO", "Auto", "TRUE", "True", "FALSE", "False", "ON", "OFF"}
	for _, c := range cases {
		_, err := ParseMode(c)
		if err != nil {
			t.Fatalf("ParseMode(%q): unexpected error: %v", c, err)
		}
	}
}

func TestParseModeAliasesRequire(t *testing.T) {
	got, err := ParseMode("require")
	if err != nil {
		t.Fatalf("ParseMode(require): %v", err)
	}
	if got != ModeOn {
		t.Fatalf("got %q, want %q", got, ModeOn)
	}
}

func TestParseModeAliasesDisable(t *testing.T) {
	got, err := ParseMode("disable")
	if err != nil {
		t.Fatalf("ParseMode(disable): %v", err)
	}
	if got != ModeOff {
		t.Fatalf("got %q, want %q", got, ModeOff)
	}
}

func TestBuildConfigModeAuto(t *testing.T) {
	relocateTmpdirOutsideTmpfs(t)
	cfg, err := BuildConfig("/tmp/p", ".test", "auto")
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if cfg.Mode != ModeAuto {
		t.Fatalf("Mode = %q, want %q", cfg.Mode, ModeAuto)
	}
	if cfg.ProjectDir != "/tmp/p" {
		t.Fatalf("ProjectDir = %q, want /tmp/p", cfg.ProjectDir)
	}
}

func TestBuildConfigModeOn(t *testing.T) {
	relocateTmpdirOutsideTmpfs(t)
	cfg, err := BuildConfig("/tmp/p", ".test", "true")
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if cfg.Mode != ModeOn {
		t.Fatalf("Mode = %q, want %q", cfg.Mode, ModeOn)
	}
}

func TestBuildConfigSecondWritableDirContainsProviderHome(t *testing.T) {
	relocateTmpdirOutsideTmpfs(t)
	cfg, err := BuildConfig("/tmp/p", ".my-sandbox-provider", "off")
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if len(cfg.WritableDirs) < 2 {
		t.Fatal("expected at least 2 writable dirs")
	}
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".my-sandbox-provider")
	if cfg.WritableDirs[1] != expected {
		t.Fatalf("WritableDirs[1] = %q, want %q", cfg.WritableDirs[1], expected)
	}
}

func TestPostStartOrKill_NilStdinStdout(t *testing.T) {
	// Should not panic when stdin/stdout are nil and mode is off
	cmd := exec.Command("nonexistent-binary")
	_, err := PostStartOrKill(cmd, Config{Mode: ModeOff}, nilCloser{}, nilCloser{}, "test")
	if err != nil {
		t.Fatalf("PostStartOrKill: %v", err)
	}
}

func TestPostStartOrKill_NilCmdModeOff(t *testing.T) {
	_, err := PostStartOrKill(nil, Config{Mode: ModeOff}, nilCloser{}, nilCloser{}, "test")
	if err != nil {
		t.Fatalf("PostStartOrKill nil cmd ModeOff: %v", err)
	}
}

func TestPrepareNilCmdModeOff(t *testing.T) {
	cleanup, err := Prepare(nil, Config{Mode: ModeOff})
	if err != nil {
		t.Fatalf("Prepare(nil, ModeOff): %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup")
	}
	cleanup()
}

func TestPostStartNilCmdModeOff(t *testing.T) {
	cleanup, err := PostStart(nil, Config{Mode: ModeOff})
	if err != nil {
		t.Fatalf("PostStart(nil, ModeOff): %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup")
	}
	cleanup()
}

func TestPostStartNilProcessModeOff(t *testing.T) {
	cmd := exec.Command("nonexistent-binary")
	cleanup, err := PostStart(cmd, Config{Mode: ModeOff})
	if err != nil {
		t.Fatalf("PostStart nil Process ModeOff: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup")
	}
	cleanup()
}

func TestPostStartOrKillProviderIDInErrorMessage(t *testing.T) {
	if Available() {
		t.Skip("sandbox available, error path not triggered")
	}
	cmd := exec.Command("nonexistent-binary")
	_, err := PostStartOrKill(cmd, Config{Mode: ModeOn}, nilCloser{}, nilCloser{}, "my-provider")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "my-provider") {
		t.Fatalf("error %q should contain provider ID", err.Error())
	}
}

func TestPrepareModeAutoUnavailableNilCmd(t *testing.T) {
	if Available() {
		t.Skip("sandbox available")
	}
	cleanup, err := Prepare(nil, Config{Mode: ModeAuto})
	if err != nil {
		t.Fatalf("Prepare(nil, ModeAuto): %v", err)
	}
	if cleanup != nil {
		cleanup()
	}
}

func TestShouldUseNativeModeOffAlwaysFalse(t *testing.T) {
	if ShouldUseNative(ModeOff) {
		t.Fatal("ModeOff should never use native")
	}
}

// --- ParseNetwork tests ---

func TestParseNetwork(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{"", NetworkOpen, false},
		{"open", NetworkOpen, false},
		{"OPEN", NetworkOpen, false},
		{"open ", NetworkOpen, false},
		{"isolated", NetworkIsolated, false},
		{"ISOLATED", NetworkIsolated, false},
		{"bogus", "", true},
		{" private", "", true},
	}
	for _, tt := range tests {
		got, err := ParseNetwork(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("ParseNetwork(%q) expected error", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseNetwork(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseNetwork(%q) = %q, want %q", tt.input, got, tt.want)
			}
		}
	}
}

// --- ParseSeccomp tests ---

func TestParseSeccomp(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{"", SeccompMinimal, false},
		{"off", SeccompOff, false},
		{"OFF", SeccompOff, false},
		{"minimal", SeccompMinimal, false},
		{"MINIMAL", SeccompMinimal, false},
		{"full", SeccompFull, false},
		{"FULL", SeccompFull, false},
		{"ful", "", true},
		{"on", "", true},
	}
	for _, tt := range tests {
		got, err := ParseSeccomp(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("ParseSeccomp(%q) expected error", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseSeccomp(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseSeccomp(%q) = %q, want %q", tt.input, got, tt.want)
			}
		}
	}
}

// --- ResourceLimits.Validate tests ---

func TestResourceLimitsValidate_Defaults(t *testing.T) {
	var r ResourceLimits
	if err := r.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.MemoryMB != DefaultMemoryMB {
		t.Errorf("MemoryMB = %d, want %d", r.MemoryMB, DefaultMemoryMB)
	}
	if r.Processes != DefaultProcesses {
		t.Errorf("Processes = %d, want %d", r.Processes, DefaultProcesses)
	}
	if r.FDs != DefaultFDs {
		t.Errorf("FDs = %d, want %d", r.FDs, DefaultFDs)
	}
}

func TestResourceLimitsValidate_BelowMinimum(t *testing.T) {
	tests := []struct {
		name string
		r    ResourceLimits
	}{
		{"memory", ResourceLimits{MemoryMB: MinMemoryMB - 1}},
		// MinProcesses=1, so MinProcesses-1=0 means "not set" (skipped).
		// Use a negative value to test actual below-minimum rejection.
		{"processes", ResourceLimits{Processes: -1}},
		{"fds", ResourceLimits{FDs: MinFDs - 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.r.Validate(); err == nil {
				t.Error("expected error for below-minimum value")
			}
		})
	}
}

func TestResourceLimitsValidate_ExactMinimum(t *testing.T) {
	r := ResourceLimits{MemoryMB: MinMemoryMB, Processes: MinProcesses, FDs: MinFDs}
	if err := r.Validate(); err != nil {
		t.Fatalf("unexpected error for exact minimums: %v", err)
	}
	if r.MemoryMB != MinMemoryMB {
		t.Errorf("MemoryMB = %d, want %d", r.MemoryMB, MinMemoryMB)
	}
}

// --- helper ---

type nilCloser struct{}

func (nilCloser) Close() error { return nil }
