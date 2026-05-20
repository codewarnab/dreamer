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

const fixtureOpenCodeDriverName = "dreamer-fixture-opencode"

var (
	registerFixtureOpenCodeDriver sync.Once
	fixtureOpenCodeMu             sync.Mutex
	fixtureOpenCodeDatasets       = map[string]fixtureOpenCodeDataset{}
)

type fixtureOpenCodeDataset struct {
	Sessions []fixtureOpenCodeSession
	Messages []fixtureOpenCodeMessage
	Parts    []fixtureOpenCodePart
}

type fixtureOpenCodeSession struct {
	ID, Directory, Title string
	TimeUpdated          int64
	ParentID             string
}

type fixtureOpenCodeMessage struct {
	ID, SessionID, Data string
	TimeCreated         int64
}

type fixtureOpenCodePart struct {
	ID, MessageID, Data string
	TimeCreated         int64
}

func TestListOpenCodeSessionsFiltersByDirectory(t *testing.T) {
	dbPath := registerFixtureOpenCodeDataset(t, fixtureOpenCodeDataset{
		Sessions: []fixtureOpenCodeSession{
			{ID: "s1", Directory: "/home/ani/proj", Title: "alpha", TimeUpdated: 1_700_000_000},
			{ID: "s2", Directory: "/home/ani/other", Title: "beta", TimeUpdated: 1_700_000_010},
			{ID: "s3", Directory: "/home/ani/proj", Title: "gamma", TimeUpdated: 1_700_000_020},
		},
	})

	reader := OpenCodeReader{DriverName: fixtureOpenCodeDriverName}
	sessions, err := reader.ListSessions(dbPath, "/home/ani/proj")
	if err != nil {
		t.Fatalf("ListSessions returned error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].ID != "s1" || sessions[1].ID != "s3" {
		t.Errorf("session ids = %q,%q", sessions[0].ID, sessions[1].ID)
	}
	if want := time.Unix(1_700_000_000, 0).UTC(); !sessions[0].ModifiedTime.Equal(want) {
		t.Errorf("session[0] mtime = %v, want %v", sessions[0].ModifiedTime, want)
	}
}

func TestReadOpenCodeMessagesJoinsParts(t *testing.T) {
	dbPath := registerFixtureOpenCodeDataset(t, fixtureOpenCodeDataset{
		Sessions: []fixtureOpenCodeSession{{ID: "s1", Directory: "/p", Title: "t", TimeUpdated: 1}},
		Messages: []fixtureOpenCodeMessage{
			{ID: "m1", SessionID: "s1", Data: `{"role":"user"}`, TimeCreated: 1_700_000_000},
			{ID: "m2", SessionID: "s1", Data: `{"role":"assistant"}`, TimeCreated: 1_700_000_010},
			{ID: "m3", SessionID: "s1", Data: `{"role":"system"}`, TimeCreated: 1_700_000_020},
			{ID: "m4", SessionID: "other", Data: `{"role":"user"}`, TimeCreated: 1_700_000_030},
		},
		Parts: []fixtureOpenCodePart{
			{ID: "p1", MessageID: "m1", Data: `{"type":"text","text":"hello"}`, TimeCreated: 1_700_000_000},
			{ID: "p2", MessageID: "m2", Data: `{"type":"reasoning","text":"thinking..."}`, TimeCreated: 1_700_000_010},
			{ID: "p3", MessageID: "m2", Data: `{"type":"text","text":"hi back"}`, TimeCreated: 1_700_000_011},
			{ID: "p4", MessageID: "m2", Data: `{"type":"tool","tool":"bash"}`, TimeCreated: 1_700_000_012},
		},
	})

	reader := OpenCodeReader{DriverName: fixtureOpenCodeDriverName}
	messages, err := reader.ReadMessages(dbPath, "s1")
	if err != nil {
		t.Fatalf("ReadMessages returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Content != "hello" {
		t.Errorf("message[0] = %+v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Content != "thinking...\nhi back\nbash" {
		t.Errorf("message[1] = %+v", messages[1])
	}
}

func TestReadOpenCodeMessagesRequiresSessionID(t *testing.T) {
	dbPath := registerFixtureOpenCodeDataset(t, fixtureOpenCodeDataset{})
	reader := OpenCodeReader{DriverName: fixtureOpenCodeDriverName}
	if _, err := reader.ReadMessages(dbPath, ""); err == nil {
		t.Fatalf("ReadMessages expected error for empty session id")
	}
}

func TestReadOpenCodeMessagesToolWithResult(t *testing.T) {
	dbPath := registerFixtureOpenCodeDataset(t, fixtureOpenCodeDataset{
		Sessions: []fixtureOpenCodeSession{{ID: "s1", Directory: "/p", Title: "t", TimeUpdated: 1}},
		Messages: []fixtureOpenCodeMessage{
			{ID: "m1", SessionID: "s1", Data: `{"role":"assistant"}`, TimeCreated: 1_700_000_000},
		},
		Parts: []fixtureOpenCodePart{
			{ID: "p1", MessageID: "m1", Data: `{"type":"tool","tool":"bash","result":"exit 0"}`, TimeCreated: 1_700_000_000},
		},
	})

	reader := OpenCodeReader{DriverName: fixtureOpenCodeDriverName}
	messages, err := reader.ReadMessages(dbPath, "s1")
	if err != nil {
		t.Fatalf("ReadMessages returned error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Content != "bash: exit 0" {
		t.Errorf("content = %q, want %q", messages[0].Content, "bash: exit 0")
	}
}

func TestReadOpenCodeMessagesSubtaskWithText(t *testing.T) {
	dbPath := registerFixtureOpenCodeDataset(t, fixtureOpenCodeDataset{
		Sessions: []fixtureOpenCodeSession{{ID: "s1", Directory: "/p", Title: "t", TimeUpdated: 1}},
		Messages: []fixtureOpenCodeMessage{
			{ID: "m1", SessionID: "s1", Data: `{"role":"assistant"}`, TimeCreated: 1_700_000_000},
		},
		Parts: []fixtureOpenCodePart{
			{ID: "p1", MessageID: "m1", Data: `{"type":"subtask","text":"delegated work"}`, TimeCreated: 1_700_000_000},
		},
	})

	reader := OpenCodeReader{DriverName: fixtureOpenCodeDriverName}
	messages, err := reader.ReadMessages(dbPath, "s1")
	if err != nil {
		t.Fatalf("ReadMessages returned error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Content != "delegated work" {
		t.Errorf("content = %q, want %q", messages[0].Content, "delegated work")
	}
}

func TestIsMissingParentIDColumn(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "missing column", err: fmt.Errorf("query opencode sessions: no such column: parent_id"), want: true},
		{name: "missing column uppercase", err: fmt.Errorf("SQL logic error: no such column: PARENT_ID"), want: true},
		{name: "other error", err: fmt.Errorf("database is locked"), want: false},
		{name: "different missing column", err: fmt.Errorf("no such column: other_col"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMissingParentIDColumn(tc.err); got != tc.want {
				t.Errorf("isMissingParentIDColumn(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestListOpenCodeSessionsWithParentID(t *testing.T) {
	dbPath := registerFixtureOpenCodeDataset(t, fixtureOpenCodeDataset{
		Sessions: []fixtureOpenCodeSession{
			{ID: "parent1", Directory: "/proj", Title: "main", TimeUpdated: 1_700_000_000},
			{ID: "child1", Directory: "/proj", Title: "sub", TimeUpdated: 1_700_000_010, ParentID: "parent1"},
		},
	})

	reader := OpenCodeReader{DriverName: fixtureOpenCodeDriverName}
	sessions, err := reader.ListSessions(dbPath, "/proj")
	if err != nil {
		t.Fatalf("ListSessions returned error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].ParentID != "" {
		t.Errorf("sessions[0].ParentID = %q, want empty", sessions[0].ParentID)
	}
	if sessions[1].ParentID != "parent1" {
		t.Errorf("sessions[1].ParentID = %q, want %q", sessions[1].ParentID, "parent1")
	}
}

type fixtureOpenCodeDriver struct{}

func (fixtureOpenCodeDriver) Open(name string) (driver.Conn, error) {
	fixtureOpenCodeMu.Lock()
	dataset, ok := fixtureOpenCodeDatasets[name]
	fixtureOpenCodeMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("missing opencode dataset for dsn %q", name)
	}
	return &fixtureOpenCodeConn{dataset: dataset}, nil
}

type fixtureOpenCodeConn struct {
	dataset fixtureOpenCodeDataset
}

func (connection *fixtureOpenCodeConn) Prepare(query string) (driver.Stmt, error) {
	return &fixtureOpenCodeStmt{connection: connection, query: query}, nil
}

func (connection *fixtureOpenCodeConn) Close() error { return nil }
func (connection *fixtureOpenCodeConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("transactions are not supported")
}

func (connection *fixtureOpenCodeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return connection.runQuery(query, values)
}

func (connection *fixtureOpenCodeConn) runQuery(query string, args []driver.Value) (driver.Rows, error) {
	lowered := strings.ToLower(strings.TrimSpace(query))
	switch {
	case strings.Contains(lowered, "from session"):
		filter := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				filter = s
			}
		}
		rows := make([][]driver.Value, 0)
		for _, session := range connection.dataset.Sessions {
			if filter != "" && session.Directory != filter {
				continue
			}
			var parentID driver.Value
			if session.ParentID != "" {
				parentID = session.ParentID
			}
			rows = append(rows, []driver.Value{session.ID, session.Directory, session.Title, session.TimeUpdated, parentID})
		}
		return &fixtureRows{columns: []string{"id", "directory", "title", "time_updated", "parent_id"}, rows: rows}, nil
	case strings.Contains(lowered, "from message"):
		filter, _ := args[0].(string)
		rows := make([][]driver.Value, 0)
		for _, message := range connection.dataset.Messages {
			if message.SessionID != filter {
				continue
			}
			rows = append(rows, []driver.Value{message.ID, message.Data, message.TimeCreated})
		}
		return &fixtureRows{columns: []string{"id", "data", "time_created"}, rows: rows}, nil
	case strings.Contains(lowered, "from part"):
		filter, _ := args[0].(string)
		rows := make([][]driver.Value, 0)
		for _, part := range connection.dataset.Parts {
			if part.MessageID != filter {
				continue
			}
			rows = append(rows, []driver.Value{part.Data})
		}
		return &fixtureRows{columns: []string{"data"}, rows: rows}, nil
	}
	return nil, fmt.Errorf("unsupported opencode fixture query: %s", query)
}

type fixtureOpenCodeStmt struct {
	connection *fixtureOpenCodeConn
	query      string
}

func (stmt *fixtureOpenCodeStmt) Close() error  { return nil }
func (stmt *fixtureOpenCodeStmt) NumInput() int { return -1 }
func (stmt *fixtureOpenCodeStmt) Exec(_ []driver.Value) (driver.Result, error) {
	return nil, fmt.Errorf("exec not supported")
}
func (stmt *fixtureOpenCodeStmt) Query(args []driver.Value) (driver.Rows, error) {
	return stmt.connection.runQuery(stmt.query, args)
}

var _ driver.Rows = (*fixtureOpenCodeRows)(nil)

type fixtureOpenCodeRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (rows *fixtureOpenCodeRows) Columns() []string { return rows.columns }
func (rows *fixtureOpenCodeRows) Close() error      { return nil }
func (rows *fixtureOpenCodeRows) Next(dest []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	current := rows.rows[rows.index]
	rows.index++
	copy(dest, current)
	return nil
}

func registerFixtureOpenCodeDataset(t *testing.T, dataset fixtureOpenCodeDataset) string {
	t.Helper()

	registerFixtureOpenCodeDriver.Do(func() {
		sql.Register(fixtureOpenCodeDriverName, fixtureOpenCodeDriver{})
	})

	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	if err := os.WriteFile(dbPath, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	fixtureOpenCodeMu.Lock()
	fixtureOpenCodeDatasets[dbPath] = dataset
	fixtureOpenCodeMu.Unlock()

	t.Cleanup(func() {
		fixtureOpenCodeMu.Lock()
		delete(fixtureOpenCodeDatasets, dbPath)
		fixtureOpenCodeMu.Unlock()
	})

	return dbPath
}
