package readers

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DefaultSQLiteDriverName is the driver name used by SQLiteReader when no
// custom driver is configured. Also referenced by the chat package for
// discovery-time availability checks.
const DefaultSQLiteDriverName = "sqlite"

type SessionMetadata struct {
	SessionID    string
	ModifiedTime time.Time
}

type SQLiteReader struct {
	DriverName string
	Open       func(driverName string, dataSourceName string) (*sql.DB, error)
}

func QuerySessionsModifiedSince(dbPath string, since time.Time) ([]SessionMetadata, error) {
	return SQLiteReader{}.QuerySessionsModifiedSince(dbPath, since)
}

func ReadSessionsModifiedSince(dbPath string, since time.Time) ([]SessionMetadata, error) {
	return QuerySessionsModifiedSince(dbPath, since)
}

func (reader SQLiteReader) QuerySessionsModifiedSince(dbPath string, since time.Time) ([]SessionMetadata, error) {
	database, err := openSQLDatabase(reader.DriverName, reader.Open, dbPath, "sqlite")
	if err != nil {
		return nil, err
	}
	defer database.Close()

	sessionIDColumn, modifiedColumn, err := discoverSessionColumns(database)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf("SELECT %s, %s FROM sessions", sessionIDColumn, modifiedColumn)
	rows, err := database.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query session metadata: %w", err)
	}
	defer rows.Close()

	sessions := make([]SessionMetadata, 0)
	for rows.Next() {
		var sessionID string
		var modifiedValue any

		if err := rows.Scan(&sessionID, &modifiedValue); err != nil {
			return nil, fmt.Errorf("scan session metadata row: %w", err)
		}

		modifiedTime, ok := parseTimestamp(modifiedValue)
		if !ok {
			continue
		}
		if !since.IsZero() && modifiedTime.Before(since) {
			continue
		}

		sessions = append(sessions, SessionMetadata{
			SessionID:    strings.TrimSpace(sessionID),
			ModifiedTime: modifiedTime,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session metadata rows: %w", err)
	}

	sort.Slice(sessions, func(i int, j int) bool {
		left := sessions[i]
		right := sessions[j]
		if left.ModifiedTime.Equal(right.ModifiedTime) {
			return left.SessionID < right.SessionID
		}
		return left.ModifiedTime.Before(right.ModifiedTime)
	})

	return sessions, nil
}

func discoverSessionColumns(database *sql.DB) (string, string, error) {
	rows, err := database.Query("PRAGMA table_info(sessions)")
	if err != nil {
		return "", "", fmt.Errorf("query sessions table metadata: %w", err)
	}
	defer rows.Close()

	idColumn := ""
	modifiedColumn := ""
	for rows.Next() {
		var cid int64
		var name string
		var colType string
		var notNull int64
		var defaultValue any
		var primaryKey int64

		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &primaryKey); err != nil {
			return "", "", fmt.Errorf("scan sessions table metadata: %w", err)
		}

		normalizedName := strings.ToLower(strings.TrimSpace(name))
		if idColumn == "" && (normalizedName == "id" || normalizedName == "session_id") {
			idColumn = normalizedName
		}
		if modifiedColumn == "" && (normalizedName == "modified_at" || normalizedName == "updated_at" || normalizedName == "last_modified" || normalizedName == "mtime") {
			modifiedColumn = normalizedName
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", fmt.Errorf("iterate sessions table metadata: %w", err)
	}

	if idColumn == "" || modifiedColumn == "" {
		return "", "", fmt.Errorf("sessions table is missing required id/modified columns")
	}

	return idColumn, modifiedColumn, nil
}
