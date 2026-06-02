//go:build windows && !arm64

// Windows sandbox using WRITE_RESTRICTED tokens with capability SIDs and
// Job Objects (KILL_ON_JOB_CLOSE). The capability SID is a unique identity
// (not the user's real SID) added as a restricting SID on the token. The
// kernel blocks writes to any object whose ACL doesn't explicitly grant the
// capability SID — no filesystem mutation required.
//
// File layout:
//
//	windows.go       — Available(), prepare(), lazy DLL block, constants
//	windows_sid.go   — capability SID, logon SID, sidAndAttrs
//	windows_token.go — restricted token, default DACL
//	windows_acl.go   — Allow-Write ACL on dirs, ACL rollback
//	windows_job.go   — Job Object, KILL_ON_JOB_CLOSE
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"dreamer/internal/procutil"
)

// Available reports whether the OS-level sandbox is supported.
// Always true on Windows.
func Available() bool { return true }

var (
	modadvapi32 = syscall.NewLazyDLL("advapi32.dll")
	modkernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateRestrictedToken = modadvapi32.NewProc("CreateRestrictedToken")
	procSetEntriesInAcl       = modadvapi32.NewProc("SetEntriesInAclW")
	procCreateJobObjectW      = modkernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObj  = modkernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob    = modkernel32.NewProc("AssignProcessToJobObject")
	procGetExitCodeProcess    = modkernel32.NewProc("GetExitCodeProcess")
)

const (
	// jobObjectLimitKillOnJobClose: all processes in the job are
	// terminated when the last job handle is closed.
	// (JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE in winnt.h)
	jobObjectLimitKillOnJobClose = 0x2000

	// FILE_GENERIC_READ, FILE_GENERIC_WRITE (winnt.h)
	fileGenericRead  = 0x00120089
	fileGenericWrite = 0x00120116
	genericAll       = 0x10000000 // GENERIC_ALL

	// CreateRestrictedToken flags (winnt.h).
	flagDisableMaxPrivilege = 0x01 // DISABLE_MAX_PRIVILEGE
	flagLuaToken            = 0x04 // LUA_TOKEN
	flagWriteRestricted     = 0x08 // WRITE_RESTRICTED

	// seGroupLogonID is SE_GROUP_LOGON_ID (0x80000000) ORed with
	// SE_GROUP_MANDATORY | SE_GROUP_ENABLED_BY_DEFAULT | SE_GROUP_ENABLED.
	// The mask check at getLogonSID matches when all four attributes are set.
	seGroupLogonID = 0xC0000000

	// stillActive is STILL_ACTIVE (259), returned by GetExitCodeProcess while
	// the child is alive.
	stillActive = 259
)

