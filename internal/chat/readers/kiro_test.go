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
)

const fixtureKiroDriverName = "dreamer-fixture-kiro"

var (
	registerFixtureKiroDriver sync.Once
	fixtureKiroMu             sync.Mutex
	fixtureKiroDatasets       = map[string]fixtureKiroDataset{}
)

type fixtureKiroDataset struct {
	Conversations []fixtureKiroConversation
}

type fixtureKiroConversation struct {
	Key, ConversationID, Value string
	UpdatedAt                  int64
}

func TestListKiroConversationsFiltersByDirectory(t *testing.T) {
	dbPath := registerFixtureKiroDataset(t, fixtureKiroDataset{
		Conversations: []fixtureKiroConversation{
			{Key: "/home/ani/proj", ConversationID: "c1", UpdatedAt: 1_700_000_000},
			{Key: "/home/ani/other", ConversationID: "c2", UpdatedAt: 1_700_000_010},
			{Key: "/home/ani/proj", ConversationID: "c3", UpdatedAt: 1_700_000_020},
		},
	})

	reader := KiroReader{DriverName: fixtureKiroDriverName}
	conversations, err := reader.ListConversations(dbPath, "/home/ani/proj")
	if err != nil {
		t.Fatalf("ListConversations returned error: %v", err)
	}
	if len(conversations) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(conversations))
	}
	if conversations[0].ConversationID != "c1" || conversations[1].ConversationID != "c3" {
		t.Errorf("conversation ids = %q,%q", conversations[0].ConversationID, conversations[1].ConversationID)
	}
}

