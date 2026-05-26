//go:build linux

// Tests that mutate package-level vars (usernsClonePath, maxUserNamespacesPath,
// procVersionPath) must NOT use t.Parallel() — they share process-global state.
package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- buildBwrapArgs tests ---

func TestBuildBwrapArgs_ReadOnlyRoot(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	if !containsSequence(args, "--ro-bind", "/", "/") {
		t.Errorf("expected --ro-bind / / in args: %v", args)
	}
}

func TestBuildBwrapArgs_DevMount(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	if !containsSequence(args, "--dev", "/dev") {
		t.Errorf("expected --dev /dev in args: %v", args)
	}
}

func TestBuildBwrapArgs_ProcMount(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	if !containsSequence(args, "--proc", "/proc") {
		t.Errorf("expected --proc /proc in args: %v", args)
	}
}

func TestBuildBwrapArgs_TmpfsWithSize(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	if !containsSequence(args, "--tmpfs", "/tmp", "--size", "512M") {
		t.Errorf("expected --tmpfs /tmp --size 512M in args: %v", args)
	}
}

func TestBuildBwrapArgs_VarTmpSymlink(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	if !containsSequence(args, "--symlink", "/tmp", "/var/tmp") {
		t.Errorf("expected --symlink /tmp /var/tmp in args: %v", args)
	}
}

func TestBuildBwrapArgs_NewSession(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	if !containsFlag(args, "--new-session") {
		t.Errorf("expected --new-session in args: %v", args)
	}
}

func TestBuildBwrapArgs_DieWithParent(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	if !containsFlag(args, "--die-with-parent") {
		t.Errorf("expected --die-with-parent in args: %v", args)
	}
}

func TestBuildBwrapArgs_UnshareUser(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	if !containsFlag(args, "--unshare-user") {
		t.Errorf("expected --unshare-user in args: %v", args)
	}
}

func TestBuildBwrapArgs_UnsharePid(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	if !containsFlag(args, "--unshare-pid") {
		t.Errorf("expected --unshare-pid in args: %v", args)
	}
}

func TestBuildBwrapArgs_NoNetworkIsolation(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	if containsFlag(args, "--unshare-net") {
		t.Errorf("--unshare-net should not be present (breaks CLI providers): %v", args)
	}
}

func TestBuildBwrapArgs_WritableDirs(t *testing.T) {
	cfg := Config{WritableDirs: []string{"/tmp/out", "/home/user/.provider"}}
	args := buildBwrapArgs(cfg, "/project", "/bin/ls", nil)
	if !containsSequence(args, "--bind", "/tmp/out", "/tmp/out") {
		t.Errorf("expected --bind /tmp/out in args: %v", args)
	}
	if !containsSequence(args, "--bind", "/home/user/.provider", "/home/user/.provider") {
		t.Errorf("expected --bind /home/user/.provider in args: %v", args)
	}
}

func TestBuildBwrapArgs_EmptyWritableDirs(t *testing.T) {
	args := buildBwrapArgs(Config{WritableDirs: nil}, "/project", "/bin/ls", nil)
	for i, a := range args {
		if a == "--bind" {
			t.Errorf("unexpected --bind at index %d with empty writable dirs: %v", i, args)
		}
	}
}

func TestBuildBwrapArgs_DeduplicateWritableDirs(t *testing.T) {
	dir := t.TempDir()
	// Both entries resolve to the same directory.
	cfg := Config{WritableDirs: []string{dir, dir + "/"}}
	args := buildBwrapArgs(cfg, "/project", "/bin/ls", nil)
	bindCount := 0
	for _, a := range args {
		if a == "--bind" {
			bindCount++
		}
	}
	if bindCount != 1 {
		t.Errorf("expected 1 --bind for deduplicated dir, got %d: %v", bindCount, args)
	}
}

func TestBuildBwrapArgs_Chdir(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/resolved/project", "/bin/ls", nil)
	if !containsSequence(args, "--chdir", "/resolved/project") {
		t.Errorf("expected --chdir /resolved/project in args: %v", args)
	}
}

