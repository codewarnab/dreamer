package readers

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureSQLiteDriverName = "dreamer-fixture-sqlite"

var (
	registerFixtureSQLiteDriver sync.Once
	fixtureSQLiteMu             sync.Mutex
	fixtureSQLiteDatasets       = map[string]fixtureSQLiteDataset{}
)

type fixtureSQLiteDataset struct {
	Columns []string
	Rows    []fixtureSQLiteRow
}

type fixtureSQLiteRow struct {
	SessionID string
	Modified  any
}

func TestQuerySessionsModifiedSinceFiltersByTimestamp(t *testing.T) {
	dbPath := registerFixtureSQLiteDataset(t, fixtureSQLiteDataset{
		Columns: []string{"id", "updated_at"},
		Rows: []fixtureSQLiteRow{
			{SessionID: "session-1", Modified: "2024-01-10T00:00:00Z"},
			{SessionID: "session-2", Modified: "2024-01-20T00:00:00Z"},
			{SessionID: "session-3", Modified: int64(1708387200)},
		},
	})

	since := time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC)
	reader := SQLiteReader{DriverName: fixtureSQLiteDriverName}
	sessions, err := reader.QuerySessionsModifiedSince(dbPath, since)
	if err != nil {
		t.Fatalf("QuerySessionsModifiedSince returned error: %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].SessionID != "session-2" {
		t.Fatalf("first session id = %q, want session-2", sessions[0].SessionID)
	}
	if sessions[1].SessionID != "session-3" {
		t.Fatalf("second session id = %q, want session-3", sessions[1].SessionID)
	}
}

func TestQuerySessionsModifiedSinceSupportsAlternateColumns(t *testing.T) {
	dbPath := registerFixtureSQLiteDataset(t, fixtureSQLiteDataset{
		Columns: []string{"session_id", "modified_at"},
		Rows: []fixtureSQLiteRow{
			{SessionID: "session-a", Modified: "2024-02-01T00:00:00Z"},
		},
	})

	reader := SQLiteReader{DriverName: fixtureSQLiteDriverName}
	sessions, err := reader.QuerySessionsModifiedSince(dbPath, time.Time{})
	if err != nil {
		t.Fatalf("QuerySessionsModifiedSince returned error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].SessionID != "session-a" {
		t.Fatalf("session id = %q, want session-a", sessions[0].SessionID)
	}
}

func TestQuerySessionsModifiedSinceReturnsErrorForUnknownColumns(t *testing.T) {
	dbPath := registerFixtureSQLiteDataset(t, fixtureSQLiteDataset{
		Columns: []string{"foo", "bar"},
	})

	reader := SQLiteReader{DriverName: fixtureSQLiteDriverName}
	_, err := reader.QuerySessionsModifiedSince(dbPath, time.Time{})
	if err == nil {
		t.Fatalf("QuerySessionsModifiedSince expected missing columns error")
	}
}

type fixtureDriver struct{}

func (fixtureDriver) Open(name string) (driver.Conn, error) {
	fixtureSQLiteMu.Lock()
	dataset, ok := fixtureSQLiteDatasets[name]
	fixtureSQLiteMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("missing dataset for dsn %q", name)
	}

	return &fixtureConn{
		dataset: dataset,
	}, nil
}

type fixtureConn struct {
	dataset fixtureSQLiteDataset
}

func (connection *fixtureConn) Prepare(query string) (driver.Stmt, error) {
	return nil, fmt.Errorf("prepare not supported for query %q", query)
}

func (connection *fixtureConn) Close() error {
	return nil
}

func (connection *fixtureConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("transactions are not supported")
}

func (connection *fixtureConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(lowerQuery, "pragma table_info(sessions)"):
		rows := make([][]driver.Value, 0, len(connection.dataset.Columns))
		for index, columnName := range connection.dataset.Columns {
			rows = append(rows, []driver.Value{
				int64(index),
				columnName,
				"TEXT",
				int64(0),
				nil,
				int64(0),
			})
		}
		return &fixtureRows{
			columns: []string{"cid", "name", "type", "notnull", "dflt_value", "pk"},
			rows:    rows,
		}, nil
	case strings.HasPrefix(lowerQuery, "select "):
		rows := make([][]driver.Value, 0, len(connection.dataset.Rows))
		for _, row := range connection.dataset.Rows {
			rows = append(rows, []driver.Value{row.SessionID, row.Modified})
		}
		return &fixtureRows{
			columns: []string{"session_id", "modified"},
			rows:    rows,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
}

func (connection *fixtureConn) Query(query string, _ []driver.Value) (driver.Rows, error) {
	return connection.QueryContext(context.Background(), query, nil)
}

type fixtureRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (rows *fixtureRows) Columns() []string {
	return rows.columns
}

func (rows *fixtureRows) Close() error {
	return nil
}

func (rows *fixtureRows) Next(dest []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}

	current := rows.rows[rows.index]
	rows.index++
	copy(dest, current)
	return nil
}

func registerFixtureSQLiteDataset(t *testing.T, dataset fixtureSQLiteDataset) string {
	t.Helper()

	registerFixtureSQLiteDriver.Do(func() {
		sql.Register(fixtureSQLiteDriverName, fixtureDriver{})
	})

	dbPath := filepath.Join(t.TempDir(), "session-store.db")
	if err := os.WriteFile(dbPath, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	fixtureSQLiteMu.Lock()
	fixtureSQLiteDatasets[dbPath] = dataset
	fixtureSQLiteMu.Unlock()

	t.Cleanup(func() {
		fixtureSQLiteMu.Lock()
		delete(fixtureSQLiteDatasets, dbPath)
		fixtureSQLiteMu.Unlock()
	})

	return dbPath
}
