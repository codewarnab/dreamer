//go:build windows

// Windows sandbox using WRITE_RESTRICTED tokens with capability SIDs and
// Job Objects (KILL_ON_JOB_CLOSE). The capability SID is a unique identity
// (not the user's real SID) added as a restricting SID on the token. The
// kernel blocks writes to any object whose ACL doesn't explicitly grant the
// capability SID — no filesystem mutation required.
package sandbox

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
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
)

const (
	// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: all processes in the job are
	// terminated when the last job handle is closed.
	JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000

	FILE_GENERIC_READ  = 0x00120089
	FILE_GENERIC_WRITE = 0x00120116
	genericAll         = 0x10000000

	// CreateRestrictedToken flags.
	flagDisableMaxPrivilege = 0x01
	flagLuaToken            = 0x04
	flagWriteRestricted     = 0x08

	// SE_GROUP_LOGON_ID identifies the logon SID in token groups.
	seGroupLogonID = 0xC0000000
)

// sidAndAttrs matches the Windows SID_AND_ATTRIBUTES struct layout.
type sidAndAttrs struct {
	Sid   *windows.SID
	Attrs uint32
}

// createCapabilitySID loads or creates a persistent capability SID for the
// given workspace directory. The SID is stored in ~/.dreamer/.sandbox/.
// Each workspace gets a unique SID keyed by a SHA-256 hash of the canonical
// workspace path.
func createCapabilitySID(workspaceDir string) (*windows.SID, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("sandbox: get home dir: %w", err)
	}
	dir := filepath.Join(home, ".dreamer", ".sandbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("sandbox: create SID dir: %w", err)
	}

	h := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(workspaceDir))))
	path := filepath.Join(dir, hex.EncodeToString(h[:8])+".sid")

	// Try loading an existing SID.
	if data, err := os.ReadFile(path); err == nil {
		if sid, err := windows.StringToSid(strings.TrimSpace(string(data))); err == nil {
			return sid, nil
		}
	}

	// Generate and persist a new random SID.
	sidStr := generateRandomSID()
	// TODO(linux): 0o600 is a no-op on Windows (NTFS ACLs needed).
	// Not a real risk since the SID isn't exploitable on its own.
	if err := os.WriteFile(path, []byte(sidStr), 0o600); err != nil {
		return nil, fmt.Errorf("sandbox: write SID file: %w", err)
	}
	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		return nil, fmt.Errorf("sandbox: parse generated SID: %w", err)
	}
	return sid, nil
}

// generateRandomSID creates a random SID string in the form S-1-5-21-a-b-c-d.
// This mirrors the Codex approach (codex-rs/windows-sandbox-rs/src/cap.rs).
func generateRandomSID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("S-1-5-21-%d-%d-%d-%d",
		binary.LittleEndian.Uint32(b[0:4]),
		binary.LittleEndian.Uint32(b[4:8]),
		binary.LittleEndian.Uint32(b[8:12]),
		binary.LittleEndian.Uint32(b[12:16]),
	)
}

// getLogonSID extracts the logon SID from the token's group list. The logon
// SID uniquely identifies the current logon session and is needed as a
// restricting SID so the sandboxed process can access objects created during
// this session.
func getLogonSID(token windows.Token) (*windows.SID, error) {
	// tokenGroups mirrors the Windows TOKEN_GROUPS layout:
	// DWORD GroupCount, then SID_AND_ATTRIBUTES[] at pointer-aligned offset.
	type tokenGroups struct {
		Count  uint32
		Groups [1]sidAndAttrs
	}

	var needed uint32
	windows.GetTokenInformation(token, windows.TokenGroups, nil, 0, &needed)
	buf := make([]byte, needed)
	if err := windows.GetTokenInformation(token, windows.TokenGroups, &buf[0], needed, &needed); err != nil {
		return nil, fmt.Errorf("get token groups: %w", err)
	}

	// TODO(arm): unsafe.Pointer cast assumes byte-slice alignment matches
	// tokenGroups alignment (8 on ARM64). Works on x86-64 but would crash
	// on ARM Windows due to misaligned pointer access.
	tg := (*tokenGroups)(unsafe.Pointer(&buf[0]))
	groups := unsafe.Slice(&tg.Groups[0], tg.Count)
	for _, g := range groups {
		if g.Attrs&seGroupLogonID == seGroupLogonID {
			return g.Sid.Copy()
		}
	}
	return nil, fmt.Errorf("logon SID not found in token groups")
}

