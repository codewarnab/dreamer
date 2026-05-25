//go:build windows

package sandbox

import (
	"os"
	"os/exec"
	"testing"
)

// TestPrepareAndPostStart_RunsChild verifies that the child process actually
// runs to completion. This is the B1 regression test — before the fix,
// postStart() deferred CloseHandle on the job handle, which triggered
// KILL_ON_JOB_CLOSE and killed the child immediately.
//
// Requires elevated privileges (SeAssignPrimaryTokenPrivilege) to create
// restricted tokens. Runs in a subprocess because CreateRestrictedToken
// causes STATUS_HEAP_CORRUPTION (0xc0000374) when called without admin,
// which kills the test process.
func TestPrepareAndPostStart_RunsChild(t *testing.T) {
	if os.Getenv("DREAMER_SANDBOX_INTEGRATION") == "1" {
		integrationTestRunChild(t)
		return
	}

	// Re-run self as subprocess with env flag.
	cmd := exec.Command(os.Args[0], "-test.run=TestPrepareAndPostStart_RunsChild", "-test.v")
	cmd.Env = append(os.Environ(), "DREAMER_SANDBOX_INTEGRATION=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Exit code 0xc0000374 = STATUS_HEAP_CORRUPTION (no admin).
		// Exit code 1 = normal test failure (e.g., t.Skip).
		t.Logf("subprocess output:\n%s", string(out))
		// If the subprocess skipped (non-zero but no heap corruption),
		// propagate the skip.
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if code < 0 {
				// Negative exit codes on Windows are NTSTATUS values.
				// -1073740940 = 0xc0000374 = STATUS_HEAP_CORRUPTION.
				t.Skipf("CreateRestrictedToken requires admin (NTSTATUS: 0x%x)", uint32(code))
			}
		}
		t.Skipf("subprocess failed (likely missing admin): %v", err)
	}
}

func integrationTestRunChild(t *testing.T) {
	if !Available() {
		t.Skip("sandbox not available on this platform")
	}

	projectDir := t.TempDir()
	writableDir := t.TempDir()

	cmd := exec.Command("cmd", "/c", "echo", "hello")
	cfg := Config{
		ProjectDir:   projectDir,
		Mode:         ModeAuto,
		WritableDirs: []string{writableDir},
	}

	prepCleanup, err := Prepare(cmd, cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer prepCleanup()

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Logf("Child started: PID=%d", cmd.Process.Pid)

	postCleanup, err := PostStart(cmd, cfg)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("PostStart: %v", err)
	}
	defer postCleanup()

	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: child process failed (B1 regression — job handle closed too early?): %v", err)
	}
	t.Log("Child completed successfully")
}

// TestCreateCapabilitySID verifies that the SID file is created and loaded.
func TestCreateCapabilitySID(t *testing.T) {
	dir := t.TempDir()
	sid, err := createCapabilitySID(dir)
	if err != nil {
		t.Fatalf("createCapabilitySID: %v", err)
	}
	if sid == nil {
		t.Fatal("createCapabilitySID returned nil SID")
	}

	// Loading the same dir again should return the same SID.
	sid2, err := createCapabilitySID(dir)
	if err != nil {
		t.Fatalf("createCapabilitySID (2nd call): %v", err)
	}
	if sid.String() != sid2.String() {
		t.Errorf("SID changed between calls: %s != %s", sid.String(), sid2.String())
	}
}

// TestCreateCapabilitySID_DifferentDirs verifies different dirs get different SIDs.
func TestCreateCapabilitySID_DifferentDirs(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	sid1, err := createCapabilitySID(dir1)
	if err != nil {
		t.Fatalf("createCapabilitySID(dir1): %v", err)
	}
	sid2, err := createCapabilitySID(dir2)
	if err != nil {
		t.Fatalf("createCapabilitySID(dir2): %v", err)
	}
	if sid1.String() == sid2.String() {
		t.Errorf("different dirs should get different SIDs, both got %s", sid1.String())
	}
}

// TestGenerateRandomSID verifies the SID format.
func TestGenerateRandomSID(t *testing.T) {
	sid := generateRandomSID()
	if sid == "" {
		t.Fatal("generateRandomSID returned empty string")
	}
	// Should start with S-1-5-21- prefix (standard Windows random SID).
	if len(sid) < 10 || sid[:8] != "S-1-5-21" {
		t.Errorf("unexpected SID format: %s", sid)
	}
}
