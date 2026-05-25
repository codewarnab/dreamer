//go:build windows && !arm64

package sandbox

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
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
		// File exists but contents are corrupt — do NOT overwrite silently.
		// Overwriting would orphan every prior ACE that referenced the old SID,
		// locking folders permanently. Surface the error so the user can
		// manually delete the file to regenerate.
		return nil, fmt.Errorf("sandbox: SID file %s exists but is corrupt; delete it manually to regenerate", path)
	}

	// Atomically create a new SID file. O_CREATE|O_EXCL ensures only one
	// process wins the race; losers re-read the winner's SID on retry.
	// Trade-off: if the SID file becomes corrupt, dreamer stops instead of
	// auto-fixing. This is intentional — auto-fixing would silently orphan
	// all existing folder permissions that referenced the old SID.
	sidStr := generateRandomSID()
	// NOTE: 0o600 is a no-op on Windows (NTFS ACLs control access).
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		// Another process created it between our ReadFile and OpenFile.
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("sandbox: re-read SID file: %w", readErr)
		}
		return windows.StringToSid(strings.TrimSpace(string(data)))
	}
	if err != nil {
		return nil, fmt.Errorf("sandbox: create SID file: %w", err)
	}
	_, _ = f.WriteString(sidStr)
	_ = f.Close()

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
		return nil, fmt.Errorf("sandbox: get token groups: %w", err)
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
	return nil, fmt.Errorf("sandbox: logon SID not found in token groups")
}