func TestReadKiroConversationParsesHistory(t *testing.T) {
	value := `{
        "conversation_id":"c1",
        "history":[
            {"user":{"content":{"Prompt":{"prompt":"hi"}},"timestamp":"2026-03-24T02:05:17Z"},"assistant":{"Response":{"message_id":"m1","content":"hello back"}}},
            {"user":{"content":{"Prompt":{"prompt":""}}},"assistant":{"Response":{"message_id":"m2","content":""}}},
            {"user":{"content":{"Prompt":{"prompt":"next"}}},"assistant":{"Response":{"message_id":"m3","content":"reply"}}}
        ]
    }`
	dbPath := registerFixtureKiroDataset(t, fixtureKiroDataset{
		Conversations: []fixtureKiroConversation{
			{Key: "/home/ani/proj", ConversationID: "c1", Value: value, UpdatedAt: 1_700_000_000},
		},
	})

	reader := KiroReader{DriverName: fixtureKiroDriverName}
	messages, err := reader.ReadConversation(dbPath, "c1")
	if err != nil {
		t.Fatalf("ReadConversation returned error: %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Content != "hi" {
		t.Errorf("message[0] = %+v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Content != "hello back" {
		t.Errorf("message[1] = %+v", messages[1])
	}
	if messages[2].Content != "next" {
		t.Errorf("message[2] = %+v", messages[2])
	}
	if messages[3].Content != "reply" {
		t.Errorf("message[3] = %+v", messages[3])
	}
}

func TestReadKiroConversationRequiresID(t *testing.T) {
	dbPath := registerFixtureKiroDataset(t, fixtureKiroDataset{})
	reader := KiroReader{DriverName: fixtureKiroDriverName}
	if _, err := reader.ReadConversation(dbPath, ""); err == nil {
		t.Fatalf("ReadConversation expected error for empty id")
	}
}

func TestConversationFingerprintStableAndDistinctPerConversation(t *testing.T) {
	dbPath := registerFixtureKiroDataset(t, fixtureKiroDataset{
		Conversations: []fixtureKiroConversation{
			{Key: "/p", ConversationID: "c1", Value: `{"history":[1]}`, UpdatedAt: 1_700_000_000},
			{Key: "/p", ConversationID: "c2", Value: `{"history":[2]}`, UpdatedAt: 1_700_000_005},
		},
	})

	reader := KiroReader{DriverName: fixtureKiroDriverName}
	first, err := reader.ConversationFingerprint(dbPath, "c1")
	if err != nil {
		t.Fatalf("ConversationFingerprint(c1) returned error: %v", err)
	}
	second, err := reader.ConversationFingerprint(dbPath, "c1")
	if err != nil {
		t.Fatalf("ConversationFingerprint(c1) second call returned error: %v", err)
	}
	if first == "" {
		t.Fatal("fingerprint must not be empty")
	}
	if first != second {
		t.Fatalf("fingerprint unstable across calls: %q vs %q", first, second)
	}

	other, err := reader.ConversationFingerprint(dbPath, "c2")
	if err != nil {
		t.Fatalf("ConversationFingerprint(c2) returned error: %v", err)
	}
	if other == first {
		t.Fatal("distinct conversations sharing one database must get distinct fingerprints")
	}
}

func TestConversationFingerprintChangesWhenContentChanges(t *testing.T) {
	base := fixtureKiroConversation{Key: "/p", ConversationID: "c1", Value: `{"history":[1]}`, UpdatedAt: 1_700_000_000}
	reader := KiroReader{DriverName: fixtureKiroDriverName}

	before, err := reader.ConversationFingerprint(registerFixtureKiroDataset(t, fixtureKiroDataset{Conversations: []fixtureKiroConversation{base}}), "c1")
	if err != nil {
		t.Fatalf("baseline ConversationFingerprint returned error: %v", err)
	}

	longerValue := base
	longerValue.Value = `{"history":[1,2]}`
	afterValue, err := reader.ConversationFingerprint(registerFixtureKiroDataset(t, fixtureKiroDataset{Conversations: []fixtureKiroConversation{longerValue}}), "c1")
	if err != nil {
		t.Fatalf("post-value ConversationFingerprint returned error: %v", err)
	}
	if afterValue == before {
		t.Fatal("changing the conversation value must change the fingerprint")
	}

	bumpedStamp := base
	bumpedStamp.UpdatedAt = 1_700_050_000
	afterStamp, err := reader.ConversationFingerprint(registerFixtureKiroDataset(t, fixtureKiroDataset{Conversations: []fixtureKiroConversation{bumpedStamp}}), "c1")
	if err != nil {
		t.Fatalf("post-stamp ConversationFingerprint returned error: %v", err)
	}
	if afterStamp == before {
		t.Fatal("bumping updated_at must change the fingerprint even at equal value length")
	}
}

func TestConversationFingerprintRequiresID(t *testing.T) {
	dbPath := registerFixtureKiroDataset(t, fixtureKiroDataset{})
	reader := KiroReader{DriverName: fixtureKiroDriverName}
	if _, err := reader.ConversationFingerprint(dbPath, ""); err == nil {
		t.Fatalf("ConversationFingerprint expected error for empty id")
	}
}

func TestConversationFingerprintMissingConversation(t *testing.T) {
	dbPath := registerFixtureKiroDataset(t, fixtureKiroDataset{})
	reader := KiroReader{DriverName: fixtureKiroDriverName}
	if _, err := reader.ConversationFingerprint(dbPath, "ghost"); err == nil {
		t.Fatalf("ConversationFingerprint expected error for missing conversation row")
	}
}

func TestReadKiroConversationReturnsParseError(t *testing.T) {
	dbPath := registerFixtureKiroDataset(t, fixtureKiroDataset{
		Conversations: []fixtureKiroConversation{
			{Key: "/home/ani/proj", ConversationID: "bad", Value: "{not valid json", UpdatedAt: 1_700_000_000},
		},
	})

	reader := KiroReader{DriverName: fixtureKiroDriverName}
	_, err := reader.ReadConversation(dbPath, "bad")
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse kiro conversation") {
		t.Fatalf("error = %v, want parse kiro conversation context", err)
	}
}

type fixtureKiroDriver struct{}

func (fixtureKiroDriver) Open(name string) (driver.Conn, error) {
	fixtureKiroMu.Lock()
	dataset, ok := fixtureKiroDatasets[name]
	fixtureKiroMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("missing kiro dataset for dsn %q", name)
	}
	return &fixtureKiroConn{dataset: dataset}, nil
}

