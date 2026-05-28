//go:build windows && !arm64

package sandbox

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
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

	// Prune stale SID files before creating a new one. Errors are
	// non-fatal — cleanup is best-effort.
	pruneOrphanSIDs(dir, DefaultSIDExpiryDays)

	h := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(workspaceDir))))
	path := filepath.Join(dir, hex.EncodeToString(h[:8])+".sid")

	// Try loading an existing SID.
	if data, err := os.ReadFile(path); err == nil {
		if sid, err := windows.StringToSid(strings.TrimSpace(string(data))); err == nil {
			return sid, nil
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("sandbox: remove corrupt SID file %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("sandbox: read SID file %s: %w", path, err)
	}

	sidStr, err := generateRandomSID()
	if err != nil {
		return nil, err
	}
	if err := writeCapabilitySIDFile(path, sidStr); err != nil {
		if data, readErr := os.ReadFile(path); readErr == nil {
			if sid, parseErr := windows.StringToSid(strings.TrimSpace(string(data))); parseErr == nil {
				return sid, nil
			}
		}
		return nil, err
	}

	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		return nil, fmt.Errorf("sandbox: parse generated SID: %w", err)
	}
	return sid, nil
}

// DefaultSIDExpiryDays is the default number of days before an unused SID
// file is eligible for cleanup. Overridable via config.sandbox.sid_expiry_days.
const DefaultSIDExpiryDays = 7

// pruneOrphanSIDs removes .sid files in dir that are older than expiryDays.
// Errors are silently ignored — cleanup is best-effort.
func pruneOrphanSIDs(dir string, expiryDays int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Duration(expiryDays) * 24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sid" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// generateRandomSID creates a random SID string in the form S-1-5-21-a-b-c-d.
// This mirrors the Codex approach (codex-rs/windows-sandbox-rs/src/cap.rs).
func generateRandomSID() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", fmt.Errorf("sandbox: generate random SID: %w", err)
	}
	return fmt.Sprintf("S-1-5-21-%d-%d-%d-%d",
		binary.LittleEndian.Uint32(b[0:4]),
		binary.LittleEndian.Uint32(b[4:8]),
		binary.LittleEndian.Uint32(b[8:12]),
		binary.LittleEndian.Uint32(b[12:16]),
	), nil
}

// writeCapabilitySIDFile commits a generated SID through a synced temporary
// file and atomic rename so crashes do not leave a truncated persistent SID.
func writeCapabilitySIDFile(path, sidStr string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("sandbox: create temporary SID file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(sidStr + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sandbox: write temporary SID file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sandbox: sync temporary SID file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("sandbox: close temporary SID file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("sandbox: commit SID file: %w", err)
	}
	return nil
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