// setDefaultDACL sets a permissive default DACL on the token so the sandboxed
// process can create pipes, IPC objects, and temp files. Without this, CLI
// providers that use named pipes (PowerShell, Node.js) hit ACCESS_DENIED.
func setDefaultDACL(token windows.Token, sids ...*windows.SID) error {
	entries := make([]windows.EXPLICIT_ACCESS, len(sids))
	for i, sid := range sids {
		entries[i] = windows.EXPLICIT_ACCESS{
			AccessPermissions: genericAll,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		}
	}

	var newACL *windows.ACL
	ret, _, lastErr := procSetEntriesInAcl.Call(
		uintptr(len(entries)),
		uintptr(unsafe.Pointer(&entries[0])),
		0,
		uintptr(unsafe.Pointer(&newACL)),
	)
	if ret != 0 {
		return fmt.Errorf("SetEntriesInAcl: %w", lastErr)
	}
	defer windows.LocalFree((windows.Handle)(unsafe.Pointer(newACL)))

	info := struct {
		DefaultDACL *windows.ACL
	}{DefaultDACL: newACL}
	if err := windows.SetTokenInformation(
		token,
		windows.TokenDefaultDacl,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return fmt.Errorf("set token default DACL: %w", err)
	}
	return nil
}

// createRestrictedToken creates a WRITE_RESTRICTED token with the capability
// SID as a restricting SID. The kernel checks: "does the target object's ACL
// grant access to the capability SID?" Since the project dir's DACL doesn't
// mention the capability SID, writes are blocked — no filesystem mutation
// needed.
//
// Restricting SIDs: capability SID (workspace identity), logon SID (session
// access), everyone SID (public objects).
func createRestrictedToken(capSID *windows.SID) (syscall.Token, error) {
	currentProc, _ := windows.GetCurrentProcess()
	var currentToken syscall.Token
	err := windows.OpenProcessToken(
		windows.Handle(currentProc),
		windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_QUERY|
			windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ADJUST_PRIVILEGES,
		(*windows.Token)(&currentToken),
	)
	if err != nil {
		return 0, fmt.Errorf("open process token: %w", err)
	}
	defer currentToken.Close()

	logonSID, err := getLogonSID(windows.Token(currentToken))
	if err != nil {
		return 0, fmt.Errorf("get logon SID: %w", err)
	}
	everyoneSID, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		return 0, fmt.Errorf("parse everyone SID: %w", err)
	}

	// Build restricting SIDs array: [capability, logon, everyone].
	restricting := [3]sidAndAttrs{
		{Sid: capSID},
		{Sid: logonSID},
		{Sid: everyoneSID},
	}

	var restrictedToken syscall.Token
	flags := flagDisableMaxPrivilege | flagLuaToken | flagWriteRestricted
	ret, _, lastErr := procCreateRestrictedToken.Call(
		uintptr(currentToken),
		uintptr(flags),
		0, 0, // no SIDs to disable
		0, 0, // no privileges to delete
		uintptr(len(restricting)),
		uintptr(unsafe.Pointer(&restricting[0])),
		uintptr(unsafe.Pointer(&restrictedToken)),
	)
	if ret == 0 {
		return 0, fmt.Errorf("CreateRestrictedToken: %w", lastErr)
	}

	// Set permissive default DACL so the process can create pipes/IPC.
	if err := setDefaultDACL(
		windows.Token(restrictedToken),
		logonSID, everyoneSID, capSID,
	); err != nil {
		restrictedToken.Close()
		return 0, fmt.Errorf("set default DACL: %w", err)
	}

	return restrictedToken, nil
}

