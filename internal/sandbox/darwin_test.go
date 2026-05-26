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

func TestBuildSeatbeltProfile_AllowsProcessExec(t *testing.T) {
	profile := buildSeatbeltProfile(nil)
	if !strings.Contains(profile, "(allow process-exec)") {
		t.Fatal("profile missing (allow process-exec)")
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

func TestBuildSeatbeltProfile_CapsAtMax(t *testing.T) {
	dirs := make([]string, maxWritableDirs+10)
	for i := range dirs {
		dirs[i] = "/tmp/dir" + string(rune('A'+i%26)) + string(rune('0'+i/26))
	}
	profile := buildSeatbeltProfile(dirs)
	// WRITABLE entries appear in one block (file-write* re-allow only;
	// file-link is denied entirely with no re-allow).
	count := strings.Count(profile, "WRITABLE_")
	if count != maxWritableDirs {
		t.Fatalf("profile has %d WRITABLE entries, want exactly %d", count, maxWritableDirs)
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

// --- Argument construction tests ---

func TestBuildSandboxArgs_BinaryPath(t *testing.T) {
	args := buildSandboxArgs("profile", nil, "/bin/echo", nil)
	if args[0] != sandboxExecPath {
		t.Fatalf("first arg = %q, want %q", args[0], sandboxExecPath)
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

func TestBuildSandboxArgs_DeduplicateWritableDirs(t *testing.T) {
	// /tmp is a symlink to /private/tmp on macOS.
	dirs := []string{"/tmp", "/private/tmp"}
	args := buildSandboxArgs("p", dirs, "/bin/echo", nil)
	count := 0
	for _, a := range args {
		if strings.HasPrefix(a, "WRITABLE_") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 WRITABLE param after dedup, got %d", count)
	}
}

func TestBuildSandboxArgs_MaxWritableDirs(t *testing.T) {
	dirs := make([]string, maxWritableDirs+10)
	for i := range dirs {
		dirs[i] = filepath.Join(os.TempDir(), "dir"+string(rune('A'+i%26))+string(rune('0'+i/26)))
	}
	args := buildSandboxArgs("p", dirs, "/bin/echo", nil)
	count := 0
	for _, a := range args {
		if strings.HasPrefix(a, "WRITABLE_") {
			count++
		}
	}
	if count != maxWritableDirs {
		t.Fatalf("expected exactly %d WRITABLE params, got %d", maxWritableDirs, count)
	}
}

func TestBuildSandboxArgs_EmptyStringSkipped(t *testing.T) {
	dirs := []string{"", "/tmp/w", ""}
	args := buildSandboxArgs("p", dirs, "/bin/echo", nil)
	count := 0
	for _, a := range args {
		if strings.HasPrefix(a, "WRITABLE_") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 WRITABLE param (empty strings skipped), got %d", count)
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
	if cmd.Path != sandboxExecPath {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, sandboxExecPath)
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
	if cmd.Path != sandboxExecPath {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, sandboxExecPath)
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
	// On macOS, /usr/bin/sandbox-exec should exist.
	if _, err := os.Stat(sandboxExecPath); err != nil {
		t.Skipf("sandbox-exec not found: %v", err)
	}
	if !Available() {
		t.Fatal("Available() should return true when sandbox-exec exists")
	}
}

func TestAvailable_Missing(t *testing.T) {
	orig := sandboxExecPath
	sandboxExecPath = "/nonexistent/sandbox-exec"
	defer func() { sandboxExecPath = orig }()

	if Available() {
		t.Fatal("Available() should return false for nonexistent path")
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