// KNOWN LIMITATION — OS scheduler manipulation (persistence) is NOT blocked.
//
// The WRITE_RESTRICTED token prevents file writes to protected directories,
// but does NOT prevent the sandboxed child from executing arbitrary binaries
// on the host PATH. A prompt-injection attack could cause the child to run:
//
//	schtasks /Create /TN "Backdoor" /TR "powershell -c ..." /SC DAILY
//	Register-ScheduledTask -TaskName "Backdoor" -Action (New-ScheduledTaskAction ...)
//	at.exe \\target 12:00 cmd /c "malicious payload"
//
// These commands succeed because the kernel's DAC check for CreateProcess
// only requires EXECUTE permission on the target binary, not WRITE — and
// the restricted token only restricts writes via the capability SID.
//
// Job Object breakaway: Tools that spawn processes through WMI or the
// Service Control Manager create new process trees outside the current
// Job Object. KILL_ON_JOB_CLOSE only terminates processes inside the job.
//
//	wmic process call create "cmd /c ..."        — WMI process creation
//	psexec -s cmd.exe                            — SCM-based process spawn
//	Start-Process -FilePath "cmd.exe"            — PowerShell wrapper
//	powershell -Command "Start-Process cmd.exe"  — indirect invocation
//
// Mitigation plan (not yet implemented):
//
//	Layer 1 — Deny FILE_GENERIC_EXECUTE on writable directories for the
//	capability SID. This prevents copy-rename attacks where the child
//	stages a binary in a writable dir and executes it from there.
//	If the child can only execute from read-only paths (System32,
//	Program Files), the attack surface shrinks to OS-provided binaries.
//
//	Layer 2 — Deny FILE_GENERIC_EXECUTE on the capability SID for known
//	escape binaries:
//	  Scheduler: schtasks.exe, at.exe, Register-ScheduledTask (COM)
//	  Shells: powershell.exe, pwsh.exe, cmd.exe (breakaway via Start-Process)
//	  WMI: wmic.exe (breakaway via process call create)
//	  SCM: psexec.exe, psexec64.exe (Sysinternals SCM spawn)
//	Paths are enumerate-and-deny (whack-a-mole) but cover the realistic
//	attack surface for prompt injection.
//
//	Layer 3 (future) — AppContainer (LowBox) via NtCreateLowBoxToken.
//	Provides kernel-enforced process + network isolation with a
//	capability-based model. Processes can't access objects outside
//	their capability set regardless of ACLs. This is the only layer
//	that blocks Win32 COM API calls to ITaskService directly.
//	See internal/sandbox/windows_appcontainer.go (planned).
//
// COM API gap: Even with Layers 1-2, a sophisticated attacker could call
// ITaskService::NewTask via COM directly (taskschd.dll is Microsoft-signed
// and in read-only System32). Only AppContainer (Layer 3) prevents this.
//
// prepare creates a restricted token with a capability SID and applies
// Allow-Write ACLs on writable directories. The project directory needs
// no ACL changes — writes are blocked because its DACL doesn't mention
// the capability SID. The user's real SID is unaffected.
//
// Returns a cleanup function that closes the restricted token handle.
// The caller MUST defer cleanup after cmd.Wait() returns.
func prepare(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	// Windows does not support network isolation. Return an actionable
	// error so the user can adjust their config instead of silently
	// running without isolation.
	if cfg.Network != NetworkOpen {
		return nil, fmt.Errorf("sandbox: network isolation not supported on Windows; set sandbox.network to \"open\" or remove the setting")
	}

	projectDir, err := filepath.Abs(cfg.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("sandbox: resolve project dir: %w", err)
	}

	capSID, err := createCapabilitySID(projectDir, cfg.SIDExpiryDays)
	if err != nil {
		return nil, fmt.Errorf("sandbox: create capability SID: %w", err)
	}

	token, err := createRestrictedToken(capSID)
	if err != nil {
		return nil, fmt.Errorf("sandbox: create restricted token: %w", err)
	}
	tokenClosed := false
	closeToken := func() {
		if !tokenClosed {
			token.Close()
			tokenClosed = true
		}
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = token
	// CREATE_NO_WINDOW prevents the sandboxed child from allocating a console
	// window. Sandboxed processes are headless — they communicate via pipes.
	cmd.SysProcAttr.CreationFlags |= procutil.CreateNoWindow

	var writableReleases []func()
	defer func() {
		if err != nil {
			for i := len(writableReleases) - 1; i >= 0; i-- {
				writableReleases[i]()
			}
			closeToken()
		}
	}()

	// Apply Allow-Write ACLs on writable directories using the capability SID.
	for _, wdir := range cfg.WritableDirs {
		absDir, err := filepath.Abs(wdir)
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve writable dir: %w", err)
		}
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			return nil, fmt.Errorf("sandbox: create writable dir %s: %w", absDir, err)
		}
		release, err := acquireWritableACL(absDir, capSID)
		if err != nil {
			return nil, fmt.Errorf("sandbox: allow-write ACL on %s: %w", absDir, err)
		}
		writableReleases = append(writableReleases, release)
	}

	return func() {
		for i := len(writableReleases) - 1; i >= 0; i-- {
			writableReleases[i]()
		}
		closeToken()
	}, nil
}
