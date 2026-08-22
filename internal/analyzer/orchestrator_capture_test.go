package analyzer

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// recordingCapture collects CapturedCall events; safe for the parallel path.
type recordingCapture struct {
	mu    sync.Mutex
	calls []CapturedCall
}

func (r *recordingCapture) CaptureCall(cc CapturedCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, cc)
}

func (r *recordingCapture) all() []CapturedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]CapturedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func phase1Calls(calls []CapturedCall) []CapturedCall {
	var out []CapturedCall
	for _, cc := range calls {
		if cc.Phase == PhasePhase1 {
			out = append(out, cc)
		}
	}
	return out
}

const invalidPhase1Body = `{"summary": "s", "mistakes": {}}n`

func TestRunChunksCapturesParseFailedResponseIntact(t *testing.T) {
	sess := &fakeSession{handler: func(p string) (string, error) {
		return invalidPhase1Body, nil
	}}
	rec := &recordingCapture{}
	rc := RunConfig{
		Mode:                 ModeSequential,
		Phase1SessionFactory: func() (Session, error) { return sess, nil },
		Capture:              rec,
	}
	in := ChunkInputs{Chunks: []Chunk{{Index: 0, Transcript: "hi", Bytes: 2, SourceLabels: []string{"codex"}}}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	res, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{ProjectRoot: "/repo"})
	if err != nil {
		t.Fatalf("parse failure must not abort the run: %v", err)
	}
	warningsJoined := strings.Join(res.Warnings, "; ")
	if !strings.Contains(warningsJoined, "parse failed") {
		t.Fatalf("expected parse warning, got %v", res.Warnings)
	}

	p1 := phase1Calls(rec.all())
	if len(p1) != 1 {
		t.Fatalf("phase-1 captures = %d, want 1", len(p1))
	}
	cc := p1[0]
	if cc.Response != invalidPhase1Body {
		t.Fatalf("captured response must be the exact raw body, got %q", cc.Response)
	}
	if cc.ParseError == nil || !strings.Contains(cc.ParseError.Error(), "invalid phase-1 JSON") {
		t.Fatalf("ParseError = %v, want invalid phase-1 JSON wrapper", cc.ParseError)
	}
	if cc.ChunkIndex != 0 || cc.ChunkCount != 1 {
		t.Fatalf("chunk index/count = %d/%d, want 0/1", cc.ChunkIndex, cc.ChunkCount)
	}
	if cc.Prompt == "" {
		t.Fatal("captured prompt must not be empty")
	}
	if cc.RunError != nil {
		t.Fatalf("RunError should be nil on a transport-success call, got %v", cc.RunError)
	}
}

func TestRunChunksCapturesEveryCallHappyPath(t *testing.T) {
	sess := &fakeSession{handler: func(p string) (string, error) {
		if strings.Contains(p, "auditing chat transcripts") {
			return phase1Reply("sum", map[string][]map[string]any{
				"test": {{"category": "test", "summary": "m", "evidence_excerpt": "e", "confidence": 0.9}},
			}), nil
		}
		return phase2Reply(map[string][]map[string]any{
			"test": {{"category": "test", "mistake": "m", "confidence": 0.9}},
		}), nil
	}}
	rec := &recordingCapture{}
	rc := RunConfig{
		Mode:                 ModeSequential,
		Phase1SessionFactory: func() (Session, error) { return sess, nil },
		Capture:              rec,
	}
	in := ChunkInputs{Chunks: []Chunk{{Index: 0, Transcript: "hi", Bytes: 2, SourceLabels: []string{"codex"}}}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	if _, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{ProjectRoot: "/repo"}); err != nil {
		t.Fatalf("RunChunks: %v", err)
	}
	calls := rec.all()
	if len(calls) != 2 {
		t.Fatalf("captures = %d, want 2 (one per phase)", len(calls))
	}
	for _, cc := range calls {
		if cc.ParseError != nil || cc.RunError != nil {
			t.Fatalf("happy-path capture must be clean, got parse=%v run=%v", cc.ParseError, cc.RunError)
		}
	}
	if calls[0].Phase != PhasePhase1 || calls[1].Phase != PhasePhase2 {
		t.Fatalf("capture order = %s,%s; want phase1 then phase2", calls[0].Phase, calls[1].Phase)
	}
	if calls[1].ChunkIndex != -1 {
		t.Fatalf("phase-2 ChunkIndex = %d, want -1", calls[1].ChunkIndex)
	}
}

