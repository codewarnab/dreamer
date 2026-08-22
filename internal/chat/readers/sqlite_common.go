package readers

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// openSQLDatabase resolves the driver name and open hook, then opens the
// database. Shared by kiro, opencode, and sqlite readers.
func openSQLDatabase(driverName string, openFunc func(string, string) (*sql.DB, error), dbPath string, label string) (*sql.DB, error) {
	path := strings.TrimSpace(dbPath)
	if path == "" {
		return nil, fmt.Errorf("%s sqlite path is required", label)
	}

	resolvedDriver := strings.TrimSpace(driverName)
	if resolvedDriver == "" {
		resolvedDriver = DefaultSQLiteDriverName
	}

	openDB := openFunc
	if openDB == nil {
		openDB = sql.Open
	}

	database, err := openDB(resolvedDriver, path)
	if err != nil {
		return nil, fmt.Errorf("open %s database %q with driver %q: %w", label, path, resolvedDriver, err)
	}
	return database, nil
}

// sqliteContentDigest returns the hex sha256 of a canonical fingerprint
// string. Shared by the SQLite-backed readers' per-session fingerprint
// functions so every provider produces digests in the same form.
func sqliteContentDigest(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// fingerprintTimestamp normalizes any-typed SQLite timestamp columns to Unix
// nanoseconds so fingerprint input stays stable regardless of how a provider
// stores its timestamps (unix seconds, RFC3339 text, and so on).
func fingerprintTimestamp(raw any) int64 {
	parsed, _ := parseTimestamp(raw)
	return parsed.UnixNano()
}
