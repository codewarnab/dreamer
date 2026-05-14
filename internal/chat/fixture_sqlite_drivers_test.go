package chat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"dreamer/internal/chat/readers"
)

var (
	discoverDriverCounter atomic.Uint64
	discoverDriverMutex   sync.Mutex
	discoverDriverOpenCodeDatasets = map[string][]readers.OpenCodeSession{}
	discoverDriverKiroDatasets     = map[string][]readers.KiroConversation{}
)

func registerDiscoverOpenCodeDriver(sessions []readers.OpenCodeSession) string {
	name := fmt.Sprintf("dreamer-discover-opencode-%d", discoverDriverCounter.Add(1))
	discoverDriverMutex.Lock()
	discoverDriverOpenCodeDatasets[name] = sessions
	discoverDriverMutex.Unlock()
	sql.Register(name, discoverOpenCodeDriver{name: name})
	return name
}

func registerDiscoverKiroDriver(conversations []readers.KiroConversation) string {
	name := fmt.Sprintf("dreamer-discover-kiro-%d", discoverDriverCounter.Add(1))
	discoverDriverMutex.Lock()
	discoverDriverKiroDatasets[name] = conversations
	discoverDriverMutex.Unlock()
	sql.Register(name, discoverKiroDriver{name: name})
	return name
}

type discoverOpenCodeDriver struct{ name string }

func (driverObj discoverOpenCodeDriver) Open(_ string) (driver.Conn, error) {
	discoverDriverMutex.Lock()
	sessions := discoverDriverOpenCodeDatasets[driverObj.name]
	discoverDriverMutex.Unlock()
	return &discoverOpenCodeConn{sessions: sessions}, nil
}

type discoverOpenCodeConn struct {
	sessions []readers.OpenCodeSession
}

func (connection *discoverOpenCodeConn) Prepare(query string) (driver.Stmt, error) {
	return &discoverOpenCodeStmt{connection: connection, query: query}, nil
}
func (connection *discoverOpenCodeConn) Close() error              { return nil }
func (connection *discoverOpenCodeConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("unsupported") }
func (connection *discoverOpenCodeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	return connection.runQuery(query)
}
func (connection *discoverOpenCodeConn) runQuery(query string) (driver.Rows, error) {
	if !strings.Contains(strings.ToLower(query), "from session") {
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
	rows := make([][]driver.Value, 0, len(connection.sessions))
	for _, session := range connection.sessions {
		rows = append(rows, []driver.Value{
			session.ID,
			session.Directory,
			session.Title,
			session.ModifiedTime.Unix(),
		})
	}
	return &discoverRows{columns: []string{"id", "directory", "title", "time_updated"}, rows: rows}, nil
}

type discoverOpenCodeStmt struct {
	connection *discoverOpenCodeConn
	query      string
}

func (stmt *discoverOpenCodeStmt) Close() error                                    { return nil }
func (stmt *discoverOpenCodeStmt) NumInput() int                                   { return -1 }
func (stmt *discoverOpenCodeStmt) Exec(_ []driver.Value) (driver.Result, error)    { return nil, fmt.Errorf("unsupported") }
func (stmt *discoverOpenCodeStmt) Query(_ []driver.Value) (driver.Rows, error) {
	return stmt.connection.runQuery(stmt.query)
}

type discoverKiroDriver struct{ name string }

func (driverObj discoverKiroDriver) Open(_ string) (driver.Conn, error) {
	discoverDriverMutex.Lock()
	conversations := discoverDriverKiroDatasets[driverObj.name]
	discoverDriverMutex.Unlock()
	return &discoverKiroConn{conversations: conversations}, nil
}

type discoverKiroConn struct {
	conversations []readers.KiroConversation
}

func (connection *discoverKiroConn) Prepare(query string) (driver.Stmt, error) {
	return &discoverKiroStmt{connection: connection, query: query}, nil
}
func (connection *discoverKiroConn) Close() error              { return nil }
func (connection *discoverKiroConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("unsupported") }
func (connection *discoverKiroConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	return connection.runQuery(query)
}
func (connection *discoverKiroConn) runQuery(query string) (driver.Rows, error) {
	if !strings.Contains(strings.ToLower(query), "from conversations_v2") {
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
	rows := make([][]driver.Value, 0, len(connection.conversations))
	for _, conversation := range connection.conversations {
		rows = append(rows, []driver.Value{
			conversation.Directory,
			conversation.ConversationID,
			conversation.ModifiedTime.Unix(),
		})
	}
	return &discoverRows{columns: []string{"key", "conversation_id", "updated_at"}, rows: rows}, nil
}

type discoverKiroStmt struct {
	connection *discoverKiroConn
	query      string
}

func (stmt *discoverKiroStmt) Close() error                                    { return nil }
func (stmt *discoverKiroStmt) NumInput() int                                   { return -1 }
func (stmt *discoverKiroStmt) Exec(_ []driver.Value) (driver.Result, error)    { return nil, fmt.Errorf("unsupported") }
func (stmt *discoverKiroStmt) Query(_ []driver.Value) (driver.Rows, error) {
	return stmt.connection.runQuery(stmt.query)
}

type discoverRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (rows *discoverRows) Columns() []string { return rows.columns }
func (rows *discoverRows) Close() error      { return nil }
func (rows *discoverRows) Next(dest []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	current := rows.rows[rows.index]
	rows.index++
	copy(dest, current)
	return nil
}
