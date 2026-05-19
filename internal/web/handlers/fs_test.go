package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func callFSExists(t *testing.T, q string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	h := FSExists(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/fs/exists?"+q, nil)
	h(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	return rec, body
}

func TestFSExists_ExistingDir(t *testing.T) {
	dir := t.TempDir()
	rec, body := callFSExists(t, "path="+dir)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if body["exists"] != true {
		t.Errorf("exists = %v, want true", body["exists"])
	}
	if body["is_dir"] != true {
		t.Errorf("is_dir = %v, want true", body["is_dir"])
	}
	if body["absolute"] != filepath.Clean(dir) {
		t.Errorf("absolute = %v, want %s", body["absolute"], filepath.Clean(dir))
	}
}

func TestFSExists_Nonexistent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	rec, body := callFSExists(t, "path="+missing)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if body["exists"] != false {
		t.Errorf("exists = %v, want false", body["exists"])
	}
	if body["is_dir"] != false {
		t.Errorf("is_dir = %v, want false", body["is_dir"])
	}
}

func TestFSExists_RelativeRejected(t *testing.T) {
	rec, body := callFSExists(t, "path=relative/path")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if body["error"] != "path must be absolute" {
		t.Errorf("error = %v, want 'path must be absolute'", body["error"])
	}
}

func TestFSExists_MethodNotAllowed(t *testing.T) {
	h := FSExists(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fs/exists", nil)
	h(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
