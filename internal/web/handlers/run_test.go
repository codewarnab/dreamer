package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dreamer/internal/config"
)

func TestRun_AcceptedReturns202(t *testing.T) {
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "proj", Path: t.TempDir()}},
		Daemon:   config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	deps := Deps{
		Config:     func() *config.Config { return cfg },
		EnqueueRun: func(name string) (string, bool, error) { return "rid", true, nil },
	}
	r := httptest.NewRequest("POST", "/api/projects/proj/run", nil)
	w := httptest.NewRecorder()
	Run(deps)(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

func TestRun_ConflictReturns409(t *testing.T) {
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "proj", Path: t.TempDir()}},
		Daemon:   config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	deps := Deps{
		Config:     func() *config.Config { return cfg },
		EnqueueRun: func(name string) (string, bool, error) { return "", false, nil },
	}
	r := httptest.NewRequest("POST", "/api/projects/proj/run", nil)
	w := httptest.NewRecorder()
	Run(deps)(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

func TestRun_UnknownProject404(t *testing.T) {
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "other", Path: t.TempDir()}},
		Daemon:   config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	deps := Deps{
		Config:     func() *config.Config { return cfg },
		EnqueueRun: func(name string) (string, bool, error) { return "id", true, nil },
	}
	r := httptest.NewRequest("POST", "/api/projects/proj/run", nil)
	w := httptest.NewRecorder()
	Run(deps)(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}