func TestBuildBwrapArgs_CommandSeparator(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/usr/bin/claude", []string{"--flag", "val"})
	// Find the -- separator.
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		t.Fatalf("expected -- separator in args: %v", args)
	}
	// After --: original binary + original args.
	rest := args[sepIdx+1:]
	if len(rest) != 3 || rest[0] != "/usr/bin/claude" || rest[1] != "--flag" || rest[2] != "val" {
		t.Errorf("after --: got %v, want [/usr/bin/claude --flag val]", rest)
	}
}

func TestBuildBwrapArgs_NilOriginalArgs(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", "/bin/ls", nil)
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		t.Fatal("expected -- separator")
	}
	rest := args[sepIdx+1:]
	if len(rest) != 1 || rest[0] != "/bin/ls" {
		t.Errorf("after --: got %v, want [/bin/ls]", rest)
	}
}

func TestBuildBwrapArgs_EmptyStringSkipped(t *testing.T) {
	// Empty string in WritableDirs must be skipped — it would otherwise
	// silently resolve to CWD via filepath.Abs, making the entire
	// working directory writable inside the sandbox.
	cfg := Config{WritableDirs: []string{"", "/valid"}}
	args := buildBwrapArgs(cfg, "/project", "/bin/ls", nil)
	if !containsSequence(args, "--bind", "/valid", "/valid") {
		t.Errorf("expected --bind /valid for resolvable dir: %v", args)
	}
	// Count --bind flags — should be exactly 1 (for /valid), not 2.
	bindCount := 0
	for _, a := range args {
		if a == "--bind" {
			bindCount++
		}
	}
	if bindCount != 1 {
		t.Errorf("expected 1 --bind (empty string should be skipped), got %d: %v", bindCount, args)
	}
}

