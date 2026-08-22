package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/capture"
	"dreamer/internal/config"
	"dreamer/internal/logging"
)

// seedCaptureProject creates a configured project plus one captured run
// containing a single parse-failed phase-1 call. Returns the config and
// the seeded run ID.
func seedCaptureProject(t *testing.T, responseBody string) (*config.App, string) {
	t.Helper()
	root := t.TempDir()
	projDir := filepath.Join(root, "proj-a")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.App{
		Projects: []config.ProjectConfig{{Name: "proj-a", Path: projDir}},
		Daemon:   config.DaemonConfig{OutputRoot: root, FrequencySeconds: 3600},
	}

	w, err := capture.Open(capture.RunDir(root, "proj-a", "abcd1234"), capture.RunMeta{
		RunID:         "abcd1234",
		ProjectName:   "proj-a",
		ProjectPath:   projDir,
		ProviderID:    "claude-cli",
		Model:         "claude-sonnet",
		Sandbox:       "auto",
		SystemMessage: "sys",
	})
	if err != nil {
		t.Fatalf("capture.Open: %v", err)
	}
	defer func() { _ = w.Close() }()
	rec := capture.Record{Phase: capture.PhasePhase1, ChunkIndex: 0, ChunkCount: 1, Prompt: "captured prompt", Response: responseBody}
	rec.Status = capture.StatusOK
	if responseBody == `{"summary": "broken"n` {
		rec.Status = capture.StatusParseFailed
		rec.Error = "invalid phase-1 JSON: invalid character 'n' after object key:value pair"
	}
	if err := w.Append(rec); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return cfg, "abcd1234"
}

const goodCapturedBody = `{"summary":"done","mistakes":{"test":[{"category":"test","summary":"missing edge-case test","evidence_excerpt":"no test","confidence":0.9}]}}`

func runsDeps(cfg *config.App) Deps {
	return Deps{
		Config: func() *config.App { return cfg },
		Logger: logging.Silent(),
	}
}