// setAllowWriteACL merges a FILE_GENERIC_WRITE | FILE_GENERIC_READ ACE for sid
// into the existing DACL on the given directory. The ACE inherits to all
// subdirectories and files. The existing DACL is read first so the new ACE is
// added alongside existing entries rather than replacing them.
func setAllowWriteACL(dir string, sid *windows.SID) error {
	// Read the existing DACL so we can merge instead of replace.
	sd, err := windows.GetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("GetNamedSecurityInfo(%s): %w", dir, err)
	}
	existingACL, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("DACL(%s): %w", dir, err)
	}

	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: FILE_GENERIC_WRITE | FILE_GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}

	var newACL *windows.ACL
	ret, _, lastErr := procSetEntriesInAcl.Call(
		1,
		uintptr(unsafe.Pointer(&ea)),
		uintptr(unsafe.Pointer(existingACL)),
		uintptr(unsafe.Pointer(&newACL)),
	)
	if ret != 0 {
		return fmt.Errorf("SetEntriesInAcl: %w", lastErr)
	}
	defer windows.LocalFree((windows.Handle)(unsafe.Pointer(newACL)))

	// PROTECTED_DACL_SECURITY_INFORMATION (0x80000000) prevents inherited
	// parent ACEs from overriding the explicit grant.
	const protectedDACL = 0x80000000
	if err := windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|protectedDACL,
		nil, nil, newACL, nil,
	); err != nil {
		return fmt.Errorf("SetNamedSecurityInfo(%s): %w", dir, err)
	}
	return nil
}

// prepare creates a restricted token with a capability SID and applies
// Allow-Write ACLs on writable directories. The project directory needs
// no ACL changes — writes are blocked because its DACL doesn't mention
// the capability SID. The user's real SID is unaffected.
func prepare(cmd *exec.Cmd, cfg Config) error {
	projectDir, err := filepath.Abs(cfg.ProjectDir)
	if err != nil {
		return fmt.Errorf("sandbox: resolve project dir: %w", err)
	}

	capSID, err := createCapabilitySID(projectDir)
	if err != nil {
		return fmt.Errorf("sandbox: create capability SID: %w", err)
	}

	token, err := createRestrictedToken(capSID)
	if err != nil {
		return fmt.Errorf("sandbox: create restricted token: %w", err)
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = token

	// Apply Allow-Write ACLs on writable directories using the capability SID.
	for _, wdir := range cfg.WritableDirs {
		absDir, err := filepath.Abs(wdir)
		if err != nil {
			token.Close()
			return fmt.Errorf("sandbox: resolve writable dir: %w", err)
		}
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			token.Close()
			return fmt.Errorf("sandbox: create writable dir %s: %w", absDir, err)
		}
		if err := setAllowWriteACL(absDir, capSID); err != nil {
			token.Close()
			return fmt.Errorf("sandbox: allow-write ACL on %s: %w", absDir, err)
		}
	}

	return nil
}

// postStart assigns the child process to a Job Object with
// KILL_ON_JOB_CLOSE so orphaned children are cleaned up.
func postStart(cmd *exec.Cmd, cfg Config) error {
	jobHandle, _, lastErr := procCreateJobObjectW.Call(0, 0)
	if jobHandle == 0 {
		return fmt.Errorf("sandbox: CreateJobObject: %w", lastErr)
	}
	defer windows.CloseHandle(windows.Handle(jobHandle))

	info := struct {
		BasicLimitInformation windows.JOBOBJECT_BASIC_LIMIT_INFORMATION
		IoInfo                windows.IO_COUNTERS
		ProcessMemoryLimit    uintptr
		JobMemoryLimit        uintptr
		PeakProcessMemoryUsed uintptr
		PeakJobMemoryUsed     uintptr
	}{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	ret, _, lastErr := procSetInformationJobObj.Call(
		jobHandle,
		uintptr(windows.JobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ret == 0 {
		return fmt.Errorf("sandbox: SetInformationJobObject: %w", lastErr)
	}

	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return fmt.Errorf("sandbox: OpenProcess(%d): %w", cmd.Process.Pid, err)
	}
	defer windows.CloseHandle(procHandle)

	ret, _, lastErr = procAssignProcessToJob.Call(
		jobHandle,
		uintptr(procHandle),
	)
	if ret == 0 {
		return fmt.Errorf("sandbox: AssignProcessToJobObject: %w", lastErr)
	}

	return nil
}
