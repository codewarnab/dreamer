//go:build darwin

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Profile construction tests ---

func TestBuildSeatbeltProfile_ContainsVersion1(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if !strings.Contains(profile, "(version 1)") {
		t.Fatal("profile missing (version 1) header")
	}
}

func TestBuildSeatbeltProfile_ContainsDenyWrite(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if !strings.Contains(profile, "(deny file-write*)") {
		t.Fatal("profile missing (deny file-write*)")
	}
}

func TestBuildSeatbeltProfile_ProjectDirNotInWriteAllow(t *testing.T) {
	// PROJECT_DIR must NOT be in the (allow file-write*) block.
	// The (deny file-write*) protects it by default.
	profile := buildSeatbeltProfile(nil)
	if strings.Contains(profile, "PROJECT_DIR") {
		t.Fatal("profile should not contain PROJECT_DIR in write-allow block")
	}
}

func TestBuildSeatbeltProfile_AllowsPrivateTmp(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if !strings.Contains(profile, `(subpath "/private/tmp")`) {
		t.Fatal("profile should use /private/tmp, not /tmp")
	}
	if strings.Contains(profile, `(subpath "/tmp")`) {
		t.Fatal("profile should NOT contain /tmp (must use /private/tmp)")
	}
}

func TestBuildSeatbeltProfile_AllowsDevNull(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if !strings.Contains(profile, `(literal "/dev/null")`) {
		t.Fatal("profile missing /dev/null")
	}
}

func TestBuildSeatbeltProfile_NoNetworkDeny(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if strings.Contains(profile, "(deny network") {
		t.Fatal("profile should not deny network")
	}
}

func TestBuildSeatbeltProfile_ReferencesWritableDirParams(t *testing.T) {
	dirs := []string{"/a", "/b", "/c", "/d", "/e"}
	profile := buildSeatbeltProfile(dirs)
	for i := 0; i < 5; i++ {
		param := `(subpath (param "WRITABLE_` + string(rune('0'+i)) + `"))`
		if !strings.Contains(profile, param) {
			t.Fatalf("profile missing %s", param)
		}
	}
}

func TestBuildSeatbeltProfile_ZeroWritableDirs(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if strings.Contains(profile, "WRITABLE_") {
		t.Fatal("profile should have no WRITABLE entries when dirs is empty")
	}
}

func TestBuildSeatbeltProfile_NoRedundantMachLookup(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if strings.Contains(profile, "(allow mach-lookup") {
		t.Fatal("profile should not have redundant mach-lookup rules")
	}
}

func TestBuildSeatbeltProfile_NoIpcPosixSem(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if strings.Contains(profile, "(allow ipc-posix-sem") {
		t.Fatal("profile should not have ipc-posix-sem rules")
	}
}

func TestBuildSeatbeltProfile_DeniesFileLink(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if !strings.Contains(profile, "(deny file-link)") {
		t.Fatal("profile missing (deny file-link)")
	}
}

func TestBuildSeatbeltProfile_NoFileLinkReallow(t *testing.T) {
	// Hard links are denied entirely — no re-allow in writable dirs.
	// SBPL's file-link checks the destination path, so re-allowing
	// would let processes hard-link project files into writable dirs.
	dirs := []string{"/tmp/writable"}
	profile := buildSeatbeltProfile(dirs)
	if strings.Contains(profile, "(allow file-link") {
		t.Fatal("profile should not re-allow file-link in writable dirs")
	}
}

func TestBuildSeatbeltProfile_EmptyProjectDir(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if strings.Contains(profile, "PROJECT_DIR") {
		t.Fatal("profile should never contain PROJECT_DIR (protected by deny rule)")
	}
}

func TestBuildSeatbeltProfile_MoreThan3WritableDirs(t *testing.T) {
	dirs := []string{"/a", "/b", "/c", "/d"}
	profile := buildSeatbeltProfile(dirs)
	for i := 0; i < 4; i++ {
		param := `(subpath (param "WRITABLE_` + string(rune('0'+i)) + `"))`
		if !strings.Contains(profile, param) {
			t.Fatalf("profile missing %s", param)
		}
	}
}

