//go:build windows && !arm64

package sandbox

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

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
		return fmt.Errorf("sandbox: SetEntriesInAcl: %w", lastErr)
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
		return fmt.Errorf("sandbox: set token default DACL: %w", err)
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
		return 0, fmt.Errorf("sandbox: open process token: %w", err)
	}
	defer currentToken.Close()

	logonSID, err := getLogonSID(windows.Token(currentToken))
	if err != nil {
		return 0, fmt.Errorf("sandbox: get logon SID: %w", err)
	}
	everyoneSID, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		return 0, fmt.Errorf("sandbox: parse everyone SID: %w", err)
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
		return 0, fmt.Errorf("sandbox: CreateRestrictedToken: %w", lastErr)
	}

	// Set permissive default DACL so the process can create pipes/IPC.
	if err := setDefaultDACL(
		windows.Token(restrictedToken),
		logonSID, everyoneSID, capSID,
	); err != nil {
		restrictedToken.Close()
		return 0, fmt.Errorf("sandbox: set default DACL: %w", err)
	}

	return restrictedToken, nil
}
