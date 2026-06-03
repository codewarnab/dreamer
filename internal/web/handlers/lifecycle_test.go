package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"dreamer/internal/config"
	"dreamer/internal/pipeline"
	"dreamer/internal/state"
)

// seedApplyProject seeds a minimal project with a CLAUDE.md target file and
// returns the config plus an event bus. The seeded state contains no
// lifecycle entries for the given hashes so Apply starts from "open".
func seedApplyProject(t *testing.T) (*config.App, *pipeline.EventBus) {
	t.Helper()
	root := t.TempDir()
	projDir := filepath.Join(root, "proj-a")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "CLAUDE.md"), []byte("# Doc\n\nIntro.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &state.State{Version: state.StateVersion}
	if err := state.Save(root, "proj-a", st); err != nil {
		t.Fatal(err)
	}
	cfg := &config.App{
		Projects: []config.ProjectConfig{{Name: "proj-a", Path: projDir}},
		Daemon:   config.DaemonConfig{OutputRoot: root, FrequencySeconds: 3600},
	}
	return cfg, pipeline.NewEventBus()
}

// seedApplySpec records a server-trusted ApplySpec for the given hash so
// the Apply handler treats the finding as analyzer-emitted. Tests must
// seed before invoking Apply since the body's apply fields are ignored.
func seedApplySpec(t *testing.T, cfg *config.App, hash string, spec state.FindingApplySpec) {
	t.Helper()
	st, err := state.Load(cfg.Daemon.OutputRoot, cfg.Projects[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	if st.Findings == nil {
		st.Findings = map[string]state.FindingState{}
	}
	fs := st.Findings[hash]
	cp := spec
	fs.ApplySpec = &cp
	st.Findings[hash] = fs
	if err := state.Save(cfg.Daemon.OutputRoot, cfg.Projects[0].Name, st); err != nil {
		t.Fatal(err)
	}
}

func postJSON(t *testing.T, h http.HandlerFunc, url string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Buffer
	if body == nil {
		rdr = bytes.NewBuffer(nil)
	} else {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewBuffer(buf)
	}
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, url, rdr))
	return rec
}

