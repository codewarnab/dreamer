package readers

import (
	"database/sql"
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
