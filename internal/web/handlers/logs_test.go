package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/config"
)

func seedLog(t *testing.T, root, contents string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "dreamer.log"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}

func TestLogsTail_ReturnsAllLines(t *testing.T) {
	root := t.TempDir()
	contents := "level=info ts=1 msg=hello\n" +
		"level=warn ts=2 msg=careful\n" +
		"level=error ts=3 msg=boom\n" +
		"level=debug ts=4 msg=trace\n" +
		"level=info ts=5 msg=done\n"
	seedLog(t, root, contents)

	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: root}, Web: config.WebConfig{LogTailKB: 256}}
	h := LogsTail(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/logs/tail", nil)
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain*", ct)
	}
	body := rec.Body.String()
	for _, marker := range []string{"hello", "careful", "boom", "trace", "done"} {
		if !strings.Contains(body, marker) {
			t.Errorf("body missing %q: %s", marker, body)
		}
	}
}

func TestLogsTail_FiltersByLevel(t *testing.T) {
	root := t.TempDir()
	contents := "level=info ts=1 msg=hello\n" +
		"level=warn ts=2 msg=careful\n" +
		"level=error ts=3 msg=boom\n" +
		"level=debug ts=4 msg=trace\n" +
		"level=info ts=5 msg=done\n"
	seedLog(t, root, contents)
	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: root}, Web: config.WebConfig{LogTailKB: 256}}
	h := LogsTail(Deps{Config: func() *config.App { return cfg }})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/logs/tail?level=error", nil)
	h(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "boom") {
		t.Errorf("expected error line, got: %s", body)
	}
	if strings.Contains(body, "hello") || strings.Contains(body, "trace") {
		t.Errorf("level=error must not include other lines, got: %s", body)
	}
}

func TestLogsTail_HXRequestWrapsPre(t *testing.T) {
	root := t.TempDir()
	seedLog(t, root, "level=info ts=1 msg=hello\n")
	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: root}, Web: config.WebConfig{LogTailKB: 256}}
	h := LogsTail(Deps{Config: func() *config.App { return cfg }})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/logs/tail", nil)
	req.Header.Set("HX-Request", "true")
	h(rec, req)
	body := rec.Body.String()
	if !strings.HasPrefix(body, "<pre>") || !strings.HasSuffix(body, "</pre>") {
		t.Errorf("expected <pre>...</pre>, got: %s", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html*", ct)
	}
}

func TestLogsTail_DropsPartialFirstLine(t *testing.T) {
	root := t.TempDir()
	// Build a file > 1 KB so we trip the truncation path.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("level=info ts=X msg=pad-line-data\n")
	}
	b.WriteString("level=info ts=last msg=tail-marker\n")
	seedLog(t, root, b.String())

	// 1 KB tail forces truncation; first partial line must be dropped.
	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: root}, Web: config.WebConfig{LogTailKB: 1}}
	h := LogsTail(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/logs/tail", nil)
	h(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "tail-marker") {
		t.Errorf("expected tail-marker present, got: %s", body)
	}
	// First char must be 'l' (start of "level=...") since we dropped the partial.
	if len(body) == 0 || body[0] != 'l' {
		head := body
		if len(head) > 40 {
			head = head[:40]
		}
		t.Errorf("first byte not start of line; head=%q", head)
	}
}

func TestLogsTail_MissingFileOK(t *testing.T) {
	root := t.TempDir()
	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: root}, Web: config.WebConfig{LogTailKB: 256}}
	h := LogsTail(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/logs/tail", nil)
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty body)", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}
