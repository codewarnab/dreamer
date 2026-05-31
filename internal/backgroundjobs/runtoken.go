package backgroundjobs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"dreamer/internal/fsutil"
)

const runTokenFile = "run.token"

// LoadOrCreateRunToken returns the per-install run token, creating it if absent.
// The token is persisted at <storeDir>/run.token with 0600 perms so only the
// owning user can read it. The scheduler passes the file path to "jobs run"
// via --run-token-file; the runner reads and compares it against the stored secret.
func LoadOrCreateRunToken(storeDir string) (string, error) {
	path := filepath.Join(storeDir, runTokenFile)
	data, err := os.ReadFile(path)
	if err == nil {
		token := string(data)
		if token != "" {
			return token, nil
		}
		// Empty file — treat as missing and regenerate.
	}

	// Generate 32 random bytes → 64 hex chars.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate run token: %w", err)
	}
	token := hex.EncodeToString(b)

	if err := os.MkdirAll(storeDir, fsutil.DirPerms); err != nil {
		return "", fmt.Errorf("create token dir: %w", err)
	}
	if err := fsutil.WriteFileAtomic(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("write run token: %w", err)
	}
	return token, nil
}

// RunTokenPath returns the expected path for the run token file.
func RunTokenPath(storeDir string) string {
	return filepath.Join(storeDir, runTokenFile)
}

// ValidateRunToken reads the stored token and compares it to the provided value.
// Returns nil on match, error on mismatch or read failure.
func ValidateRunToken(storeDir, providedToken string) error {
	path := filepath.Join(storeDir, runTokenFile)
	stored, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read run token: %w", err)
	}
	if string(stored) != providedToken {
		return fmt.Errorf("run token mismatch")
	}
	return nil
}

// ReadRunToken reads the run token from disk without creating it.
func ReadRunToken(storeDir string) (string, error) {
	path := filepath.Join(storeDir, runTokenFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