func TestProjectRuns_ListsSeededRunWithDerivedStatus(t *testing.T) {
	cfg, runID := seedCaptureProject(t, `{"summary": "broken"n`)
	r := httptest.NewRequest(http.MethodGet, "/api/projects/proj-a/runs", nil)
	w := httptest.NewRecorder()
	ProjectRuns(runsDeps(cfg))(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Runs []capture.RunSummary `json:"runs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].RunID != runID {
		t.Fatalf("runs = %+v", got.Runs)
	}
	if got.Runs[0].Status != capture.StatusParseFailed {
		t.Fatalf("derived status = %q, want parse_failed", got.Runs[0].Status)
	}
	if got.Runs[0].Calls != 1 {
		t.Fatalf("calls = %d, want 1", got.Runs[0].Calls)
	}
}

func TestRunDetail_SummariesOmitBodiesAndCallParamReturnsFull(t *testing.T) {
	cfg, runID := seedCaptureProject(t, goodCapturedBody)

	r := httptest.NewRequest(http.MethodGet, "/api/projects/proj-a/runs/"+runID, nil)
	w := httptest.NewRecorder()
	RunDetail(runsDeps(cfg))(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !json.Valid([]byte(body)) {
		t.Fatal("detail response is not JSON")
	}
	var listing struct {
		Meta  capture.RunMeta   `json:"meta"`
		Calls []callSummaryView `json:"calls"`
	}
	if err := json.Unmarshal([]byte(body), &listing); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(listing.Calls) != 1 || listing.Calls[0].PromptBytes != len("captured prompt") {
		t.Fatalf("calls = %+v", listing.Calls)
	}
	if strings.Contains(body, `"prompt"`) || strings.Contains(body, `"response"`) {
		t.Fatal("listing must not carry full prompt/response bodies")
	}
	if listing.Meta.ProviderID != "claude-cli" || listing.Meta.Model != "claude-sonnet" {
		t.Fatalf("meta = %+v", listing.Meta)
	}

	fullReq := httptest.NewRequest(http.MethodGet, "/api/projects/proj-a/runs/"+runID+"?call=0", nil)
	fullW := httptest.NewRecorder()
	RunDetail(runsDeps(cfg))(fullW, fullReq)
	if fullW.Code != http.StatusOK {
		t.Fatalf("?call status %d body=%s", fullW.Code, fullW.Body.String())
	}
	var full struct {
		Call capture.Record `json:"call"`
	}
	if err := json.Unmarshal(fullW.Body.Bytes(), &full); err != nil {
		t.Fatalf("decode call: %v", err)
	}
	if full.Call.Prompt != "captured prompt" || full.Call.Response != goodCapturedBody {
		t.Fatalf("full call = %+v", full.Call)
	}

	oob := httptest.NewRequest(http.MethodGet, "/api/projects/proj-a/runs/"+runID+"?call=7", nil)
	oobW := httptest.NewRecorder()
	RunDetail(runsDeps(cfg))(oobW, oob)
	if oobW.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range call index = %d, want 400", oobW.Code)
	}
}

func TestRunDetail_RejectsTraversalRunIDs(t *testing.T) {
	cfg, _ := seedCaptureProject(t, goodCapturedBody)
	for _, bad := range []string{"..%2F..%2Fstate", "ZZZZZZZZ", "short", "abcd12345"} {
		urlPath := "/api/projects/proj-a/runs/" + bad
		r := httptest.NewRequest(http.MethodGet, urlPath, nil)
		w := httptest.NewRecorder()
		RunDetail(runsDeps(cfg))(w, r)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Fatalf("run id %q → %d, want 400/404", bad, w.Code)
		}
	}
}

func TestRunsEndpoints_UnknownProject404(t *testing.T) {
	cfg, runID := seedCaptureProject(t, goodCapturedBody)
	deps := runsDeps(cfg)

	listReq := httptest.NewRequest(http.MethodGet, "/api/projects/nope/runs", nil)
	listW := httptest.NewRecorder()
	ProjectRuns(deps)(listW, listReq)
	if listW.Code != http.StatusNotFound {
		t.Fatalf("list unknown project = %d, want 404", listW.Code)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/projects/nope/runs/"+runID, nil)
	detailW := httptest.NewRecorder()
	RunDetail(deps)(detailW, detailReq)
	if detailW.Code != http.StatusNotFound {
		t.Fatalf("detail unknown project = %d, want 404", detailW.Code)
	}
}

func TestRunReplay_ReparseRecoversMistakesOverHTTP(t *testing.T) {
	cfg, runID := seedCaptureProject(t, goodCapturedBody)

	payload := `{"mode":"reparse","call_index":0}`
	r := httptest.NewRequest(http.MethodPost, "/api/projects/proj-a/runs/"+runID+"/replay", strings.NewReader(payload))
	w := httptest.NewRecorder()
	RunReplay(runsDeps(cfg))(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var result struct {
		Mode     string           `json:"mode"`
		Mistakes []map[string]any `json:"mistakes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Mode != capture.ReplayModeReparse || len(result.Mistakes) != 1 {
		t.Fatalf("result = mode=%s mistakes=%+v", result.Mode, result.Mistakes)
	}
}

func TestRunReplay_ValidatesBodyAndMode(t *testing.T) {
	cfg, runID := seedCaptureProject(t, goodCapturedBody)
	deps := runsDeps(cfg)

	badJSON := httptest.NewRequest(http.MethodPost, "/api/projects/proj-a/runs/"+runID+"/replay", strings.NewReader("{nope"))
	badW := httptest.NewRecorder()
	RunReplay(deps)(badW, badJSON)
	if badW.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON = %d, want 400", badW.Code)
	}

	badMode := httptest.NewRequest(http.MethodPost, "/api/projects/proj-a/runs/"+runID+"/replay", strings.NewReader(`{"mode":"teleport","call_index":0}`))
	modeW := httptest.NewRecorder()
	RunReplay(deps)(modeW, badMode)
	if modeW.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode = %d, want 400", modeW.Code)
	}

	oobIdx := httptest.NewRequest(http.MethodPost, "/api/projects/proj-a/runs/"+runID+"/replay", strings.NewReader(`{"mode":"reparse","call_index":42}`))
	idxW := httptest.NewRecorder()
	RunReplay(deps)(idxW, oobIdx)
	if idxW.Code != http.StatusNotFound {
		t.Fatalf("out-of-range index = %d, want 404", idxW.Code)
	}
}
