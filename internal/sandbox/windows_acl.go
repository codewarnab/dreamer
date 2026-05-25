//go:build windows && !arm64

package sandbox

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

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
		return fmt.Errorf("sandbox: GetNamedSecurityInfo(%s): %w", dir, err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(sd)))

	existingACL, _, err := sd.DACL()
	if err != nil {
		// A nil DACL (no explicit entries, full-access inherited) is not
		// an error — treat as empty so SetEntriesInAcl creates a fresh ACL.
		// Trade-off: if the nil DACL is from actual corruption (extremely
		// rare), we silently treat it as empty. This is acceptable because
		// the sandbox will still add its own permission entry correctly.
		existingACL = nil
	}

	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: fileGenericWrite | fileGenericRead,
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
		return fmt.Errorf("sandbox: SetEntriesInAcl(%s): %w", dir, lastErr)
	}
	defer windows.LocalFree((windows.Handle)(unsafe.Pointer(newACL)))

	// Trade-off: PROTECTED_DACL (0x80000000) was dropped because it strips
	// inherited parent ACEs (SYSTEM, antivirus, admin tools) as a persistent,
	// unreversed side effect. Without it, the merge approach preserves
	// inherited ACEs. The sandbox process still cannot escape its boundaries
	// because it runs under a WRITE_RESTRICTED token with only the capability
	// SID. Edge case: a parent "Deny Write" inherited rule (rare, corporate
	// GPO) could trickle down and block writes to allowed folders.
	if err := windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		nil, nil, newACL, nil,
	); err != nil {
		return fmt.Errorf("sandbox: SetNamedSecurityInfo(%s): %w", dir, err)
	}
	return nil
}

// aclSnapshot captures a directory's DACL before mutation so it can be
// restored on partial failure.
type aclSnapshot struct {
	dir string
	acl *windows.ACL
}

// snapshotDACL reads the current DACL on a directory for rollback purposes.
func snapshotDACL(dir string) (*windows.ACL, error) {
	sd, err := windows.GetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return nil, fmt.Errorf("sandbox: snapshot DACL(%s): %w", dir, err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		// Nil DACL (no explicit entries) — return nil ACL which restores
		// as "no explicit DACL" on rollback.
		return nil, nil
	}
	return acl, nil
}
