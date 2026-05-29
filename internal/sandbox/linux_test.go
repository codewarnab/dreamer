//go:build linux

// Tests that mutate package-level vars (usernsClonePath, maxUserNamespacesPath,
// procVersionPath, readFile) must NOT use t.Parallel() — they share
// process-global state.
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// --- buildBwrapArgs tests ---

func TestBuildBwrapArgs_ReadOnlyRoot(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if !containsContiguousSequence(args, "--ro-bind", "/", "/") {
		t.Errorf("expected --ro-bind / / in args: %v", args)
	}
}

func TestBuildBwrapArgs_DevMount(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if !containsContiguousSequence(args, "--dev", "/dev") {
		t.Errorf("expected --dev /dev in args: %v", args)
	}
}

func TestBuildBwrapArgs_ProcMount(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if !containsContiguousSequence(args, "--proc", "/proc") {
		t.Errorf("expected --proc /proc in args: %v", args)
	}
}

func TestBuildBwrapArgs_TmpfsWithSize(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	// --size <bytes> must immediately precede --tmpfs /tmp.
	// bwrap consumes next_size_arg from --size when it hits --tmpfs.
	expectedBytes := strconv.Itoa(tmpfsSizeBytes)
	if !containsContiguousSequence(args, "--size", expectedBytes, "--tmpfs", "/tmp") {
		t.Errorf("expected --size %s --tmpfs /tmp (contiguous) in args: %v", expectedBytes, args)
	}
}

func TestBuildBwrapArgs_TmpfsSizeBytesValue(t *testing.T) {
	// Guard against someone accidentally changing the value to bytes
	// instead of MiB (e.g. 512 instead of 536870912).
	if tmpfsSizeBytes != 536870912 {
		t.Errorf("tmpfsSizeBytes = %d, want 536870912 (512 MiB)", tmpfsSizeBytes)
	}
}

func TestBuildBwrapArgs_VarTmpSymlink(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if !containsContiguousSequence(args, "--symlink", "/tmp", "/var/tmp") {
		t.Errorf("expected --symlink /tmp /var/tmp in args: %v", args)
	}
}

func TestBuildBwrapArgs_NewSession(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if !containsFlag(args, "--new-session") {
		t.Errorf("expected --new-session in args: %v", args)
	}
}

func TestBuildBwrapArgs_DieWithParent(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if !containsFlag(args, "--die-with-parent") {
		t.Errorf("expected --die-with-parent in args: %v", args)
	}
}

func TestBuildBwrapArgs_UnshareUser(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if !containsFlag(args, "--unshare-user") {
		t.Errorf("expected --unshare-user in args: %v", args)
	}
}

func TestBuildBwrapArgs_UnsharePid(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if !containsFlag(args, "--unshare-pid") {
		t.Errorf("expected --unshare-pid in args: %v", args)
	}
}

func TestBuildBwrapArgs_NetworkOpenByDefault(t *testing.T) {
	// Default is NetworkOpen (isolation is opt-in) because network
	// isolation breaks CLI providers that need to reach model backends.
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if containsFlag(args, "--unshare-net") {
		t.Errorf("--unshare-net should not be present with default config (NetworkOpen): %v", args)
	}
}

func TestBuildBwrapArgs_NetworkIsolated(t *testing.T) {
	args := buildBwrapArgs(Config{Network: NetworkIsolated}, "/project", nil, "/bin/ls", nil, 0, true)
	if !containsFlag(args, "--unshare-net") {
		t.Errorf("expected --unshare-net when Network=isolated: %v", args)
	}
}

func TestBuildBwrapArgs_WritableDirs(t *testing.T) {
	resolved := []string{"/home/user/.provider"}
	args := buildBwrapArgs(Config{}, "/project", resolved, "/bin/ls", nil, 0, true)
	if !containsContiguousSequence(args, "--bind", "/home/user/.provider", "/home/user/.provider") {
		t.Errorf("expected --bind /home/user/.provider in args: %v", args)
	}
}

func TestBuildBwrapArgs_EmptyWritableDirs(t *testing.T) {
	args := buildBwrapArgs(Config{WritableDirs: nil}, "/project", nil, "/bin/ls", nil, 0, true)
	for i, a := range args {
		if a == "--bind" {
			t.Errorf("unexpected --bind at index %d with empty writable dirs: %v", i, args)
		}
	}
}