func TestBuildSeatbeltProfile_NoProcessExecRule(t *testing.T) {
	// Under (allow default), process-exec is already allowed.
	// No redundant (allow process-exec) should appear in the profile.
	profile := buildSeatbeltProfile(nil)
	if strings.Contains(profile, "(allow process-exec)") {
		t.Fatal("profile should not contain redundant (allow process-exec)")
	}
}

func TestBuildSeatbeltProfile_NoPseudoTtyRule(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if strings.Contains(profile, "(allow pseudo-tty)") {
		t.Fatal("profile should not contain redundant (allow pseudo-tty)")
	}
}

func TestBuildSeatbeltProfile_NoSysctlReadRule(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if strings.Contains(profile, "(allow sysctl-read") {
		t.Fatal("profile should not contain redundant (allow sysctl-read)")
	}
}

func TestBuildSeatbeltProfile_NoDevTtyRule(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if strings.Contains(profile, "/dev/tty") {
		t.Fatal("profile should not contain redundant /dev/tty rule")
	}
}

func TestBuildSeatbeltProfile_ParamsMatchInputLength(t *testing.T) {
	// buildSeatbeltProfile trusts its input. The number of WRITABLE_N
	// params must equal len(writableDirs) — no skip/cap logic.
	dirs := []string{"/a", "/b", "/c"}
	profile := buildSeatbeltProfile(dirs)
	count := strings.Count(profile, "WRITABLE_")
	if count != 3 {
		t.Fatalf("profile has %d WRITABLE entries, want 3", count)
	}
}

// --- Argument construction tests ---

func TestBuildSandboxArgs_BinaryPath(t *testing.T) {
	args := buildSandboxArgs("profile", nil, "/bin/echo", nil)
	if args[0] != sandboxExecLocator() {
		t.Fatalf("first arg = %q, want %q", args[0], sandboxExecLocator())
	}
}

func TestBuildSandboxArgs_ProfileFlag(t *testing.T) {
	args := buildSandboxArgs("myprofile", nil, "/bin/echo", nil)
	if args[1] != "-p" || args[2] != "myprofile" {
		t.Fatalf("expected -p myprofile, got %q %q", args[1], args[2])
	}
}

func TestBuildSandboxArgs_WritableDirParams(t *testing.T) {
	dirs := []string{"/tmp/w1", "/tmp/w2"}
	args := buildSandboxArgs("p", dirs, "/bin/echo", nil)
	for i, d := range dirs {
		param := "WRITABLE_" + string(rune('0'+i)) + "=" + d
		found := false
		for j, a := range args {
			if a == "-D" && j+1 < len(args) && args[j+1] == param {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing -D %s", param)
		}
	}
}

func TestBuildSandboxArgs_CommandSeparator(t *testing.T) {
	args := buildSandboxArgs("p", nil, "/bin/echo", []string{"hello"})
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		t.Fatal("missing -- separator")
	}
	if args[sepIdx+1] != "/bin/echo" {
		t.Fatalf("binary after -- = %q, want /bin/echo", args[sepIdx+1])
	}
	if args[sepIdx+2] != "hello" {
		t.Fatalf("arg after -- = %q, want hello", args[sepIdx+2])
	}
}

func TestBuildSandboxArgs_NilOriginalArgs(t *testing.T) {
	args := buildSandboxArgs("p", nil, "/bin/echo", nil)
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		t.Fatal("missing -- separator")
	}
	if sepIdx+2 != len(args) {
		t.Fatalf("expected exactly one arg after --, got %d", len(args)-sepIdx-1)
	}
}

func TestBuildSandboxArgs_EmptyWritableDirs(t *testing.T) {
	args := buildSandboxArgs("p", nil, "/bin/echo", nil)
	for _, a := range args {
		if strings.HasPrefix(a, "WRITABLE_") {
			t.Fatal("should have no WRITABLE params when dirs is empty")
		}
	}
}

