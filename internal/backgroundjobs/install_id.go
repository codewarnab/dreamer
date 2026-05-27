package backgroundjobs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"dreamer/internal/fsutil"
)

const (
	installIDFile  = "install_id"
	randomHexIDLen = 16 // 8 bytes = 16 hex chars, shared by job IDs and install IDs
)

// generateRandomHex returns a cryptographically random hex string of n characters.
// Shared by GenerateJobID and ResolveInstallID to avoid DRY violations.
func generateRandomHex(n int) (string, error) {
	b := make([]byte, n/2)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random hex: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ResolveInstallID returns the stable install ID for this Dreamer installation,
// creating one if it doesn't exist. The ID is a 16-hex-char string stored
// at <storeDir>/install_id.
func ResolveInstallID(storeDir string) (string, error) {
	path := filepath.Join(storeDir, installIDFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("read install ID: %w", err)
		}
		// File doesn't exist — create one below.
	} else {
		id := strings.TrimSpace(string(data))
		if isValidHexID(id) {
			return id, nil
		}
		// Corrupt file — regenerate below.
	}

	id, err := generateRandomHex(randomHexIDLen)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(storeDir, fsutil.DirPerms); err != nil {
		return "", fmt.Errorf("create store dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), fsutil.FilePerms); err != nil {
		return "", fmt.Errorf("write install ID: %w", err)
	}
	return id, nil
}

// isValidHexID checks that id is exactly randomHexIDLen lowercase hex chars.
func isValidHexID(id string) bool {
	if len(id) != randomHexIDLen {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// HashConfigPath returns a short hex hash of the config path for drift detection.
// Normalizes case on Windows to prevent false mismatches from casing differences.
func HashConfigPath(configPath string) string {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		abs = configPath
	}
	// Case-insensitive on Windows to match filepath behavior.
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	b := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(b[:8]) // 16 hex chars
}

// HashExecutablePath returns a hex hash of the executable path for drift detection.
func HashExecutablePath(exePath string) string {
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		resolved = exePath
	}
	b := sha256.Sum256([]byte(resolved))
	return hex.EncodeToString(b[:8])
}

// HashScheduleSpec returns a SHA-256 hash of the JSON-serialized ScheduleSpec.
func HashScheduleSpec(spec ScheduleSpec) (string, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal schedule spec: %w", err)
	}
	b := sha256.Sum256(data)
	return hex.EncodeToString(b[:8]), nil
}