func TestBuildBwrapArgs_MaxWritableDirs(t *testing.T) {
	dirs := make([]string, maxWritableDirs+10)
	for i := range dirs {
		dirs[i] = filepath.Join(os.TempDir(), "bwrap-test-dirs", string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	cfg := Config{WritableDirs: dirs}
	args := buildBwrapArgs(cfg, "/project", "/bin/ls", nil)
	bindCount := 0
	for _, a := range args {
		if a == "--bind" {
			bindCount++
		}
	}
	if bindCount > maxWritableDirs {
		t.Errorf("expected at most %d --bind flags, got %d", maxWritableDirs, bindCount)
	}
}

// --- checkUserNamespacesEnabled tests ---

func TestCheckUserNamespacesEnabled_Default(t *testing.T) {
	// When sysctl files don't exist (or aren't readable), should return true.
	old := usernsClonePath
	old2 := maxUserNamespacesPath
	usernsClonePath = "/nonexistent/path/1"
	maxUserNamespacesPath = "/nonexistent/path/2"
	defer func() {
		usernsClonePath = old
		maxUserNamespacesPath = old2
	}()
	if !checkUserNamespacesEnabled() {
		t.Error("expected true when sysctl files are absent")
	}
}

func TestCheckUserNamespacesEnabled_Disabled(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "unprivileged_user_ns_clone")
	if err := os.WriteFile(f, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := usernsClonePath
	usernsClonePath = f
	defer func() { usernsClonePath = old }()

	if checkUserNamespacesEnabled() {
		t.Error("expected false when userns_clone is 0")
	}
}

func TestCheckUserNamespacesEnabled_MaxUserNamespacesZero(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "max_user_namespaces")
	if err := os.WriteFile(f, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := maxUserNamespacesPath
	maxUserNamespacesPath = f
	defer func() { maxUserNamespacesPath = old }()

	if checkUserNamespacesEnabled() {
		t.Error("expected false when max_user_namespaces is 0")
	}
}

// --- checkWSL1 tests ---

func TestCheckWSL1_Positive(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "version")
	if err := os.WriteFile(f, []byte("Linux version 4.4.0-18362-Microsoft (Microsoft@Microsoft.com)"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := procVersionPath
	procVersionPath = f
	defer func() { procVersionPath = old }()

	if !checkWSL1() {
		t.Error("expected true for WSL1 /proc/version")
	}
}

func TestCheckWSL1_Negative(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "version")
	if err := os.WriteFile(f, []byte("Linux version 5.10.16.3-microsoft-standard-WSL2 (root@...)"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := procVersionPath
	procVersionPath = f
	defer func() { procVersionPath = old }()

	if checkWSL1() {
		t.Error("expected false for WSL2 /proc/version")
	}
}

func TestCheckWSL1_RegularLinux(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "version")
	if err := os.WriteFile(f, []byte("Linux version 6.1.0-generic (buildd@lcy02-amd64-045)"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := procVersionPath
	procVersionPath = f
	defer func() { procVersionPath = old }()

	if checkWSL1() {
		t.Error("expected false for regular Linux /proc/version")
	}
}

func TestCheckWSL1_UnreadableFile(t *testing.T) {
	old := procVersionPath
	procVersionPath = "/nonexistent/path/version"
	defer func() { procVersionPath = old }()

	if checkWSL1() {
		t.Error("expected false when /proc/version is unreadable")
	}
}

// --- prepare tests ---

func TestPrepare_WrapsCommand(t *testing.T) {
	if bwrapPath() == "" {
		t.Skip("bwrap not in PATH")
	}
	if !userNamespacesEnabled() {
		t.Skip("user namespaces disabled")
	}

	projectDir := t.TempDir()
	cmd := exec.Command("echo", "hello")
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: []string{t.TempDir()},
		Mode:         ModeAuto,
	}

	cleanup, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer cleanup()

	if cmd.Path != bwrapPath() {
		t.Errorf("cmd.Path = %q, want bwrap path %q", cmd.Path, bwrapPath())
	}
	if len(cmd.Args) < 2 {
		t.Fatalf("cmd.Args too short: %v", cmd.Args)
	}
	if cmd.Args[0] != bwrapPath() {
		t.Errorf("cmd.Args[0] = %q, want %q", cmd.Args[0], bwrapPath())
	}
	// Original binary should appear after -- separator.
	found := false
	for _, a := range cmd.Args {
		if a == "echo" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("original binary 'echo' not found in args: %v", cmd.Args)
	}
}

func TestPrepare_CreatesWritableDirs(t *testing.T) {
	if bwrapPath() == "" {
		t.Skip("bwrap not in PATH")
	}

	projectDir := t.TempDir()
	writableDir := filepath.Join(projectDir, "nested", "writable")
	cmd := exec.Command("echo", "hello")
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: []string{writableDir},
		Mode:         ModeAuto,
	}

	cleanup, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer cleanup()

	info, err := os.Stat(writableDir)
	if err != nil {
		t.Fatalf("writable dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("writable dir is not a directory: %v", writableDir)
	}
}

func TestPrepare_SavesOriginalArgs(t *testing.T) {
	if bwrapPath() == "" {
		t.Skip("bwrap not in PATH")
	}

	projectDir := t.TempDir()
	cmd := exec.Command("/usr/bin/claude", "--model", "sonnet", "--verbose")
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: nil,
		Mode:         ModeAuto,
	}

	cleanup, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer cleanup()

	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "/usr/bin/claude") {
		t.Errorf("original binary not preserved in args: %v", cmd.Args)
	}
	if !strings.Contains(joined, "--model") {
		t.Errorf("original args not preserved: %v", cmd.Args)
	}
}

func TestPrepare_NilArgsHandled(t *testing.T) {
	if bwrapPath() == "" {
		t.Skip("bwrap not in PATH")
	}

	projectDir := t.TempDir()
	cmd := exec.Command("echo")
	cmd.Args = nil // Simulate manual construction.
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: nil,
		Mode:         ModeAuto,
	}

	cleanup, err := prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("prepare with nil Args: %v", err)
	}
	defer cleanup()

	// Should still have bwrap args + -- + binary.
	if len(cmd.Args) < 2 {
		t.Errorf("cmd.Args too short after prepare: %v", cmd.Args)
	}
}

// --- helpers ---

// containsFlag checks whether the args slice contains the given flag.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// containsSequence checks whether args contains the given subsequence
// in order (not necessarily contiguous).
func containsSequence(args []string, seq ...string) bool {
	if len(seq) == 0 {
		return true
	}
	j := 0
	for _, a := range args {
		if a == seq[j] {
			j++
			if j == len(seq) {
				return true
			}
		}
	}
	return false
}
