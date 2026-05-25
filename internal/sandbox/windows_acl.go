//go:build windows && !arm64

package sandbox

import (
	"fmt"
	"sync"
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
// restored after the last in-process sandbox user releases that directory.
type aclSnapshot struct {
	dir string
	acl *windows.ACL
	sd  *windows.SECURITY_DESCRIPTOR
}

type aclReference struct {
	snapshot aclSnapshot
	count    int
}

var writableACLState = struct {
	sync.Mutex
	refs map[string]*aclReference
}{
	refs: map[string]*aclReference{},
}

// acquireWritableACL grants the sandbox capability SID write access to dir and
// returns a release function that restores the original DACL after the last
// overlapping sandbox in this process releases the same directory.
func acquireWritableACL(dir string, sid *windows.SID) (func(), error) {
	writableACLState.Lock()
	ref := writableACLState.refs[dir]
	if ref == nil {
		snapshot, err := snapshotDACL(dir)
		if err != nil {
			writableACLState.Unlock()
			return nil, err
		}
		ref = &aclReference{snapshot: snapshot}
		writableACLState.refs[dir] = ref
	}
	ref.count++
	writableACLState.Unlock()

	if err := setAllowWriteACL(dir, sid); err != nil {
		releaseWritableACL(dir)
		return nil, err
	}

	released := false
	return func() {
		if released {
			return
		}
		released = true
		releaseWritableACL(dir)
	}, nil
}

// releaseWritableACL decrements the in-process reference count for dir. The
// original DACL is restored only when no active sandbox still depends on it.
func releaseWritableACL(dir string) {
	writableACLState.Lock()
	defer writableACLState.Unlock()

	ref := writableACLState.refs[dir]
	if ref == nil {
		return
	}
	ref.count--
	if ref.count > 0 {
		return
	}
	delete(writableACLState.refs, dir)
	restoreDACL(ref.snapshot)
	freeDACL(ref.snapshot)
}

// snapshotDACL reads the current DACL on a directory for rollback purposes.
func snapshotDACL(dir string) (aclSnapshot, error) {
	sd, err := windows.GetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return aclSnapshot{}, fmt.Errorf("sandbox: snapshot DACL(%s): %w", dir, err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		// Nil DACL (no explicit entries) — return nil ACL which restores
		// as "no explicit DACL" on rollback.
		acl = nil
	}
	return aclSnapshot{dir: dir, acl: acl, sd: sd}, nil
}

func restoreDACL(snapshot aclSnapshot) {
	_ = windows.SetNamedSecurityInfo(
		snapshot.dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		nil, nil, snapshot.acl, nil,
	)
}

func freeDACL(snapshot aclSnapshot) {
	if snapshot.sd != nil {
		windows.LocalFree(windows.Handle(unsafe.Pointer(snapshot.sd)))
	}
}