func TestBuildBwrapArgs_DeduplicateWritableDirs(t *testing.T) {
	dir := t.TempDir()
	// Both entries resolve to the same directory.
	resolved := []string{dir, dir}
	args := buildBwrapArgs(Config{}, "/project", resolved, "/bin/ls", nil, 0, true)
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
	args := buildBwrapArgs(Config{}, "/resolved/project", nil, "/bin/ls", nil, 0, false)
	if !containsContiguousSequence(args, "--chdir", "/resolved/project") {
		t.Errorf("expected --chdir /resolved/project in args: %v", args)
	}
}

func TestBuildBwrapArgs_CommandSeparator(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/usr/bin/claude", []string{"--flag", "val"}, 0, true)
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
	rest := args[sepIdx+1:]
	if len(rest) != 3 || rest[0] != "/usr/bin/claude" || rest[1] != "--flag" || rest[2] != "val" {
		t.Errorf("after --: got %v, want [/usr/bin/claude --flag val]", rest)
	}
}

func TestBuildBwrapArgs_NilOriginalArgs(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
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

func TestBuildBwrapArgs_MaxWritableDirs(t *testing.T) {
	dirs := make([]string, maxWritableDirs+10)
	for i := range dirs {
		dirs[i] = filepath.Join("/home/user", "bwrap-test-dirs", string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	args := buildBwrapArgs(Config{}, "/project", dirs, "/bin/ls", nil, 0, false)
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

// --- /tmp and /var/tmp skip tests ---

func TestBuildBwrapArgs_SkipsTmpBind(t *testing.T) {
	// /tmp in resolvedDirs should be skipped since --tmpfs /tmp covers it.
	dirs := []string{"/tmp", "/home/user/.provider"}
	args := buildBwrapArgs(Config{}, "/project", dirs, "/bin/ls", nil, 0, false)
	bindCount := 0
	for _, a := range args {
		if a == "--bind" {
			bindCount++
		}
	}
	if bindCount != 1 {
		t.Errorf("expected 1 --bind (/tmp should be skipped), got %d: %v", bindCount, args)
	}
}

func TestBuildBwrapArgs_SkipsTmpSubdirBind(t *testing.T) {
	dirs := []string{"/tmp/some-provider-cache"}
	args := buildBwrapArgs(Config{}, "/project", dirs, "/bin/ls", nil, 0, false)
	for i, a := range args {
		if a == "--bind" {
			t.Errorf("unexpected --bind at index %d (path under /tmp should be skipped): %v", i, args)
		}
	}
}

func TestBuildBwrapArgs_SkipsVarTmpBind(t *testing.T) {
	// /var/tmp is covered by --symlink /tmp /var/tmp, so it must be skipped.
	dirs := []string{"/var/tmp", "/home/user/.provider"}
	args := buildBwrapArgs(Config{}, "/project", dirs, "/bin/ls", nil, 0, false)
	for i, a := range args {
		if a == "--bind" && i+1 < len(args) && (args[i+1] == "/var/tmp" || strings.HasPrefix(args[i+1], "/var/tmp/")) {
			t.Errorf("unexpected --bind for /var/tmp at index %d: %v", i, args)
		}
	}
}

// --- isSubpath tests ---

func TestIsSubpath_Equal(t *testing.T) {
	if !isSubpath("/a/b", "/a/b") {
		t.Error("expected true for equal paths")
	}
}

func TestIsSubpath_Child(t *testing.T) {
	if !isSubpath("/a/b/c", "/a/b") {
		t.Error("expected true for child path")
	}
}

func TestIsSubpath_NotChild(t *testing.T) {
	if isSubpath("/a/x", "/a/b") {
		t.Error("expected false for non-child path")
	}
}

func TestIsSubpath_PrefixCollision(t *testing.T) {
	// /a/bc should NOT be a child of /a/b
	if isSubpath("/a/bc", "/a/b") {
		t.Error("expected false for prefix collision")
	}
}

func TestIsSubpath_TrailingSlash(t *testing.T) {
	// filepath.Clean normalizes trailing slashes.
	if !isSubpath("/a/b", "/a/b/") {
		t.Error("expected true when parent has trailing slash")
	}
	if !isSubpath("/a/b/", "/a/b") {
		t.Error("expected true when child has trailing slash")
	}
}

func TestIsSubpath_RedundantSeparator(t *testing.T) {
	if !isSubpath("/a//b/c", "/a/b") {
		t.Error("expected true for child with double slash (normalized by Clean)")
	}
}

// --- isAncestor tests ---

func TestIsAncestor_Strict(t *testing.T) {
	if !isAncestor("/a", "/a/b") {
		t.Error("expected true for strict ancestor")
	}
}

func TestIsAncestor_Equal(t *testing.T) {
	if isAncestor("/a/b", "/a/b") {
		t.Error("expected false for equal paths (not a strict ancestor)")
	}
}

func TestIsAncestor_NotAncestor(t *testing.T) {
	if isAncestor("/a/b", "/a/x") {
		t.Error("expected false for non-ancestor")
	}
}

func TestIsAncestor_TrailingSlash(t *testing.T) {
	if !isAncestor("/a/", "/a/b") {
		t.Error("expected true when ancestor has trailing slash")
	}
}

// --- isTmpfsPath tests ---

func TestIsTmpfsPath_Tmp(t *testing.T) {
	if !isTmpfsPath("/tmp") {
		t.Error("expected /tmp to be a tmpfs path")
	}
}

func TestIsTmpfsPath_TmpSubdir(t *testing.T) {
	if !isTmpfsPath("/tmp/cache") {
		t.Error("expected /tmp/cache to be a tmpfs path")
	}
}

func TestIsTmpfsPath_VarTmp(t *testing.T) {
	if !isTmpfsPath("/var/tmp") {
		t.Error("expected /var/tmp to be a tmpfs path")
	}
}

func TestIsTmpfsPath_VarTmpSubdir(t *testing.T) {
	if !isTmpfsPath("/var/tmp/cache") {
		t.Error("expected /var/tmp/cache to be a tmpfs path")
	}
}

func TestIsTmpfsPath_NotTmpfs(t *testing.T) {
	if isTmpfsPath("/home/user/.provider") {
		t.Error("expected /home/user/.provider to NOT be a tmpfs path")
	}
}

func TestIsTmpfsPath_PrefixCollision(t *testing.T) {
	if isTmpfsPath("/tmpdata") {
		t.Error("expected /tmpdata to NOT be a tmpfs path (prefix collision)")
	}
}

// --- validateWritableDir tests ---

func TestValidateWritableDir_RejectsRoot(t *testing.T) {
	err := validateWritableDir("/", "/project", []string{"/home"}, false)
	if err == nil {
		t.Error("expected error for root path")
	}
	if !strings.Contains(err.Error(), "refusing to mount") {
		t.Errorf("error should mention 'refusing to mount', got: %v", err)
	}
}

func TestValidateWritableDir_RejectsDotDot(t *testing.T) {
	err := validateWritableDir("/home/user/../etc", "/project", []string{"/tmp"}, false)
	if err == nil {
		t.Error("expected error for path with ..")
	}
	if !strings.Contains(err.Error(), "'..'") {
		t.Errorf("error should mention '..', got: %v", err)
	}
}

func TestValidateWritableDir_RejectsAncestorOfProject(t *testing.T) {
	err := validateWritableDir("/home/user", "/home/user/project", []string{"/home/user"}, false)
	if err == nil {
		t.Error("expected error when writable dir is ancestor of project dir")
	}
	if !strings.Contains(err.Error(), "overlaps with project dir") {
		t.Errorf("error should mention 'overlaps with project dir', got: %v", err)
	}
}

func TestValidateWritableDir_RejectsProjectDirItself(t *testing.T) {
	// Passing projectDir itself as writable defeats the deny-write ACL.
	err := validateWritableDir("/home/user/project", "/home/user/project", []string{"/home/user/project"}, false)
	if err == nil {
		t.Error("expected error when writable dir equals project dir")
	}
	if !strings.Contains(err.Error(), "overlaps with project dir") {
		t.Errorf("error should mention 'overlaps with project dir', got: %v", err)
	}
}

func TestValidateWritableDir_RejectsOutsideAllowedRoots(t *testing.T) {
	err := validateWritableDir("/etc/something", "/project", []string{"/tmp", "/home"}, false)
	if err == nil {
		t.Error("expected error for path outside allowed roots")
	}
	if !strings.Contains(err.Error(), "not under any allowed root") {
		t.Errorf("error should mention 'not under any allowed root', got: %v", err)
	}
}

func TestValidateWritableDir_AllowsSubpath(t *testing.T) {
	err := validateWritableDir("/home/user/.claude", "/project", []string{"/home"}, false)
	if err != nil {
		t.Errorf("expected no error for valid subpath, got: %v", err)
	}
}

func TestValidateWritableDir_AllowsSubpathOfProjectDir(t *testing.T) {
	// allowedRoots should use the project dir itself, not the subdir,
	// to match production behavior (resolveAndValidateWritableDirs
	// passes projectDir as an allowed root).
	err := validateWritableDir("/home/user/project/output", "/home/user/project", []string{"/home/user/project"}, false)
	if err != nil {
		t.Errorf("expected no error for subdir of project dir, got: %v", err)
	}
}

func TestValidateWritableDir_AllowsEqualPath(t *testing.T) {
	err := validateWritableDir("/tmp", "/project", []string{"/tmp"}, false)
	if err != nil {
		t.Errorf("expected no error for equal path, got: %v", err)
	}
}

// --- checkUserNamespacesEnabled tests ---

func TestCheckUserNamespacesEnabled_Default(t *testing.T) {
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

// --- userNamespacesEnabled tests (public API, no caching) ---

func TestUserNamespacesEnabled_ReflectsVarChange(t *testing.T) {
	old := usernsClonePath
	old2 := maxUserNamespacesPath
	defer func() {
		usernsClonePath = old
		maxUserNamespacesPath = old2
	}()

	// Set paths to nonexistent files — should return true.
	usernsClonePath = "/nonexistent/path/1"
	maxUserNamespacesPath = "/nonexistent/path/2"
	if !userNamespacesEnabled() {
		t.Error("expected true when sysctl files are absent")
	}

	// Now disable via userns_clone = 0.
	dir := t.TempDir()
	f := filepath.Join(dir, "unprivileged_user_ns_clone")
	if err := os.WriteFile(f, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	usernsClonePath = f
	if userNamespacesEnabled() {
		t.Error("expected false after injecting userns_clone=0")
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

// --- isWSL1 tests (public API, no caching) ---

func TestIsWSL1_ReflectsVarChange(t *testing.T) {
	old := procVersionPath
	defer func() { procVersionPath = old }()

	dir := t.TempDir()

	// Regular Linux first.
	f := filepath.Join(dir, "version_linux")
	if err := os.WriteFile(f, []byte("Linux version 6.1.0-generic"), 0o644); err != nil {
		t.Fatal(err)
	}
	procVersionPath = f
	if isWSL1() {
		t.Error("expected false for regular Linux")
	}

	// Now switch to WSL1.
	f2 := filepath.Join(dir, "version_wsl1")
	if err := os.WriteFile(f2, []byte("Linux version 4.4.0-18362-Microsoft (Microsoft@Microsoft.com)"), 0o644); err != nil {
		t.Fatal(err)
	}
	procVersionPath = f2
	if !isWSL1() {
		t.Error("expected true for WSL1 after var change")
	}
}

// --- Available() tests via var injection (now possible without sync.Once) ---

func TestAvailable_DisabledByUserNS(t *testing.T) {
	old := maxUserNamespacesPath
	maxUserNamespacesPath = "/nonexistent/init" // ensure no early exit
	dir := t.TempDir()
	f := filepath.Join(dir, "max_user_namespaces")
	if err := os.WriteFile(f, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	maxUserNamespacesPath = f
	defer func() { maxUserNamespacesPath = old }()

	if userNamespacesEnabled() {
		t.Skip("user namespaces still enabled after injection — can't test Available()")
	}
	if Available() {
		t.Error("Available() should return false when user namespaces are disabled")
	}
}

func TestAvailable_DisabledByWSL1(t *testing.T) {
	old := procVersionPath
	dir := t.TempDir()
	f := filepath.Join(dir, "version")
	if err := os.WriteFile(f, []byte("Linux version 4.4.0-18362-Microsoft (Microsoft@Microsoft.com)"), 0o644); err != nil {
		t.Fatal(err)
	}
	procVersionPath = f
	defer func() { procVersionPath = old }()

	if !isWSL1() {
		t.Skip("WSL1 not detected after injection — can't test Available()")
	}
	if Available() {
		t.Error("Available() should return false when isWSL1() is true")
	}
}

// --- bwrapPath tests ---

func TestBwrapPath_Deterministic(t *testing.T) {
	// bwrapPath() calls exec.LookPath each time. Results should be
	// consistent across calls (LookPath is deterministic for the same
	// PATH environment).
	p := bwrapPath()
	p2 := bwrapPath()
	if p != p2 {
		t.Errorf("bwrapPath() returned inconsistent results: %q vs %q", p, p2)
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
	writableDir := t.TempDir()
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

	if cmd.Path != bwrapPath() {
		t.Errorf("cmd.Path = %q, want bwrap path %q", cmd.Path, bwrapPath())
	}
	if len(cmd.Args) < 2 {
		t.Fatalf("cmd.Args too short: %v", cmd.Args)
	}
	if cmd.Args[0] != bwrapPath() {
		t.Errorf("cmd.Args[0] = %q, want %q", cmd.Args[0], bwrapPath())
	}
	resolvedBin, _ := exec.LookPath("echo")
	if !containsContiguousSequence(cmd.Args, "--", resolvedBin) {
		t.Errorf("original binary %q not found after -- in args: %v", resolvedBin, cmd.Args)
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
	cmd.Args = nil
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

	if len(cmd.Args) < 2 {
		t.Errorf("cmd.Args too short after prepare: %v", cmd.Args)
	}
}

func TestPrepare_RejectsRootWritableDir(t *testing.T) {
	if bwrapPath() == "" {
		t.Skip("bwrap not in PATH")
	}

	cmd := exec.Command("echo", "hello")
	cfg := Config{
		ProjectDir:   t.TempDir(),
		WritableDirs: []string{"/"},
		Mode:         ModeAuto,
	}

	_, err := prepare(cmd, cfg)
	if err == nil {
		t.Fatal("expected prepare to reject writable dir /")
	}
	if !strings.Contains(err.Error(), "refusing to mount") {
		t.Errorf("error should come from validateWritableDir, got: %v", err)
	}
}

func TestPrepare_RejectsAncestorWritableDir(t *testing.T) {
	if bwrapPath() == "" {
		t.Skip("bwrap not in PATH")
	}

	projectDir := t.TempDir()
	cmd := exec.Command("echo", "hello")
	cfg := Config{
		ProjectDir:   projectDir,
		WritableDirs: []string{filepath.Dir(projectDir)},
		Mode:         ModeAuto,
	}

	_, err := prepare(cmd, cfg)
	if err == nil {
		t.Fatal("expected prepare to reject writable dir that is ancestor of project dir")
	}
	if !strings.Contains(err.Error(), "overlaps with project dir") {
		t.Errorf("error should come from validateWritableDir, got: %v", err)
	}
}

// --- seccomp BPF tests ---

func TestCompileSeccompBPF_MinimalBlocksSyscall(t *testing.T) {
	raw, err := compileSeccompBPF(profileMinimal)
	if err != nil {
		t.Fatalf("compileSeccompBPF: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty BPF program")
	}
	// The program should be valid BPF: each instruction is 8 bytes.
	if len(raw)%8 != 0 {
		t.Errorf("BPF program length %d is not a multiple of 8", len(raw))
	}
}

func TestCompileSeccompBPF_FullBlocksSyscall(t *testing.T) {
	raw, err := compileSeccompBPF(profileFull)
	if err != nil {
		t.Fatalf("compileSeccompBPF: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty BPF program")
	}
	if len(raw)%8 != 0 {
		t.Errorf("BPF program length %d is not a multiple of 8", len(raw))
	}
	// Full profile has more rules than minimal.
	minimalRaw, _ := compileSeccompBPF(profileMinimal)
	if len(raw) <= len(minimalRaw) {
		t.Error("full profile should produce more BPF instructions than minimal")
	}
}

func TestCompileSeccompBPF_EmptyProfile(t *testing.T) {
	raw, err := compileSeccompBPF(SeccompProfile{})
	if err != nil {
		t.Fatalf("compileSeccompBPF: %v", err)
	}
	if len(raw) != 0 {
		t.Errorf("empty profile should produce empty BPF, got %d instructions", len(raw))
	}
}

func TestCreateSeccompFD_NilRaw(t *testing.T) {
	fd, err := createSeccompFD(nil)
	if err != nil {
		t.Fatalf("createSeccompFD(nil): %v", err)
	}
	if fd != 0 {
		t.Errorf("expected fd=0 for nil raw, got %d", fd)
	}
}

// --- rlimit arg tests ---

func TestBuildBwrapArgs_RlimitWhenSupported(t *testing.T) {
	cfg := Config{
		Resources: ResourceLimits{MemoryMB: 512, Processes: 128, FDs: 256},
	}
	args := buildBwrapArgs(cfg, "/project", nil, "/bin/ls", nil, 0, true)
	if !containsFlag(args, "--rlimit") {
		t.Error("expected --rlimit flags when supportsRlimit=true")
	}
	if !containsContiguousSequence(args, "--rlimit", "RLIMIT_AS", fmt.Sprintf("%d", int64(512)*1024*1024)) {
		t.Errorf("expected RLIMIT_AS with 512 MiB, args: %v", args)
	}
	if !containsContiguousSequence(args, "--rlimit", "RLIMIT_NPROC", "128") {
		t.Errorf("expected RLIMIT_NPROC=128, args: %v", args)
	}
	if !containsContiguousSequence(args, "--rlimit", "RLIMIT_NOFILE", "256") {
		t.Errorf("expected RLIMIT_NOFILE=256, args: %v", args)
	}
}

func TestBuildBwrapArgs_NoRlimitWhenUnsupported(t *testing.T) {
	cfg := Config{
		Resources: ResourceLimits{MemoryMB: 512, Processes: 128, FDs: 256},
	}
	args := buildBwrapArgs(cfg, "/project", nil, "/bin/ls", nil, 0, false)
	if containsFlag(args, "--rlimit") {
		t.Errorf("--rlimit should not be present when supportsRlimit=false, args: %v", args)
	}
}

func TestBuildBwrapArgs_NoRlimitWhenZeroResources(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if containsFlag(args, "--rlimit") {
		t.Errorf("--rlimit should not be present with zero resources, args: %v", args)
	}
}

func TestBuildBwrapArgs_SeccompFD(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 42, true)
	if !containsContiguousSequence(args, "--seccomp", "42") {
		t.Errorf("expected --seccomp 42, args: %v", args)
	}
}

func TestBuildBwrapArgs_NoSeccompWhenZero(t *testing.T) {
	args := buildBwrapArgs(Config{}, "/project", nil, "/bin/ls", nil, 0, true)
	if containsFlag(args, "--seccomp") {
		t.Errorf("--seccomp should not be present when fd=0, args: %v", args)
	}
}

func TestBwrapSupportsRlimit_CachesResult(t *testing.T) {
	ResetBwrapPathCache()
	result1 := bwrapSupportsRlimit()
	result2 := bwrapSupportsRlimit()
	if result1 != result2 {
		t.Errorf("bwrapSupportsRlimit() returned different results: %v vs %v", result1, result2)
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.12.0", "0.11.9", 1},
		{"0.11.9", "0.12.0", -1},
		{"0.12.0", "0.12.0", 0},
		{"1.0.0", "0.99.99", 1},
		{"0.12.0+git", "0.12.0", 0},
	}
	for _, tt := range tests {
		got := compareSemver(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
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

// containsContiguousSequence checks whether args contains the given
// subsequence as a contiguous window. This is stricter than the old
// containsSequence (which allowed non-contiguous matches) and prevents
// the "tests test the helper, not the code" failure mode where a
// split arg pair (e.g. --proc ... /proc) would still pass.
func containsContiguousSequence(args []string, seq ...string) bool {
	if len(seq) == 0 {
		return true
	}
	for i := 0; i+len(seq) <= len(args); i++ {
		match := true
		for j := 0; j < len(seq); j++ {
			if args[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