func TestApply_Happy(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	sub := bus.Subscribe(4)
	defer bus.Unsubscribe(sub)
	h := Apply(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "aaaa111111111111111111111111111111111111111111111111111111111111"
	seedApplySpec(t, cfg, hash, state.FindingApplySpec{
		Category:   "doc",
		TargetFile: "CLAUDE.md",
		Strategy:   "append-section",
		Anchor:     "Cache",
		Snippet:    "Rules for cache.",
	})
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/apply", applyRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var fs state.FindingState
	if err := json.Unmarshal(rec.Body.Bytes(), &fs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fs.Status != state.FindingStatusApplied {
		t.Errorf("status=%q want applied", fs.Status)
	}
	if fs.AppliedReversal == nil || fs.AppliedReversal.PostImageSHA256 == "" {
		t.Errorf("reversal not captured: %+v", fs.AppliedReversal)
	}

	// State must persist.
	st, err := state.Load(cfg.Daemon.OutputRoot, "proj-a")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := st.Findings[hash]
	if !ok {
		t.Fatalf("hash not persisted in state: %+v", st.Findings)
	}
	if got.Status != state.FindingStatusApplied {
		t.Errorf("persisted status=%q", got.Status)
	}

	// Event must be published.
	select {
	case evt := <-sub:
		if evt.Type != pipeline.EventFindingApplied {
			t.Errorf("event type=%q", evt.Type)
		}
		if evt.Payload["hash"] != hash {
			t.Errorf("event payload hash=%v", evt.Payload["hash"])
		}
	default:
		t.Errorf("no event published")
	}
}

func TestApply_IneligibleCategory(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	h := Apply(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "bbbb111111111111111111111111111111111111111111111111111111111111"
	seedApplySpec(t, cfg, hash, state.FindingApplySpec{
		Category:   "perf",
		TargetFile: "CLAUDE.md",
		Strategy:   "append-file",
		Snippet:    "x",
	})
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/apply", applyRequest{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
}

func TestApply_Containment(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	h := Apply(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "cccc111111111111111111111111111111111111111111111111111111111111"
	seedApplySpec(t, cfg, hash, state.FindingApplySpec{
		Category:   "doc",
		TargetFile: "../etc/passwd",
		Strategy:   "append-file",
		Snippet:    "x",
	})
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/apply", applyRequest{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
}

func TestApply_AnchorMissing(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	h := Apply(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "dddd111111111111111111111111111111111111111111111111111111111111"
	seedApplySpec(t, cfg, hash, state.FindingApplySpec{
		Category:   "doc",
		TargetFile: "CLAUDE.md",
		Strategy:   "replace-section",
		Anchor:     "NonexistentSection",
		Snippet:    "x",
	})
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/apply", applyRequest{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestApply_OversizeTarget(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	// Inflate CLAUDE.md beyond the 4 MiB cap.
	big := bytes.Repeat([]byte("a"), 5<<20)
	if err := os.WriteFile(filepath.Join(cfg.Projects[0].Path, "CLAUDE.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	h := Apply(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "eeee111111111111111111111111111111111111111111111111111111111111"
	seedApplySpec(t, cfg, hash, state.FindingApplySpec{
		Category:   "doc",
		TargetFile: "CLAUDE.md",
		Strategy:   "append-file",
		Snippet:    "x",
	})
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/apply", applyRequest{})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413 body=%s", rec.Code, rec.Body.String())
	}
}

func TestApply_MethodNotAllowed(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	h := Apply(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/projects/proj-a/findings/ffff111111111111111111111111111111111111111111111111111111111111/apply", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want 405", rec.Code)
	}
}

// TestApply_BodyIgnored_AttackerCannotRedirectWrite asserts that an
// attacker-shaped request body (replace-file targeting an arbitrary path
// outside the project) is ignored: the server uses the persisted spec.
func TestApply_BodyIgnored_AttackerCannotRedirectWrite(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	h := Apply(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "b1aa111111111111111111111111111111111111111111111111111111111111"
	// Server's recorded plan: a safe append to CLAUDE.md.
	seedApplySpec(t, cfg, hash, state.FindingApplySpec{
		Category:   "doc",
		TargetFile: "CLAUDE.md",
		Strategy:   "append-file",
		Snippet:    "trusted-snippet",
	})
	// Attacker tries to redirect the write via replace-file.
	attackTarget := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(attackTarget, []byte("untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/apply", applyRequest{
		Category:   "doc",
		TargetFile: attackTarget,
		Strategy:   "replace-file",
		Snippet:    "OWNED",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Victim file must be unchanged.
	got, _ := os.ReadFile(attackTarget)
	if string(got) != "untouched\n" {
		t.Errorf("attacker-supplied target was written: %q", got)
	}
	// Server's actual write landed on the trusted target.
	claude, _ := os.ReadFile(filepath.Join(cfg.Projects[0].Path, "CLAUDE.md"))
	if !bytes.Contains(claude, []byte("trusted-snippet")) {
		t.Errorf("trusted snippet not written to CLAUDE.md; got %q", claude)
	}
	if bytes.Contains(claude, []byte("OWNED")) {
		t.Errorf("attacker snippet leaked into CLAUDE.md: %q", claude)
	}
}

// TestApply_MissingApplySpec_Returns404 covers findings without a recorded
// ApplySpec (older state.json files or non-apply-eligible findings). The
// handler refuses rather than falling back to the request body.
func TestApply_MissingApplySpec_Returns404(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	h := Apply(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "b2aa111111111111111111111111111111111111111111111111111111111111"
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/apply", applyRequest{
		Category:   "doc",
		TargetFile: "CLAUDE.md",
		Strategy:   "append-file",
		Snippet:    "x",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestApply_UnknownProject(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	h := Apply(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "aaaa222222222222222222222222222222222222222222222222222222222222"
	rec := postJSON(t, h, "/api/projects/nope/findings/"+hash+"/apply", applyRequest{Category: "doc"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

// applyOnce drives the Apply handler so undo/redo tests inherit a real
// FindingReversal rather than constructing one by hand.
func applyOnce(t *testing.T, cfg *config.App, bus *pipeline.EventBus, hash string) {
	t.Helper()
	seedApplySpec(t, cfg, hash, state.FindingApplySpec{
		Category:   "doc",
		TargetFile: "CLAUDE.md",
		Strategy:   "append-section",
		Anchor:     "Cache",
		Snippet:    "Rules for cache.",
	})
	h := Apply(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/apply", applyRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("seed apply failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUndo_Happy(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	hash := "11aa111111111111111111111111111111111111111111111111111111111111"
	applyOnce(t, cfg, bus, hash)
	target := filepath.Join(cfg.Projects[0].Path, "CLAUDE.md")
	preApply := "# Doc\n\nIntro.\n"
	// Sanity: file changed after apply.
	got, _ := os.ReadFile(target)
	if string(got) == preApply {
		t.Fatalf("apply did not mutate target")
	}

	sub := bus.Subscribe(4)
	defer bus.Unsubscribe(sub)
	h := Undo(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/undo", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	got, _ = os.ReadFile(target)
	if string(got) != preApply {
		t.Errorf("undo did not restore pre-image; got %q", got)
	}
	st, _ := state.Load(cfg.Daemon.OutputRoot, "proj-a")
	fs, ok := st.Findings[hash]
	if !ok {
		t.Fatalf("undo removed state entry entirely; expected it to be preserved with cleared fields")
	}
	if fs.Status != "" {
		t.Errorf("undo did not clear Status; got %q", fs.Status)
	}
	if !fs.AppliedAt.IsZero() {
		t.Errorf("undo did not clear AppliedAt; got %v", fs.AppliedAt)
	}
	if fs.AppliedReversal != nil {
		t.Errorf("undo did not clear AppliedReversal")
	}
	select {
	case evt := <-sub:
		if evt.Type != pipeline.EventFindingUndone {
			t.Errorf("event type=%q", evt.Type)
		}
	default:
		t.Errorf("no undone event published")
	}
}

func TestUndo_TargetChanged(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	hash := "22aa111111111111111111111111111111111111111111111111111111111111"
	applyOnce(t, cfg, bus, hash)
	target := filepath.Join(cfg.Projects[0].Path, "CLAUDE.md")
	// Operator edits the file after apply.
	if err := os.WriteFile(target, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := Undo(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/undo", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 body=%s", rec.Code, rec.Body.String())
	}
	// State entry must still be present (undo did not run).
	st, _ := state.Load(cfg.Daemon.OutputRoot, "proj-a")
	if _, ok := st.Findings[hash]; !ok {
		t.Errorf("state entry dropped despite conflict")
	}
}

func TestUndo_NotApplied(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	h := Undo(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "33aa111111111111111111111111111111111111111111111111111111111111"
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/undo", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

func TestDismiss_PersistsAndPublishes(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	sub := bus.Subscribe(4)
	defer bus.Unsubscribe(sub)
	h := Dismiss(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "44aa111111111111111111111111111111111111111111111111111111111111"
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/dismiss", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	st, _ := state.Load(cfg.Daemon.OutputRoot, "proj-a")
	findingState, ok := st.Findings[hash]
	if !ok || findingState.Status != state.FindingStatusDismissed {
		t.Fatalf("dismiss not persisted: %+v", st.Findings)
	}
	if findingState.DismissedAt.IsZero() {
		t.Errorf("DismissedAt zero")
	}
	select {
	case evt := <-sub:
		if evt.Type != pipeline.EventFindingDismissed {
			t.Errorf("event type=%q", evt.Type)
		}
	default:
		t.Errorf("no dismiss event published")
	}
}

func TestResolve_PersistsAndPublishes(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	sub := bus.Subscribe(4)
	defer bus.Unsubscribe(sub)
	h := Resolve(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	hash := "55aa111111111111111111111111111111111111111111111111111111111111"
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/resolve", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	st, _ := state.Load(cfg.Daemon.OutputRoot, "proj-a")
	findingState, ok := st.Findings[hash]
	if !ok || findingState.Status != state.FindingStatusResolved {
		t.Fatalf("resolve not persisted: %+v", st.Findings)
	}
	select {
	case evt := <-sub:
		if evt.Type != pipeline.EventFindingResolved {
			t.Errorf("event type=%q", evt.Type)
		}
	default:
		t.Errorf("no resolve event published")
	}
}

func TestUndismiss_DropsEntry(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	hash := "66aa111111111111111111111111111111111111111111111111111111111111"
	// Seed: dismissed.
	hDismiss := Dismiss(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	if rec := postJSON(t, hDismiss, "/api/projects/proj-a/findings/"+hash+"/dismiss", nil); rec.Code != http.StatusOK {
		t.Fatalf("seed dismiss failed: %d", rec.Code)
	}
	h := Undismiss(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/undismiss", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	st, _ := state.Load(cfg.Daemon.OutputRoot, "proj-a")
	if _, ok := st.Findings[hash]; ok {
		t.Errorf("undismiss did not drop entry")
	}
}

func TestUnresolve_DropsEntry(t *testing.T) {
	cfg, bus := seedApplyProject(t)
	hash := "77aa111111111111111111111111111111111111111111111111111111111111"
	hResolve := Resolve(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	if rec := postJSON(t, hResolve, "/api/projects/proj-a/findings/"+hash+"/resolve", nil); rec.Code != http.StatusOK {
		t.Fatalf("seed resolve failed: %d", rec.Code)
	}
	h := Unresolve(Deps{Config: func() *config.App { return cfg }, Events: bus, StateLock: NewProjectLock()})
	rec := postJSON(t, h, "/api/projects/proj-a/findings/"+hash+"/unresolve", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	st, _ := state.Load(cfg.Daemon.OutputRoot, "proj-a")
	if _, ok := st.Findings[hash]; ok {
		t.Errorf("unresolve did not drop entry")
	}
}

func TestParseProjectHashTransition(t *testing.T) {
	tests := []struct {
		name      string
		urlPath   string
		wantName  string
		wantHash  string
		wantTrans string
	}{
		{
			name:      "valid apply",
			urlPath:   "/api/projects/my-proj/findings/abc123/apply",
			wantName:  "my-proj",
			wantHash:  "abc123",
			wantTrans: "apply",
		},
		{
			name:      "valid undo",
			urlPath:   "/api/projects/proj-a/findings/ffff0000/undo",
			wantName:  "proj-a",
			wantHash:  "ffff0000",
			wantTrans: "undo",
		},
		{
			name:      "valid dismiss",
			urlPath:   "/api/projects/test/findings/aaa111/dismiss",
			wantName:  "test",
			wantHash:  "aaa111",
			wantTrans: "dismiss",
		},
		{
			name:      "valid resolve",
			urlPath:   "/api/projects/x/findings/bbb222/resolve",
			wantName:  "x",
			wantHash:  "bbb222",
			wantTrans: "resolve",
		},
		{
			name:      "valid undismiss",
			urlPath:   "/api/projects/p/findings/ccc333/undismiss",
			wantName:  "p",
			wantHash:  "ccc333",
			wantTrans: "undismiss",
		},
		{
			name:      "valid unresolve",
			urlPath:   "/api/projects/p/findings/ddd444/unresolve",
			wantName:  "p",
			wantHash:  "ddd444",
			wantTrans: "unresolve",
		},
		{
			name:    "too few segments",
			urlPath: "/api/projects/proj-a/findings/abc",
		},
		{
			name:    "too many segments",
			urlPath: "/api/projects/proj-a/findings/abc/apply/extra",
		},
		{
			name:    "wrong prefix",
			urlPath: "/other/projects/proj-a/findings/abc/apply",
		},
		{
			name:    "missing findings keyword",
			urlPath: "/api/projects/proj-a/items/abc/apply",
		},
		{
			name:    "empty path",
			urlPath: "",
		},
		{
			name:      "trailing slash",
			urlPath:   "/api/projects/proj-a/findings/abc/apply/",
			wantName:  "proj-a",
			wantHash:  "abc",
			wantTrans: "apply",
		},
		{
			name:      "leading slash preserved",
			urlPath:   "api/projects/proj-a/findings/abc/apply",
			wantName:  "proj-a",
			wantHash:  "abc",
			wantTrans: "apply",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, hash, trans := parseProjectHashTransition(tt.urlPath)
			if name != tt.wantName {
				t.Errorf("name=%q want %q", name, tt.wantName)
			}
			if hash != tt.wantHash {
				t.Errorf("hash=%q want %q", hash, tt.wantHash)
			}
			if trans != tt.wantTrans {
				t.Errorf("transition=%q want %q", trans, tt.wantTrans)
			}
		})
	}
}