type fixtureKiroConn struct {
	dataset fixtureKiroDataset
}

func (connection *fixtureKiroConn) Prepare(query string) (driver.Stmt, error) {
	return &fixtureKiroStmt{connection: connection, query: query}, nil
}
func (connection *fixtureKiroConn) Close() error { return nil }
func (connection *fixtureKiroConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("transactions are not supported")
}

func (connection *fixtureKiroConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return connection.runQuery(query, values)
}

func (connection *fixtureKiroConn) runQuery(query string, args []driver.Value) (driver.Rows, error) {
	lowered := strings.ToLower(strings.TrimSpace(query))
	switch {
	// ConversationFingerprint must be matched before the generic
	// conversations_v2 case below: it selects aggregates for one row.
	case strings.Contains(lowered, "length(cast(value as blob))"):
		filter, _ := args[0].(string)
		rows := make([][]driver.Value, 0)
		for _, conversation := range connection.dataset.Conversations {
			if conversation.ConversationID == filter {
				rows = append(rows, []driver.Value{int64(len(conversation.Value)), conversation.UpdatedAt})
				break
			}
		}
		return &fixtureRows{columns: []string{"bytes", "updated_at"}, rows: rows}, nil
	case strings.Contains(lowered, "select value"):
		filter, _ := args[0].(string)
		rows := make([][]driver.Value, 0)
		for _, conversation := range connection.dataset.Conversations {
			if conversation.ConversationID == filter {
				rows = append(rows, []driver.Value{conversation.Value})
				break
			}
		}
		return &fixtureRows{columns: []string{"value"}, rows: rows}, nil
	case strings.Contains(lowered, "from conversations_v2"):
		filter := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				filter = s
			}
		}
		rows := make([][]driver.Value, 0)
		for _, conversation := range connection.dataset.Conversations {
			if filter != "" && conversation.Key != filter {
				continue
			}
			rows = append(rows, []driver.Value{conversation.Key, conversation.ConversationID, conversation.UpdatedAt})
		}
		return &fixtureRows{columns: []string{"key", "conversation_id", "updated_at"}, rows: rows}, nil
	}
	return nil, fmt.Errorf("unsupported kiro fixture query: %s", query)
}

type fixtureKiroStmt struct {
	connection *fixtureKiroConn
	query      string
}

func (stmt *fixtureKiroStmt) Close() error  { return nil }
func (stmt *fixtureKiroStmt) NumInput() int { return -1 }
func (stmt *fixtureKiroStmt) Exec(_ []driver.Value) (driver.Result, error) {
	return nil, fmt.Errorf("exec not supported")
}
func (stmt *fixtureKiroStmt) Query(args []driver.Value) (driver.Rows, error) {
	return stmt.connection.runQuery(stmt.query, args)
}

func registerFixtureKiroDataset(t *testing.T, dataset fixtureKiroDataset) string {
	t.Helper()

	registerFixtureKiroDriver.Do(func() {
		sql.Register(fixtureKiroDriverName, fixtureKiroDriver{})
	})

	dbPath := filepath.Join(t.TempDir(), "kiro.db")
	if err := os.WriteFile(dbPath, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	fixtureKiroMu.Lock()
	fixtureKiroDatasets[dbPath] = dataset
	fixtureKiroMu.Unlock()

	t.Cleanup(func() {
		fixtureKiroMu.Lock()
		delete(fixtureKiroDatasets, dbPath)
		fixtureKiroMu.Unlock()
	})

	return dbPath
}

// ensure io.EOF is referenced (Next handled by shared fixtureRows from sqlite_test.go)
var _ = io.EOF
