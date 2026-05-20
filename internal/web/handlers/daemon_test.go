package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDaemonRestart_NoHook(t *testing.T) {
	h := DaemonRestart(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/daemon/restart", nil)
	h(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestDaemonRestart_MethodNotAllowed(t *testing.T) {
	h := DaemonRestart(Deps{RestartDaemon: func() error { return nil }})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/daemon/restart", nil)
	h(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestDaemonRestart_InvokesHook(t *testing.T) {
	fired := make(chan struct{}, 1)
	h := DaemonRestart(Deps{RestartDaemon: func() error {
		fired <- struct{}{}
		return nil
	}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/daemon/restart", nil)
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if body["message"] != "daemon restart triggered" {
		t.Errorf("message = %v", body["message"])
	}
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("restart hook never fired")
	}
}
