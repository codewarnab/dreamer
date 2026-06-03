package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/state"
)

func buildProjectsCfg(t *testing.T) *config.App {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.Abs(root)
	now := time.Now().UTC()

	stA := &state.State{
		Version:       state.StateVersion,
		LastRunUTC:    now,
		ChatHashes:    map[string]string{"a": "1", "b": "2"},
		FindingHashes: []string{"h1", "h2", "h3"},
		Findings: map[string]state.FindingState{
			"h1": {Status: state.FindingStatusApplied, AppliedAt: now},
		},
	}
	stB := &state.State{
		Version:       state.StateVersion,
		LastRunUTC:    time.Time{}, // never run
		ChatHashes:    map[string]string{},
		FindingHashes: []string{},
	}
	if err := state.Save(root, "proj-a", stA); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(root, "proj-b", stB); err != nil {
		t.Fatal(err)
	}
	return &config.App{
		Projects: []config.ProjectConfig{
			{Name: "proj-a", Path: "/tmp/proj-a", Since: "2026-01-01"},
			{Name: "proj-b", Path: "/tmp/proj-b"},
		},
		Daemon: config.DaemonConfig{FrequencySeconds: 3600, OutputRoot: root},
	}
}

func TestProjectsList(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectsList(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp projectsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Projects) != 2 {
		t.Fatalf("len(projects) = %d, want 2", len(resp.Projects))
	}
	byName := map[string]ProjectRollup{}
	for _, p := range resp.Projects {
		byName[p.Name] = p
	}
	a := byName["proj-a"]
	if a.ChatsCount != 2 {
		t.Errorf("proj-a chats_count = %d, want 2", a.ChatsCount)
	}
	if a.FindingsApplied != 1 {
		t.Errorf("proj-a findings_applied = %d, want 1", a.FindingsApplied)
	}
	if a.FindingsOpen != 2 {
		t.Errorf("proj-a findings_open = %d, want 2", a.FindingsOpen)
	}
	if a.Since != "2026-01-01" {
		t.Errorf("proj-a since = %q, want 2026-01-01", a.Since)
	}
	if a.LastRunUTC == "" || a.NextRunUTC == "" {
		t.Errorf("proj-a last_run/next_run empty: %+v", a)
	}
	b := byName["proj-b"]
	if b.LastRunUTC != "" {
		t.Errorf("proj-b last_run_utc = %q, want empty", b.LastRunUTC)
	}
}

