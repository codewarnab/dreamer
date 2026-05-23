package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"dreamer/internal/config"
)

func TestParseSinceWindow(t *testing.T) {
	cases := []struct {
		in        string
		wantOK    bool
		wantHours float64
	}{
		{"", false, 0},
		{"lifetime", false, 0},
		{"24h", true, 24},
		{"7d", true, 7 * 24},
		{"1w", true, 7 * 24},
		{"1mo", true, 30 * 24},
		{"30m", true, 0.5},
		{"garbage", false, 0},
		{"0h", false, 0},
	}
	for _, c := range cases {
		got, ok := parseSinceWindow(c.in)
		if ok != c.wantOK {
			t.Errorf("parseSinceWindow(%q) ok=%v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if ok && got.Hours() != c.wantHours {
			t.Errorf("parseSinceWindow(%q) = %v, want %vh", c.in, got, c.wantHours)
		}
	}
}

func TestChatsProjectName(t *testing.T) {
	cases := map[string]string{
		"/api/projects/foo/chats":    "foo",
		"/api/projects/foo/chats/":   "foo",
		"/api/projects/foo":          "",
		"/api/projects/":             "",
		"/api/projects/foo/findings": "",
		"/other":                     "",
	}
	for path, want := range cases {
		if got := chatsProjectName(path); got != want {
			t.Errorf("chatsProjectName(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestProjectChats_Discovery exercises the full handler against a real
// chat.DiscoverChats run with a fake $HOME containing one copilot session
// file. Copilot discovery is unscoped — every file under
// $HOME/.copilot/session-state matches — so we can seed it without caring
// about the project path.
func TestProjectChats_Discovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME-based fixture is POSIX-flavored; copilot discovery uses HomeDir")
	}
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	sessionDir := filepath.Join(fakeHome, ".copilot", "session-state")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	recent := filepath.Join(sessionDir, "recent.jsonl")
	old := filepath.Join(sessionDir, "old.jsonl")
	if err := os.WriteFile(recent, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	projectPath := t.TempDir()
	cfg := &config.Config{
		Projects: []config.ProjectConfig{
			{Name: "p1", Path: projectPath, Since: "24h"},
		},
	}
	h := ProjectChats(Deps{Config: func() *config.Config { return cfg }})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/chats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp chatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sources) != 2 {
		t.Fatalf("len(sources) = %d, want 2; body=%s", len(resp.Sources), rec.Body.String())
	}
	byPath := map[string]ChatSourceDTO{}
	for _, s := range resp.Sources {
		byPath[s.Path] = s
		if s.Tool != "copilot-session-jsonl" {
			t.Errorf("tool = %q, want copilot-session-jsonl", s.Tool)
		}
		if s.MessageCount != -1 {
			t.Errorf("message_count = %d, want -1", s.MessageCount)
		}
	}
	if !byPath[recent].Included {
		t.Errorf("recent should be included within 24h window")
	}
	if byPath[old].Included {
		t.Errorf("old (72h) should NOT be included within 24h window")
	}
}

func TestProjectChats_ToolFilter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME-based fixture is POSIX-flavored")
	}
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	sessionDir := filepath.Join(fakeHome, ".copilot", "session-state")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "a.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "p1", Path: t.TempDir(), Since: "lifetime"}},
	}
	h := ProjectChats(Deps{Config: func() *config.Config { return cfg }})

	// Matching filter keeps the source.
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/chats?tool=copilot-session-jsonl", nil))
	var resp chatsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Sources) != 1 {
		t.Errorf("filter match: len=%d, want 1", len(resp.Sources))
	}

	// Non-matching filter drops it.
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/chats?tool=claude-code-session-jsonl", nil))
	resp = chatsResponse{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Sources) != 0 {
		t.Errorf("filter mismatch: len=%d, want 0", len(resp.Sources))
	}

	// Lifetime since: every source is included=true.
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/chats", nil))
	resp = chatsResponse{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Sources) != 1 || !resp.Sources[0].Included {
		t.Errorf("lifetime: want 1 included source, got %+v", resp.Sources)
	}
}

