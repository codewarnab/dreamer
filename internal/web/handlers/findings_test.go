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

// seedFindingsProject writes a todos.md with two runs (old + latest) and a
// matching state.json with one applied (recurring) finding and one dismissed
// finding. Hash keys are 64-hex strings so they match the marker regex.
const (
	hashOpen      = "1111111111111111111111111111111111111111111111111111111111111111"
	hashApplied   = "2222222222222222222222222222222222222222222222222222222222222222"
	hashDismissed = "3333333333333333333333333333333333333333333333333333333333333333"
)

func seedFindingsProject(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	projDir := filepath.Join(root, "proj-x")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	todos := `# dreamer todos — proj-x

<!-- dreamer:version:1 -->

## Run 2026-05-19T10:00:00Z

### Doc
- [ ] dismissed mistake
    <!-- dreamer:finding:` + hashDismissed + ` -->

### Lint Rule
- [ ] applied mistake — guardrail: eslint/no-foo.
    <!-- dreamer:finding:` + hashApplied + ` -->

## Run 2026-05-20T10:00:00Z

### Doc
- [ ] still open mistake
    <!-- dreamer:finding:` + hashOpen + ` -->

### Lint Rule
- [ ] applied mistake — guardrail: eslint/no-foo.
    <!-- dreamer:finding:` + hashApplied + ` -->
`
	if err := os.WriteFile(filepath.Join(projDir, "todos.md"), []byte(todos), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	st := &state.State{
		Version:       state.StateVersion,
		LastRunUTC:    now,
		FindingHashes: []string{hashOpen, hashApplied, hashDismissed},
		Findings: map[string]state.FindingState{
			hashApplied:   {Status: state.FindingStatusApplied, AppliedAt: now.Add(-time.Hour)},
			hashDismissed: {Status: state.FindingStatusDismissed, DismissedAt: now.Add(-2 * time.Hour)},
		},
	}
	if err := state.Save(root, "proj-x", st); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		Projects: []config.ProjectConfig{{Name: "proj-x", Path: projDir}},
		Daemon:   config.DaemonConfig{OutputRoot: root, FrequencySeconds: 3600},
	}
}