func TestBuildSandboxArgs_ParamsMatchInputLength(t *testing.T) {
	// buildSandboxArgs trusts its input. No dedup/skip/cap logic —
	// that belongs to resolveWritableDirs.
	dirs := []string{"/a", "/b", "/c"}
	args := buildSandboxArgs("p", dirs, "/bin/echo", nil)
	count := 0
	for _, a := range args {
		if strings.HasPrefix(a, "WRITABLE_") {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("expected 3 WRITABLE params, got %d", count)
	}
}

func TestBuildSandboxArgs_AllDirsEmitted(t *testing.T) {
	// buildSandboxArgs trusts its input — every entry becomes a -D param.
	dirs := []string{"/a", "/b"}
	args := buildSandboxArgs("p", dirs, "/bin/echo", nil)
	count := 0
	for _, a := range args {
		if strings.HasPrefix(a, "WRITABLE_") {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 WRITABLE params, got %d", count)
	}
}

// --- resolveWritableDirs tests ---

func TestResolveWritableDirs_Basic(t *testing.T) {
	d1 := t.TempDir()
	d2 := t.TempDir()
	dirs, err := resolveWritableDirs("", []string{d1, d2})
	if err != nil {
		t.Fatalf("resolveWritableDirs: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 dirs, got %d", len(dirs))
	}
}

func TestResolveWritableDirs_EmptySkipped(t *testing.T) {
	d1 := t.TempDir()
	dirs, err := resolveWritableDirs("", []string{"", d1, ""})
	if err != nil {
		t.Fatalf("resolveWritableDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 dir, got %d", len(dirs))
	}
}

func TestResolveWritableDirs_SymlinkDedup(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	dirs, err := resolveWritableDirs("", []string{real, link})
	if err != nil {
		t.Fatalf("resolveWritableDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 dir after symlink dedup, got %d", len(dirs))
	}
	if dirs[0] != real {
		t.Fatalf("expected resolved dir %q, got %q", real, dirs[0])
	}
}

func TestResolveWritableDirs_CreatesDirectories(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "new", "nested", "dir")
	dirs, err := resolveWritableDirs("", []string{nested})
	if err != nil {
		t.Fatalf("resolveWritableDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 dir, got %d", len(dirs))
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("directory should have been created: %v", err)
	}
}

func TestResolveWritableDirs_OverlapDetection(t *testing.T) {
	project := t.TempDir()
	dirs, err := resolveWritableDirs(project, []string{project})
	if err == nil {
		t.Fatal("should reject writable dir == project dir")
	}
	if !strings.Contains(err.Error(), "contains project dir") {
		t.Fatalf("error = %q, want mention of overlap", err.Error())
	}
	_ = dirs
}

func TestResolveWritableDirs_ParentOverlapDetection(t *testing.T) {
	project := filepath.Join(t.TempDir(), "sub", "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(project)
	dirs, err := resolveWritableDirs(project, []string{parent})
	if err == nil {
		t.Fatal("should reject writable dir that is parent of project dir")
	}
	_ = dirs
}

func TestResolveWritableDirs_SymlinkOverlapDetection(t *testing.T) {
	// Create: /tmp/X/real/project (project dir) and /tmp/X/link -> /tmp/X/real (writable dir)
	base := t.TempDir()
	real := filepath.Join(base, "real")
	project := filepath.Join(real, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	// Both project and writable resolve to /tmp/X/real — should detect overlap.
	_, err := resolveWritableDirs(project, []string{link})
	if err == nil {
		t.Fatal("should detect overlap through symlink resolution")
	}
}

func TestResolveWritableDirs_SBPLMetacharacters(t *testing.T) {
	_, err := resolveWritableDirs("", []string{"/tmp/bad(dir"})
	if err == nil {
		t.Fatal("should reject SBPL metacharacters")
	}
}

func TestResolveWritableDirs_CapsAtMax(t *testing.T) {
	dirs := make([]string, maxWritableDirs+10)
	for i := range dirs {
		dirs[i] = filepath.Join(os.TempDir(), "dir"+string(rune('A'+i%26))+string(rune('0'+i/26)))
	}
	resolved, err := resolveWritableDirs("", dirs)
	if err != nil {
		t.Fatalf("resolveWritableDirs: %v", err)
	}
	if len(resolved) != maxWritableDirs {
		t.Fatalf("expected %d dirs, got %d", maxWritableDirs, len(resolved))
	}
}

func TestResolveWritableDirs_EmptyInput(t *testing.T) {
	dirs, err := resolveWritableDirs("", nil)
	if err != nil {
		t.Fatalf("resolveWritableDirs: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("expected 0 dirs, got %d", len(dirs))
	}
}

func TestResolveWritableDirs_DuplicateNonSymlinkPaths(t *testing.T) {
	d := t.TempDir()
	dirs, err := resolveWritableDirs("", []string{d, d, d})
	if err != nil {
		t.Fatalf("resolveWritableDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 dir after dedup, got %d", len(dirs))
	}
}

func TestResolveWritableDirs_WritableDirInsideProjectDir(t *testing.T) {
	// A writable dir inside the project dir is valid: the (deny file-write*)
	// blanket protects the project, and the (allow file-write*) re-allows
	// the specific subdirectory.
	project := t.TempDir()
	subdir := filepath.Join(project, "output")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs, err := resolveWritableDirs(project, []string{subdir})
	if err != nil {
		t.Fatalf("writable dir inside project dir should be accepted: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 dir, got %d", len(dirs))
	}
}

// --- Prepare tests ---

func TestPrepare_WrapsCommand(t *testing.T) {
	project := t.TempDir()
	writable := t.TempDir()
	cmd := exec.Command("echo", "hello")
	cfg := Config{ProjectDir: project, WritableDirs: []string{writable}, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if cmd.Path != sandboxExecLocator() {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, sandboxExecLocator())
	}
	if len(cmd.Args) < 3 || cmd.Args[1] != "-p" {
		t.Fatalf("cmd.Args missing -p flag: %v", cmd.Args)
	}
}

func TestPrepare_SavesOriginalArgs(t *testing.T) {
	project := t.TempDir()
	cmd := exec.Command("echo", "hello", "world")
	cfg := Config{ProjectDir: project, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	// Original binary + args should appear after -- separator.
	sepIdx := -1
	for i, a := range cmd.Args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		t.Fatal("missing -- separator in cmd.Args")
	}
	if cmd.Args[sepIdx+1] != "echo" {
		t.Fatalf("original binary = %q, want echo", cmd.Args[sepIdx+1])
	}
	if cmd.Args[sepIdx+2] != "hello" || cmd.Args[sepIdx+3] != "world" {
		t.Fatalf("original args = %v", cmd.Args[sepIdx+1:])
	}
}

func TestPrepare_NilArgsHandled(t *testing.T) {
	project := t.TempDir()
	cmd := exec.Command("echo")
	cmd.Args = nil // edge case
	cfg := Config{ProjectDir: project, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare with nil Args: %v", err)
	}
}

func TestPrepare_OverlapValidation_Equal(t *testing.T) {
	project := t.TempDir()
	writable := project // same dir
	cmd := exec.Command("echo")
	cfg := Config{ProjectDir: project, WritableDirs: []string{writable}, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err == nil {
		t.Fatal("prepare should error when writable dir == project dir")
	}
	if !strings.Contains(err.Error(), "contains project dir") {
		t.Fatalf("error = %q, want mention of overlap", err.Error())
	}
}

func TestPrepare_OverlapValidation_Parent(t *testing.T) {
	project := filepath.Join(t.TempDir(), "sub", "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	parentOfProject := filepath.Dir(project)
	cmd := exec.Command("echo")
	cfg := Config{ProjectDir: project, WritableDirs: []string{parentOfProject}, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err == nil {
		t.Fatal("prepare should error when writable dir is a parent of project dir")
	}
	if !strings.Contains(err.Error(), "contains project dir") {
		t.Fatalf("error = %q, want mention of overlap", err.Error())
	}
}

func TestPrepare_SBPLMetacharacters(t *testing.T) {
	cases := []string{
		"/tmp/proj(ect",
		"/tmp/proj)ect",
		`/tmp/proj"ect`,
		"/tmp/proj\\ect",
		"/tmp/proj\nect",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			cmd := exec.Command("echo")
			cfg := Config{ProjectDir: c, Mode: ModeOn}
			_, err := prepare(cmd, cfg)
			if err == nil {
				t.Fatalf("prepare should reject path with metachar: %q", c)
			}
		})
	}
}

func TestPrepare_EmptyProjectDir(t *testing.T) {
	writable := t.TempDir()
	cmd := exec.Command("echo")
	cfg := Config{ProjectDir: "", WritableDirs: []string{writable}, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare with empty ProjectDir: %v", err)
	}
	// Verify no PROJECT_DIR param (never used in profile).
	for _, a := range cmd.Args {
		if strings.HasPrefix(a, "PROJECT_DIR=") {
			t.Fatal("should not have PROJECT_DIR param")
		}
	}
}

func TestPrepare_CreatesWritableDirs(t *testing.T) {
	project := t.TempDir()
	writable := filepath.Join(project, "new-nested", "dir")
	cmd := exec.Command("echo")
	cfg := Config{ProjectDir: project, WritableDirs: []string{writable}, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	// The dir should have been created.
	if _, err := os.Stat(writable); err != nil {
		t.Fatalf("writable dir not created: %v", err)
	}
}

func TestPrepare_ResolvesSymlinks(t *testing.T) {
	// Create a temp dir and a symlink to it.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	cmd := exec.Command("echo")
	cfg := Config{ProjectDir: link, Mode: ModeOn}

	_, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	// PROJECT_DIR is no longer passed as a param (project dir is protected
	// by the (deny file-write*) rule). Verify the command was still wrapped.
	if cmd.Path != sandboxExecLocator() {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, sandboxExecLocator())
	}
}

func TestPrepare_SBPLMetacharactersWritableDir(t *testing.T) {
	project := t.TempDir()
	cmd := exec.Command("echo")
	cfg := Config{
		ProjectDir:   project,
		WritableDirs: []string{"/tmp/bad(dir"},
		Mode:         ModeOn,
	}
	_, err := prepare(cmd, cfg)
	if err == nil {
		t.Fatal("prepare should reject writable dir with SBPL metacharacters")
	}
}

// --- Available / PostStart tests ---

func TestAvailable_Present(t *testing.T) {
	if sandboxExecLocator() == "" {
		t.Skip("sandbox-exec not found")
	}
	if !Available() {
		t.Fatal("Available() should return true when sandbox-exec exists")
	}
}

// Tests that mutate sandboxExecLocator must NOT use t.Parallel() —
// they share process-global state.
func TestAvailable_Missing(t *testing.T) {
	orig := sandboxExecLocator
	sandboxExecLocator = func() string { return "" }
	defer func() { sandboxExecLocator = orig }()

	if Available() {
		t.Fatal("Available() should return false when locator returns empty")
	}
}

func TestPrepare_NilCmd_Darwin(t *testing.T) {
	_, err := Prepare(nil, Config{Mode: ModeOn, ProjectDir: t.TempDir()})
	if err == nil {
		t.Fatal("Prepare(nil, ModeOn) should error")
	}
}

func TestPostStart_NoOp(t *testing.T) {
	cmd := exec.Command("echo")
	cleanup, err := postStart(cmd, Config{})
	if err != nil {
		t.Fatalf("postStart: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup")
	}
	cleanup()
}

// --- validateSBPLPath tests ---

func TestValidateSBPLPath_Clean(t *testing.T) {
	if err := validateSBPLPath("/tmp/project"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSBPLPath_Metachars(t *testing.T) {
	cases := []struct {
		path string
		name string
	}{
		{"/tmp/proj(ect", "open-paren"},
		{"/tmp/proj)ect", "close-paren"},
		{`/tmp/proj"ect`, "double-quote"},
		{"/tmp/proj\\ect", "backslash"},
		{"/tmp/proj\nect", "newline"},
		{"/tmp/proj\r\nect", "crlf"},
		{"/tmp/proj;ect", "semicolon"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validateSBPLPath(c.path); err == nil {
				t.Fatalf("expected error for %q", c.path)
			}
		})
	}
}

// --- pathContains tests ---

func TestPathContains(t *testing.T) {
	cases := []struct {
		parent, child string
		want          bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/bc", false},
		{"/a/b", "/a", false},
	}
	for _, c := range cases {
		t.Run(c.parent+"_"+c.child, func(t *testing.T) {
			if got := pathContains(c.parent, c.child); got != c.want {
				t.Fatalf("pathContains(%q, %q) = %v, want %v", c.parent, c.child, got, c.want)
			}
		})
	}
}