func TestProjectChats_UnknownProject(t *testing.T) {
	cfg := &config.Config{Projects: []config.ProjectConfig{{Name: "p1", Path: "/tmp/x"}}}
	h := ProjectChats(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/nope/chats", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestDeleteProjectChat_PathInjection asserts the handler's security
// boundary: any path the user supplies must appear in the live DiscoverChats
// output for the project, otherwise the request is rejected with 404 before
// any provider DeleteSource call. The project path is a fresh t.TempDir so
// discovery for non-home-rooted providers finds nothing, and the supplied
// paths point outside the project — they must all be rejected.
func TestDeleteProjectChat_PathInjection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	projectDir := t.TempDir()
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "proj", Path: projectDir}},
		Daemon:   config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	handler := ProjectChats(Deps{Config: func() *config.Config { return cfg }})

	cases := []struct {
		name string
		body string
	}{
		{name: "absolute path outside project", body: `{"path": "/etc/passwd"}`},
		{name: "windows system path", body: `{"path": "C:\\Windows\\System32\\drivers\\etc\\hosts"}`},
		{name: "relative traversal", body: `{"path": "../../secret.txt"}`},
		{name: "random temp path", body: `{"path": "/tmp/nothing-here.jsonl"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, "/api/projects/proj/chats", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d (want 404); body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDeleteProjectChat_EmptyPathReturns400(t *testing.T) {
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "proj", Path: t.TempDir()}},
		Daemon:   config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	handler := ProjectChats(Deps{Config: func() *config.Config { return cfg }})
	req := httptest.NewRequest(http.MethodDelete, "/api/projects/proj/chats", strings.NewReader(`{"path": ""}`))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d (want 400); body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteProjectChat_InvalidJSONReturns400(t *testing.T) {
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "proj", Path: t.TempDir()}},
		Daemon:   config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	handler := ProjectChats(Deps{Config: func() *config.Config { return cfg }})
	req := httptest.NewRequest(http.MethodDelete, "/api/projects/proj/chats", strings.NewReader(`not-json`))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d (want 400); body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteProjectChat_UnknownProjectReturns404(t *testing.T) {
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "other", Path: t.TempDir()}},
		Daemon:   config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	handler := ProjectChats(Deps{Config: func() *config.Config { return cfg }})
	req := httptest.NewRequest(http.MethodDelete, "/api/projects/missing/chats", strings.NewReader(`{"path":"/tmp/x"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d (want 404); body=%s", rec.Code, rec.Body.String())
	}
}

// TestBulkDeleteProjectChats_PathInjection verifies the bulk endpoint applies
// the same per-path validation: every reported failure carries "not found"
// when the supplied path is not in DiscoverChats output.
func TestBulkDeleteProjectChats_PathInjection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "proj", Path: t.TempDir()}},
		Daemon:   config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	handler := ProjectChatsBulkDelete(Deps{Config: func() *config.Config { return cfg }})
	body := `{"paths": ["/etc/passwd", "C:/Windows/System32/cmd.exe"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/proj/chats:bulk-delete", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (want 200 with per-path failures); body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"failed":2`) {
		t.Errorf("expected failed=2 in summary, got body=%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"deleted":0`) {
		t.Errorf("expected deleted=0 in summary, got body=%s", rec.Body.String())
	}
}

func TestBulkDeleteProjectName(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/projects/foo/chats:bulk-delete", "foo"},
		{"/api/projects/with-dashes/chats:bulk-delete", "with-dashes"},
		{"/api/projects/foo/chats", ""},
		{"/api/projects//chats:bulk-delete", ""},
		{"/api/projects/foo/bar/chats:bulk-delete", ""},
		{"/api/projects", ""},
	}
	for _, tc := range cases {
		if got := bulkDeleteProjectName(tc.path); got != tc.want {
			t.Errorf("bulkDeleteProjectName(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