func decodeFindings(t *testing.T, body []byte) []FindingView {
	t.Helper()
	var resp struct {
		Findings []FindingView `json:"findings"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	return resp.Findings
}

func TestProjectFindings_NoFilter_HidesDismissed(t *testing.T) {
	cfg := seedFindingsProject(t)
	h := ProjectFindings(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-x/findings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	views := decodeFindings(t, rec.Body.Bytes())
	if len(views) != 2 {
		t.Fatalf("len=%d want 2 (dismissed should be hidden); views=%+v", len(views), views)
	}
	byHash := map[string]FindingView{}
	for _, v := range views {
		byHash[v.Hash] = v
	}
	if _, ok := byHash[hashDismissed]; ok {
		t.Fatalf("dismissed leaked into default list")
	}
	openV := byHash[hashOpen]
	if openV.Status != "open" {
		t.Errorf("hashOpen status=%q want open", openV.Status)
	}
	if openV.Category != "Doc" {
		t.Errorf("hashOpen category=%q want Doc", openV.Category)
	}
	if openV.Summary != "still open mistake" {
		t.Errorf("hashOpen summary=%q", openV.Summary)
	}
	appliedV := byHash[hashApplied]
	if appliedV.Status != state.FindingStatusApplied {
		t.Errorf("hashApplied status=%q want applied", appliedV.Status)
	}
	if !appliedV.Recurred {
		t.Errorf("hashApplied should be Recurred=true (present in latest run)")
	}
	if appliedV.Summary != "applied mistake" {
		t.Errorf("guardrail suffix not stripped: %q", appliedV.Summary)
	}
	if appliedV.AppliedAt == "" {
		t.Errorf("AppliedAt missing")
	}
	if appliedV.LastSeenUTC != "2026-05-20T10:00:00Z" {
		t.Errorf("LastSeenUTC=%q want latest run", appliedV.LastSeenUTC)
	}
}

func TestProjectFindings_StatusOpenFilter(t *testing.T) {
	cfg := seedFindingsProject(t)
	h := ProjectFindings(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-x/findings?status=open", nil))
	views := decodeFindings(t, rec.Body.Bytes())
	if len(views) != 1 || views[0].Hash != hashOpen {
		t.Fatalf("status=open should return only open finding; got %+v", views)
	}
}

func TestProjectFindings_StatusDismissedFilter(t *testing.T) {
	cfg := seedFindingsProject(t)
	h := ProjectFindings(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-x/findings?status=dismissed", nil))
	views := decodeFindings(t, rec.Body.Bytes())
	if len(views) != 1 || views[0].Hash != hashDismissed {
		t.Fatalf("status=dismissed should expose dismissed; got %+v", views)
	}
	if views[0].DismissedAt == "" {
		t.Errorf("DismissedAt missing")
	}
}

func TestProjectFindings_CategoryFilter(t *testing.T) {
	cfg := seedFindingsProject(t)
	h := ProjectFindings(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-x/findings?category=lint%20rule", nil))
	views := decodeFindings(t, rec.Body.Bytes())
	if len(views) != 1 || views[0].Hash != hashApplied {
		t.Fatalf("category filter mismatch; got %+v", views)
	}
}

func TestProjectFindings_UnknownProject(t *testing.T) {
	cfg := seedFindingsProject(t)
	h := ProjectFindings(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/nope/findings", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

func TestFindingDetail_ReturnsView(t *testing.T) {
	cfg := seedFindingsProject(t)
	h := FindingDetail(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-x/findings/"+hashApplied, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Detail response is flat: FindingView fields at top level plus
	// apply_eligible + diff_preview.
	var resp struct {
		FindingView
		ApplyEligible bool   `json:"apply_eligible"`
		DiffPreview   string `json:"diff_preview"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Hash != hashApplied {
		t.Errorf("hash mismatch: %+v", resp.FindingView)
	}
	if resp.Status != state.FindingStatusApplied {
		t.Errorf("status=%q want applied", resp.Status)
	}
	if !resp.Recurred {
		t.Errorf("expected Recurred=true")
	}
	if resp.DiffPreview != "" {
		t.Errorf("diff_preview should be empty without apply hints; got %q", resp.DiffPreview)
	}
}

func TestFindingDetail_WithApplyHints_RendersDiff(t *testing.T) {
	cfg := seedFindingsProject(t)
	// Seed a target file inside the project so Preview has something to read.
	proj := cfg.Projects[0]
	targetAbs := filepath.Join(proj.Path, "CLAUDE.md")
	if err := os.WriteFile(targetAbs, []byte("# Doc\n\nIntro.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := FindingDetail(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	url := "/api/projects/proj-x/findings/" + hashOpen +
		"?target_file=CLAUDE.md&strategy=append-section&anchor=Cache&snippet=Rules%20for%20cache."
	h(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		FindingView
		ApplyEligible bool   `json:"apply_eligible"`
		DiffPreview   string `json:"diff_preview"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DiffPreview == "" {
		t.Fatalf("expected non-empty diff_preview")
	}
	if !strings.Contains(resp.DiffPreview, "+ ## Cache") {
		t.Errorf("diff missing added Cache section:\n%s", resp.DiffPreview)
	}
	// Preview must not mutate the file.
	got, _ := os.ReadFile(targetAbs)
	if string(got) != "# Doc\n\nIntro.\n" {
		t.Errorf("FindingDetail wrote to target: %q", got)
	}
}

func TestFindingDetail_UnknownHash(t *testing.T) {
	cfg := seedFindingsProject(t)
	h := FindingDetail(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet,
		"/api/projects/proj-x/findings/deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

func TestProjectFindings_MissingTodosFile(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "p", Path: root}},
		Daemon:   config.DaemonConfig{OutputRoot: root},
	}
	// No todos.md and no state.json — should return empty list, not error.
	h := ProjectFindings(Deps{Config: func() *config.Config { return cfg }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p/findings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	views := decodeFindings(t, rec.Body.Bytes())
	if len(views) != 0 {
		t.Fatalf("expected empty, got %+v", views)
	}
}