func TestProjectDetail_Found(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectDetail(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var rollup ProjectRollup
	if err := json.Unmarshal(rec.Body.Bytes(), &rollup); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rollup.Name != "proj-a" || rollup.ChatsCount != 2 || rollup.FindingsApplied != 1 {
		t.Errorf("rollup mismatch: %+v", rollup)
	}
}

func TestProjectDetail_Unknown(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectDetail(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestProjectDetail_RejectSubpath(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectDetail(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-a/findings", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (sub-path is E.16's job)", rec.Code)
	}
}

func TestProjectDelete_RemovesFromConfig(t *testing.T) {
	cfg := buildProjectsCfg(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	// Seed a config.yaml with a leading comment to prove comment preservation.
	seed := "# dreamer config\nprojects:\n" +
		"  - name: proj-a\n    path: /tmp/proj-a\n    since: 2026-01-01\n" +
		"  - name: proj-b\n    path: /tmp/proj-b\n" +
		"daemon:\n  frequency_seconds: 3600\n"
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	h := ProjectDelete(Deps{
		Config:     func() *config.App { return cfg },
		ConfigPath: func() string { return configPath },
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/projects/proj-a", nil)
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["removed"] != "proj-a" {
		t.Fatalf("removed = %v, want proj-a", resp["removed"])
	}

	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "proj-a") {
		t.Errorf("config still contains proj-a after delete:\n%s", got)
	}
	if !strings.Contains(got, "proj-b") {
		t.Errorf("config lost proj-b after delete:\n%s", got)
	}
	if !strings.Contains(got, "# dreamer config") {
		t.Errorf("comment not preserved after delete:\n%s", got)
	}
}

func TestProjectDelete_ClearsOverlayProjects(t *testing.T) {
	cfg := buildProjectsCfg(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	overlayPath := filepath.Join(dir, "ui-overrides.yaml")
	seed := "projects:\n  - name: proj-a\n    path: /tmp/proj-a\n  - name: proj-b\n    path: /tmp/proj-b\n"
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	// Overlay carries a stale projects list plus an unrelated key we must keep.
	overlay := "default_provider: claude\nprojects:\n  - name: proj-a\n    path: /tmp/proj-a\n"
	if err := os.WriteFile(overlayPath, []byte(overlay), 0o644); err != nil {
		t.Fatal(err)
	}

	h := ProjectDelete(Deps{
		Config:      func() *config.App { return cfg },
		ConfigPath:  func() string { return configPath },
		OverlayPath: func() string { return overlayPath },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodDelete, "/api/projects/proj-a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}

	ov, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(ov)
	if strings.Contains(got, "projects") {
		t.Errorf("overlay still has projects key after delete:\n%s", got)
	}
	if !strings.Contains(got, "default_provider") {
		t.Errorf("overlay lost unrelated key after delete:\n%s", got)
	}
}

func TestProjectDelete_NoConfigPath(t *testing.T) {
	cfg := buildProjectsCfg(t)
	// No ConfigPath wired (standalone read-only mode) → 503.
	h := ProjectDelete(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodDelete, "/api/projects/proj-a", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestProjectDelete_UnknownProject(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectDelete(Deps{
		Config:     func() *config.App { return cfg },
		ConfigPath: func() string { return filepath.Join(t.TempDir(), "config.yaml") },
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/projects/nonexistent", nil)
	h(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestProjectDelete_RejectSubpath(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectDelete(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/projects/proj-a/findings", nil)
	h(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestProjectDelete_MethodNotAllowed(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := ProjectDelete(Deps{Config: func() *config.App { return cfg }})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/proj-a", nil)
	h(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestProjectsPost_Success(t *testing.T) {
	cfg := buildProjectsCfg(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	seed := "# dreamer config\nprojects:\n" +
		"  - name: proj-a\n    path: /tmp/proj-a\n    since: 2026-01-01\n"
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Projects(Deps{
		Config:     func() *config.App { return cfg },
		ConfigPath: func() string { return configPath },
	})

	projectDir := t.TempDir() // a valid existing directory path
	reqBody := `{"name": "proj-new", "path": "` + filepath.ToSlash(projectDir) + `", "since": "12h"}`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(reqBody))
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["ok"] != true || resp["name"] != "proj-new" || resp["since"] != "12h" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Verify config.yaml was updated
	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "name: proj-new") || !strings.Contains(got, "since: 12h") {
		t.Fatalf("config not updated with new project:\n%s", got)
	}
}

func TestProjectsPost_HomeExpanded(t *testing.T) {
	cfg := buildProjectsCfg(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	seed := "# dreamer config\nprojects: []\n"
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Projects(Deps{
		Config:     func() *config.App { return cfg },
		ConfigPath: func() string { return configPath },
	})

	// Set home directory to a temp location we control.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	projDir := filepath.Join(home, "proj-tilde")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// We pass ~/proj-tilde which should expand to projDir.
	reqBody := `{"name": "proj-tilde", "path": "~/proj-tilde", "since": "24h"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(reqBody))
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Verify config.yaml was updated with the expanded absolute path
	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	expectedPath := filepath.ToSlash(projDir)
	if !strings.Contains(filepath.ToSlash(got), expectedPath) {
		t.Fatalf("config path not expanded in file:\n%s\nExpected to contain: %s", got, expectedPath)
	}
}

func TestProjectsPost_Validation(t *testing.T) {
	cfg := buildProjectsCfg(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	seed := "projects: []\n"
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Projects(Deps{
		Config:     func() *config.App { return cfg },
		ConfigPath: func() string { return configPath },
	})

	tests := []struct {
		name string
		body string
		code int
	}{
		{"empty path", `{"name": "x", "path": ""}`, http.StatusBadRequest},
		{"nonexistent path", `{"name": "x", "path": "/nonexistent/xyz/path"}`, http.StatusBadRequest},
		{"path is a file", `{"name": "x", "path": "` + filepath.ToSlash(configPath) + `"}`, http.StatusBadRequest},
		{"invalid project name backslash", `{"name": "a\\b", "path": "` + filepath.ToSlash(dir) + `"}`, http.StatusBadRequest},
		{"invalid project name windows reserved", `{"name": "NUL", "path": "` + filepath.ToSlash(dir) + `"}`, http.StatusBadRequest},
		{"invalid since window", `{"name": "x", "path": "` + filepath.ToSlash(dir) + `", "since": "invalid"}`, http.StatusBadRequest},
		{"invalid json", `not-json`, http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(tc.body))
			h(rec, req)
			if rec.Code != tc.code {
				t.Errorf("status = %d, want %d; body=%s", rec.Code, tc.code, rec.Body.String())
			}
		})
	}
}

func TestProjectsPost_Duplicate(t *testing.T) {
	cfg := buildProjectsCfg(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dupDir := t.TempDir()
	seed := "projects:\n  - name: proj-a\n    path: " + filepath.ToSlash(dupDir) + "\n"
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Projects(Deps{
		Config:     func() *config.App { return cfg },
		ConfigPath: func() string { return configPath },
	})

	tests := []struct {
		name string
		body string
	}{
		{"duplicate name", `{"name": "proj-a", "path": "` + filepath.ToSlash(dir) + `"}`},
		{"duplicate path", `{"name": "proj-diff", "path": "` + filepath.ToSlash(dupDir) + `"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(tc.body))
			h(rec, req)
			if rec.Code != http.StatusConflict {
				t.Errorf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestProjectsPost_SizeLimit(t *testing.T) {
	cfg := buildProjectsCfg(t)
	h := Projects(Deps{
		Config:     func() *config.App { return cfg },
		ConfigPath: func() string { return "/dummy/config.yaml" },
	})
	// Body size > 4 KiB
	largeBody := `{"name": "x", "path": "` + strings.Repeat("a", 5000) + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(largeBody))
	h(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
