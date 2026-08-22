package replay

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/capture"
	"dreamer/internal/config"

	// Populate the provider registry so IsRegisteredProvider sees the real
	// IDs used by these tests (mirrors cmd/root.go's blank imports).
	_ "dreamer/internal/analyzer/providers/claudecli"
	_ "dreamer/internal/analyzer/providers/codexcli"
)

type stubSession struct {
	reply  string
	runErr error
}

func (s *stubSession) Run(_ context.Context, _ string, _ time.Duration) (string, error) {
	return s.reply, s.runErr
}
func (s *stubSession) Close() error { return nil }

func writeCaptureRun(t *testing.T, root string, meta capture.RunMeta, records ...capture.Record) {
	t.Helper()
	w, err := capture.Open(capture.RunDir(root, meta.ProjectName, meta.RunID), meta)
	if err != nil {
		t.Fatalf("capture.Open: %v", err)
	}
	for _, rec := range records {
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func replayPacks(t *testing.T) []analyzer.RulePack {
	t.Helper()
	packs, err := analyzer.LoadDefaultRulePacks()
	if err != nil {
		t.Fatalf("LoadDefaultRulePacks: %v", err)
	}
	return packs
}

const goodPhase1Body = `{"summary":"done","mistakes":{"test":[{"category":"test","summary":"missing edge-case test","evidence_excerpt":"no test","confidence":0.9}]}}`

func TestReparseDecodesStoredResponse(t *testing.T) {
	root := t.TempDir()
	meta := baseMeta("run-r1")
	writeCaptureRun(t, root, meta,
		capture.Record{Phase: capture.PhasePhase1, ChunkIndex: 0, ChunkCount: 1, Prompt: "p", Response: goodPhase1Body, Status: capture.StatusOK},
	)

	res, err := Reparse(root, "proj", "run-r1", 0, replayPacks(t))
	if err != nil {
		t.Fatalf("Reparse: %v", err)
	}
	if res.ParseError != "" {
		t.Fatalf("unexpected ParseError %q", res.ParseError)
	}
	if len(res.Mistakes) != 1 || res.Mistakes[0].Summary != "missing edge-case test" {
		t.Fatalf("mistakes = %+v", res.Mistakes)
	}
	if res.ByCategory["test"] != 1 {
		t.Fatalf("ByCategory = %+v, want test:1", res.ByCategory)
	}
}

func TestReparseReportsStillFailingBody(t *testing.T) {
	root := t.TempDir()
	meta := baseMeta("run-bad")
	writeCaptureRun(t, root, meta,
		capture.Record{Phase: capture.PhasePhase1, ChunkIndex: 0, Response: `{"summary": "s"}n`, Status: capture.StatusParseFailed, Error: "invalid phase-1 JSON"},
	)

	res, err := Reparse(root, "proj", "run-bad", 0, replayPacks(t))
	if err != nil {
		t.Fatalf("Reparse must not fail on an unparseable body: %v", err)
	}
	if !strings.Contains(res.ParseError, "invalid phase-1 JSON") {
		t.Fatalf("ParseError = %q, want invalid phase-1 JSON wrapper", res.ParseError)
	}
}

func TestReparseRejectsPhase2AndBadIndexes(t *testing.T) {
	root := t.TempDir()
	meta := baseMeta("run-mix")
	writeCaptureRun(t, root, meta,
		capture.Record{Phase: capture.PhasePhase1, ChunkIndex: 0, Response: goodPhase1Body, Status: capture.StatusOK},
		capture.Record{Phase: capture.PhasePhase2, ChunkIndex: -1, Response: "{}", Status: capture.StatusOK},
	)

	if _, err := Reparse(root, "proj", "run-mix", -1, replayPacks(t)); !errors.Is(err, ErrCallNotFound) {
		t.Fatalf("negative index = %v, want ErrCallNotFound", err)
	}
	if _, err := Reparse(root, "proj", "run-mix", 9, replayPacks(t)); !errors.Is(err, ErrCallNotFound) {
		t.Fatalf("out-of-range index = %v, want ErrCallNotFound", err)
	}
	if _, err := Reparse(root, "proj", "run-mix", 1, replayPacks(t)); err == nil || !strings.Contains(err.Error(), "phase-1") {
		t.Fatalf("phase-2 reparse = %v, want phase-1-only error", err)
	}
}

func TestResendsWithOverridesWritesReplayRun(t *testing.T) {
	root := t.TempDir()
	meta := baseMeta("run-parent")
	meta.ProjectPath = t.TempDir() // must exist for the Stat check
	writeCaptureRun(t, root, meta,
		capture.Record{Phase: capture.PhasePhase1, ChunkIndex: 2, ChunkCount: 5, Prompt: "captured prompt", Response: "", Status: capture.StatusParseFailed, Error: "invalid phase-1 JSON"},
	)

	var gotModel string
	opts := Options{
		OutputRoot:       root,
		ProjectName:      "proj",
		RunID:            "run-parent",
		CallIndex:        0,
		ProviderOverride: "codex-cli",
		ModelOverride:    "o4-mini",
		AppConfig:        &config.App{},
		Packs:            replayPacks(t),
		sessionFor: func(providerID, model, _, _, _ string) (analyzer.Session, func(), error) {
			gotModel = model
			return &stubSession{reply: goodPhase1Body}, func() {}, nil
		},
	}

	res, err := Resend(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resend: %v", err)
	}
	if gotModel != "o4-mini" {
		t.Fatalf("session model = %q, want override o4-mini", gotModel)
	}
	if res.ProviderID != "codex-cli" || res.Model != "o4-mini" {
		t.Fatalf("result provider/model = %s/%s, want codex-cli/o4-mini", res.ProviderID, res.Model)
	}
	if res.ReplayRunID == "" || res.ReplayRunID == "run-parent" {
		t.Fatalf("ReplayRunID = %q, want a fresh id", res.ReplayRunID)
	}
	if len(res.Mistakes) != 1 {
		t.Fatalf("resent mistakes = %+v", res.Mistakes)
	}

	rmeta, records, err := capture.LoadRun(capture.RunsRoot(root, "proj"), res.ReplayRunID)
	if err != nil {
		t.Fatalf("LoadRun replay dir: %v", err)
	}
	if rmeta.Kind != capture.KindReplay || rmeta.ReplayMode != capture.ReplayModeResend {
		t.Fatalf("replay meta kind/mode = %s/%s", rmeta.Kind, rmeta.ReplayMode)
	}
	if rmeta.ParentRunID != "run-parent" || rmeta.ParentCallIdx != 0 {
		t.Fatalf("lineage = %s/#%d, want run-parent/#0", rmeta.ParentRunID, rmeta.ParentCallIdx)
	}
	if rmeta.ProviderID != "codex-cli" || rmeta.Model != "o4-mini" {
		t.Fatalf("replay meta provider/model = %s/%s", rmeta.ProviderID, rmeta.Model)
	}
	if len(records) != 1 || records[0].Status != capture.StatusOK || records[0].ChunkIndex != 2 {
		t.Fatalf("replay records = %+v", records)
	}
}

func TestResendKeepsCapturedProviderWhenNoOverride(t *testing.T) {
	root := t.TempDir()
	meta := baseMeta("run-keep")
	meta.ProjectPath = t.TempDir()
	writeCaptureRun(t, root, meta,
		capture.Record{Phase: capture.PhasePhase1, ChunkIndex: 0, Prompt: "p", Response: goodPhase1Body, Status: capture.StatusOK},
	)

	var gotProvider string
	opts := Options{
		OutputRoot:  root,
		ProjectName: "proj",
		RunID:       "run-keep",
		CallIndex:   0,
		AppConfig:   &config.App{},
		Packs:       replayPacks(t),
		sessionFor: func(providerID, model, _, _, _ string) (analyzer.Session, func(), error) {
			gotProvider = providerID
			return &stubSession{reply: goodPhase1Body}, func() {}, nil
		},
	}
	res, err := Resend(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resend: %v", err)
	}
	if gotProvider != "claude-cli" || res.ProviderID != "claude-cli" {
		t.Fatalf("provider = %s/%s, want captured claude-cli kept", gotProvider, res.ProviderID)
	}
}

func TestResendValidatesInputs(t *testing.T) {
	root := t.TempDir()
	meta := baseMeta("run-v")
	meta.ProjectPath = t.TempDir()
	writeCaptureRun(t, root, meta,
		capture.Record{Phase: capture.PhasePhase1, ChunkIndex: 0, Prompt: "p", Response: goodPhase1Body, Status: capture.StatusOK},
	)

	base := Options{
		OutputRoot:  root,
		ProjectName: "proj",
		RunID:       "run-v",
		AppConfig:   &config.App{},
		Packs:       replayPacks(t),
		sessionFor: func(string, string, string, string, string) (analyzer.Session, func(), error) {
			return &stubSession{}, func() {}, errors.New("must not be called")
		},
	}

	outOfRange := base
	outOfRange.CallIndex = 5
	if _, err := Resend(context.Background(), outOfRange); !errors.Is(err, ErrCallNotFound) {
		t.Fatalf("out-of-range index = %v, want ErrCallNotFound", err)
	}

	unregistered := base
	unregistered.CallIndex = 0
	unregistered.ProviderOverride = "does-not-exist"
	if _, err := Resend(context.Background(), unregistered); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unregistered provider err = %v, want not registered", err)
	}

	noCfg := base
	noCfg.CallIndex = 0
	noCfg.AppConfig = nil
	if _, err := Resend(context.Background(), noCfg); err == nil || !strings.Contains(err.Error(), "AppConfig is required") {
		t.Fatalf("nil AppConfig err = %v", err)
	}
}

func baseMeta(runID string) capture.RunMeta {
	return capture.RunMeta{
		RunID:         runID,
		ProjectName:   "proj",
		ProjectPath:   filepath.Join("does", "not", "exist"),
		ProviderID:    "claude-cli",
		Model:         "claude-sonnet",
		Sandbox:       "auto",
		SystemMessage: "sys",
		StartedAt:     time.Now().UTC(),
		Kind:          capture.KindAnalysis,
	}
}