func TestRunChunksParallelCapturesAllChunks(t *testing.T) {
	sess := &fakeSession{handler: func(p string) (string, error) {
		return phase1Reply("s", map[string][]map[string]any{
			"test": {{"category": "test", "summary": "m", "confidence": 0.9}},
		}), nil
	}}
	rec := &recordingCapture{}
	rc := RunConfig{
		Mode:                 ModeParallel,
		MaxConcurrency:       2,
		Phase1SessionFactory: func() (Session, error) { return sess, nil },
		Capture:              rec,
	}
	chunks := []Chunk{
		{Index: 0, Transcript: "a", Bytes: 1, SourceLabels: []string{"codex"}},
		{Index: 1, Transcript: "b", Bytes: 1, SourceLabels: []string{"codex"}},
		{Index: 2, Transcript: "c", Bytes: 1, SourceLabels: []string{"codex"}},
	}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	res, err := orch.RunChunks(context.Background(), rc, ChunkInputs{Chunks: chunks}, PhaseRequest{ProjectRoot: "/repo", DryRun: true})
	if err != nil {
		t.Fatalf("RunChunks parallel dry-run: %v", err)
	}
	if !res.Phase1Complete {
		t.Fatalf("Phase1Complete = false, want true")
	}
	p1 := phase1Calls(rec.all())
	if len(p1) != 3 {
		t.Fatalf("parallel phase-1 captures = %d, want 3", len(p1))
	}
	seen := map[int]bool{}
	for _, cc := range p1 {
		if seen[cc.ChunkIndex] {
			t.Fatalf("duplicate capture for chunk %d", cc.ChunkIndex)
		}
		seen[cc.ChunkIndex] = true
		if cc.ChunkCount != 3 {
			t.Fatalf("chunk %d ChunkCount = %d, want 3", cc.ChunkIndex, cc.ChunkCount)
		}
	}
	for i := 0; i < 3; i++ {
		if !seen[i] {
			t.Fatalf("missing capture for chunk %d", i)
		}
	}
}

func TestRunChunksCapturesTransportFailure(t *testing.T) {
	boom := errors.New("provider exploded")
	sess := &fakeSession{handler: func(string) (string, error) { return "", boom }}
	rec := &recordingCapture{}
	rc := RunConfig{
		Mode:                 ModeSequential,
		Phase1SessionFactory: func() (Session, error) { return sess, nil },
		Capture:              rec,
	}
	in := ChunkInputs{Chunks: []Chunk{{Index: 0, Transcript: "hi", Bytes: 2, SourceLabels: []string{"codex"}}}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	if _, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{ProjectRoot: "/repo"}); err == nil {
		t.Fatal("RunChunks should surface the transport error")
	}
	p1 := phase1Calls(rec.all())
	if len(p1) != 1 {
		t.Fatalf("captures = %d, want 1", len(p1))
	}
	if !errors.Is(p1[0].RunError, boom) {
		t.Fatalf("RunError = %v, want provider exploded", p1[0].RunError)
	}
}

func TestRunChunksNilCaptureStillWorks(t *testing.T) {
	sess := &fakeSession{} // handler nil → "{}" replies; empty response tolerated upstream?
	rc := RunConfig{
		Mode:                 ModeSequential,
		Phase1SessionFactory: func() (Session, error) { return sess, nil },
	}
	in := ChunkInputs{Chunks: []Chunk{{Index: 0, Transcript: "hi", Bytes: 2, SourceLabels: []string{"codex"}}}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}
	if _, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{ProjectRoot: "/repo"}); err != nil {
		t.Fatalf("nil Capture must be a no-op, got %v", err)
	}
}
