package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSession captures prompts and replies based on a per-test handler.
type fakeSession struct {
	handler func(prompt string) (string, error)
	closed  bool
	mu      sync.Mutex
	prompts []string
}

func (s *fakeSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	s.mu.Lock()
	s.prompts = append(s.prompts, prompt)
	s.mu.Unlock()
	if s.handler == nil {
		return "{}", nil
	}
	return s.handler(prompt)
}
func (s *fakeSession) Close() error { s.closed = true; return nil }

type capturedTranscript struct {
	mu         sync.Mutex
	allPrompts []string
	callCount  int32
}

func (c *capturedTranscript) record(prompt string) {
	atomic.AddInt32(&c.callCount, 1)
	c.mu.Lock()
	c.allPrompts = append(c.allPrompts, prompt)
	c.mu.Unlock()
}

func newCapturingSession(handler func(prompt string) (string, error), cap *capturedTranscript) *fakeSession {
	wrapped := func(p string) (string, error) {
		cap.record(p)
		return handler(p)
	}
	return &fakeSession{handler: wrapped}
}

// minimalPacks returns enabled packs for the given categories.
func minimalPacks(cats ...RuleCategory) []RulePack {
	out := make([]RulePack, len(cats))
	for i, c := range cats {
		out[i] = RulePack{Category: c, Enabled: true, TimeoutSeconds: 10}
	}
	return out
}

func phase1Reply(summary string, mistakes map[string][]map[string]any) string {
	payload := map[string]any{"summary": summary, "mistakes": mistakes}
	b, _ := json.Marshal(payload)
	return string(b)
}

func phase2Reply(findings map[string][]map[string]any) string {
	b, _ := json.Marshal(map[string]any{"findings": findings})
	return string(b)
}

func newPool(t *testing.T, sessions ...*fakeSession) *SessionPool {
	t.Helper()
	idx := int32(-1)
	return NewSessionPool(len(sessions), func() (Session, error) {
		i := atomic.AddInt32(&idx, 1)
		if int(i) >= len(sessions) {
			return nil, fmt.Errorf("no more sessions (want index %d, have %d)", i, len(sessions))
		}
		return sessions[i], nil
	})
}

func TestRunChunksSingleChunkExactlyTwoCalls(t *testing.T) {
	cap := &capturedTranscript{}
	sess := newCapturingSession(func(p string) (string, error) {
		if strings.Contains(p, "auditing chat transcripts") {
			return phase1Reply("first chunk summary", map[string][]map[string]any{
				"test": {{"category": "test", "summary": "missing edge-case test", "evidence_excerpt": "no test", "confidence": 0.9}},
			}), nil
		}
		return phase2Reply(map[string][]map[string]any{
			"test": {{"category": "test", "mistake": "missing edge-case test", "guardrail": map[string]any{"kind": "test", "tool": "go test", "rule": "edge"}, "confidence": 0.9}},
		}), nil
	}, cap)

	rc := RunConfig{Mode: ModeSequential, SessionFactory: func() (Session, error) { return sess, nil }}
	in := ChunkInputs{Chunks: []Chunk{{Index: 0, Transcript: "hi", Bytes: 2, SourceLabels: []string{"codex"}}}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	res, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{ProjectRoot: "/repo"})
	if err != nil {
		t.Fatalf("RunChunks: %v", err)
	}
	if got := atomic.LoadInt32(&cap.callCount); got != 2 {
		t.Fatalf("call count = %d, want 2 (phase-1 + phase-2)", got)
	}
	if got, want := len(res.Findings), 1; got != want {
		t.Fatalf("len(Findings) = %d, want %d", got, want)
	}
	if got := res.CompletedCategories; len(got) != 1 || got[0] != string(RuleCategoryTest) {
		t.Fatalf("CompletedCategories = %v, want [test]", got)
	}
}

func TestRunChunksSequentialSummaryChaining(t *testing.T) {
	cap := &capturedTranscript{}
	chunkSummaries := []string{"sum-0", "sum-1", "sum-2"}
	chunkIdx := 0
	sess := newCapturingSession(func(p string) (string, error) {
		if strings.Contains(p, "synthesizing guardrails") {
			return phase2Reply(map[string][]map[string]any{
				"test": {{"category": "test", "mistake": "x", "guardrail": map[string]any{"kind": "test", "tool": "go test", "rule": "r"}, "confidence": 0.9}},
			}), nil
		}
		s := chunkSummaries[chunkIdx]
		chunkIdx++
		return phase1Reply(s, map[string][]map[string]any{
			"test": {{"category": "test", "summary": fmt.Sprintf("m%d", chunkIdx-1), "evidence_excerpt": "e", "confidence": 0.9}},
		}), nil
	}, cap)

	rc := RunConfig{Mode: ModeSequential, SessionFactory: func() (Session, error) { return sess, nil }}
	in := ChunkInputs{Chunks: []Chunk{
		{Index: 0, Transcript: "a", Bytes: 1, SourceLabels: []string{"codex"}},
		{Index: 1, Transcript: "b", Bytes: 1, SourceLabels: []string{"claude"}},
		{Index: 2, Transcript: "c", Bytes: 1, SourceLabels: []string{"copilot"}},
	}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	if _, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{ProjectRoot: "/r"}); err != nil {
		t.Fatalf("RunChunks: %v", err)
	}

	cap.mu.Lock()
	prompts := append([]string(nil), cap.allPrompts...)
	cap.mu.Unlock()
	if len(prompts) != 4 {
		t.Fatalf("prompts = %d, want 4 (3 phase-1 + 1 phase-2)", len(prompts))
	}
	if strings.Contains(prompts[0], "rolling_context") {
		t.Fatalf("chunk 0 must NOT contain rolling_context")
	}
	if !strings.Contains(prompts[1], "Prior chunks covered:\nsum-0") {
		t.Fatalf("chunk 1 must contain prior summary sum-0; got:\n%s", prompts[1])
	}
	if !strings.Contains(prompts[2], "Prior chunks covered:\nsum-1") {
		t.Fatalf("chunk 2 must contain prior summary sum-1; got:\n%s", prompts[2])
	}
}

func TestRunChunksParallelHasNoRollingContext(t *testing.T) {
	cap := &capturedTranscript{}
	mk := func() *fakeSession {
		return newCapturingSession(func(p string) (string, error) {
			if strings.Contains(p, "synthesizing guardrails") {
				return phase2Reply(map[string][]map[string]any{}), nil
			}
			return phase1Reply("s", map[string][]map[string]any{}), nil
		}, cap)
	}
	pool := []*fakeSession{mk(), mk(), mk()}
	rc := RunConfig{
		Mode:           ModeParallel,
		MaxConcurrency: 3,
		SessionFactory: func() (Session, error) {
			s := pool[0]
			pool = pool[1:]
			return s, nil
		},
	}
	in := ChunkInputs{Chunks: []Chunk{
		{Index: 0, Transcript: "a", SourceLabels: []string{"a"}},
		{Index: 1, Transcript: "b", SourceLabels: []string{"b"}},
		{Index: 2, Transcript: "c", SourceLabels: []string{"c"}},
	}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	if _, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{}); err != nil {
		t.Fatalf("RunChunks: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	for i, p := range cap.allPrompts {
		if strings.Contains(p, "synthesizing guardrails") {
			continue
		}
		if strings.Contains(p, "rolling_context") {
			t.Fatalf("parallel chunk prompt %d unexpectedly contains rolling_context", i)
		}
	}
}

func TestRunChunksSequentialMissingSummaryAborts(t *testing.T) {
	cap := &capturedTranscript{}
	idx := 0
	sess := newCapturingSession(func(p string) (string, error) {
		if strings.Contains(p, "synthesizing guardrails") {
			t.Fatalf("phase-2 must not fire after schema violation")
		}
		i := idx
		idx++
		if i == 0 {
			return `{"mistakes": {"test": []}}`, nil
		}
		return phase1Reply("ok", map[string][]map[string]any{}), nil
	}, cap)

	rc := RunConfig{Mode: ModeSequential, SessionFactory: func() (Session, error) { return sess, nil }}
	in := ChunkInputs{Chunks: []Chunk{
		{Index: 0, Transcript: "a"},
		{Index: 1, Transcript: "b"},
		{Index: 2, Transcript: "c"},
	}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	_, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{})
	if err == nil {
		t.Fatalf("expected schema-violation error")
	}
	if !strings.Contains(err.Error(), "missing summary") {
		t.Fatalf("err = %v, want missing-summary mention", err)
	}
	if got := atomic.LoadInt32(&cap.callCount); got != 1 {
		t.Fatalf("call count = %d, want 1 (chunk 0 only)", got)
	}
}

func TestRunChunksSequentialFinalChunkSummaryOptional(t *testing.T) {
	cap := &capturedTranscript{}
	idx := 0
	sess := newCapturingSession(func(p string) (string, error) {
		if strings.Contains(p, "synthesizing guardrails") {
			return phase2Reply(map[string][]map[string]any{}), nil
		}
		i := idx
		idx++
		if i == 1 {
			return `{"mistakes": {}}`, nil
		}
		return phase1Reply("s", map[string][]map[string]any{}), nil
	}, cap)

	rc := RunConfig{Mode: ModeSequential, SessionFactory: func() (Session, error) { return sess, nil }}
	in := ChunkInputs{Chunks: []Chunk{{Index: 0, Transcript: "a"}, {Index: 1, Transcript: "b"}}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	res, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{})
	if err != nil {
		t.Fatalf("RunChunks: %v (warnings=%v)", err, res.Warnings)
	}
}

func TestRunChunksPhase2ToolUseInstructions(t *testing.T) {
	cap := &capturedTranscript{}
	sess := newCapturingSession(func(p string) (string, error) {
		if strings.Contains(p, "synthesizing guardrails") {
			return phase2Reply(map[string][]map[string]any{}), nil
		}
		return phase1Reply("s", map[string][]map[string]any{
			"test": {{"summary": "m", "evidence_excerpt": "e", "confidence": 0.9}},
		}), nil
	}, cap)

	rc := RunConfig{Mode: ModeSequential, SessionFactory: func() (Session, error) { return sess, nil }}
	in := ChunkInputs{
		Chunks:        []Chunk{{Transcript: "x"}},
		CodebaseFiles: []string{"a.go", "b.go", "sub/c.go"},
	}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	if _, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{}); err != nil {
		t.Fatalf("RunChunks: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	p2 := cap.allPrompts[1]

	// Phase 2 now contains tool-use instructions instead of file paths.
	for _, want := range []string{"Grep(", "Read(", "Glob("} {
		if !strings.Contains(p2, want) {
			t.Fatalf("phase-2 prompt missing tool-use instruction %q:\n%s", want, p2)
		}
	}
	// File paths should NOT appear as a dump (no path-only grounding).
	for _, avoid := range []string{"a.go\n", "b.go\n", "sub/c.go\n"} {
		if strings.Contains(p2, avoid) {
			t.Fatalf("phase-2 prompt unexpectedly contains file path dump:\n%s", p2)
		}
	}
}

// 6-category single-chunk run = 2 calls (1 phase-1 + 1 phase-2).
func TestRunChunksSixCategoriesExactlyTwoCalls(t *testing.T) {
	cap := &capturedTranscript{}
	sess := newCapturingSession(func(p string) (string, error) {
		if strings.Contains(p, "synthesizing guardrails") {
			return phase2Reply(map[string][]map[string]any{}), nil
		}
		return phase1Reply("s", map[string][]map[string]any{
			"test": {{"category": "test", "summary": "m", "evidence_excerpt": "e", "confidence": 0.9}},
		}), nil
	}, cap)
	rc := RunConfig{Mode: ModeSequential, SessionFactory: func() (Session, error) { return sess, nil }}
	in := ChunkInputs{Chunks: []Chunk{{Index: 0, Transcript: "x"}}}
	orch := &Orchestrator{Packs: minimalPacks(
		RuleCategoryLintRule, RuleCategoryTest, RuleCategoryCICheck,
		RuleCategoryDoc, RuleCategoryConfig, RuleCategoryRefactorBoundary,
	)}
	if _, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{}); err != nil {
		t.Fatalf("RunChunks: %v", err)
	}
	if got := atomic.LoadInt32(&cap.callCount); got != 2 {
		t.Fatalf("call count = %d, want 2 (1 phase-1 + 1 phase-2)", got)
	}
}

// K=1 chunk: no rolling_context regardless of mode.
func TestRunChunksK1NoRollingContext(t *testing.T) {
	cap := &capturedTranscript{}
	sess := newCapturingSession(func(p string) (string, error) {
		if strings.Contains(p, "synthesizing guardrails") {
			return phase2Reply(map[string][]map[string]any{}), nil
		}
		return phase1Reply("s", map[string][]map[string]any{}), nil
	}, cap)
	rc := RunConfig{Mode: ModeSequential, SessionFactory: func() (Session, error) { return sess, nil }}
	in := ChunkInputs{Chunks: []Chunk{{Transcript: "x"}}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}
	if _, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{}); err != nil {
		t.Fatalf("RunChunks: %v", err)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	for i, p := range cap.allPrompts {
		if strings.Contains(p, "rolling_context") {
			t.Fatalf("K=1 prompt %d unexpectedly contains rolling_context", i)
		}
	}
}

// Parallel parity K>1 with no chaining: findings equal after sort.
func TestRunChunksParallelParityK3NoChaining(t *testing.T) {
	build := func(mode ExecutionMode) []Finding {
		cap := &capturedTranscript{}
		mk := func() *fakeSession {
			return newCapturingSession(func(p string) (string, error) {
				if strings.Contains(p, "synthesizing guardrails") {
					return phase2Reply(map[string][]map[string]any{
						"test": {
							{"category": "test", "mistake": "A", "guardrail": map[string]any{"kind": "test", "tool": "go test", "rule": "rA"}, "confidence": 0.9},
							{"category": "test", "mistake": "B", "guardrail": map[string]any{"kind": "test", "tool": "go test", "rule": "rB"}, "confidence": 0.9},
						},
					}), nil
				}
				return phase1Reply("ignored", map[string][]map[string]any{
					"test": {{"category": "test", "summary": "m", "evidence_excerpt": "e", "confidence": 0.9}},
				}), nil
			}, cap)
		}
		pool := []*fakeSession{mk(), mk(), mk(), mk()}
		rc := RunConfig{Mode: mode, MaxConcurrency: 3, SessionFactory: func() (Session, error) {
			s := pool[0]
			pool = pool[1:]
			return s, nil
		}}
		in := ChunkInputs{Chunks: []Chunk{{Transcript: "a"}, {Transcript: "b"}, {Transcript: "c"}}}
		orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}
		res, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{})
		if err != nil {
			t.Fatalf("RunChunks(%s): %v", mode, err)
		}
		return res.Findings
	}
	seq := build(ModeSequential)
	par := build(ModeParallel)
	sortFindings(seq)
	sortFindings(par)
	if !sliceFindingsEqual(seq, par) {
		t.Fatalf("K=3 parity broken: seq=%v par=%v", seq, par)
	}
}

// Sequential: one chunk returns garbage JSON, another parses cleanly.
// CompletedCategories must be empty because not all chunks parsed successfully.
func TestRunChunksSequentialParseFailDoesNotMarkCompleted(t *testing.T) {
	cap := &capturedTranscript{}
	idx := 0
	sess := newCapturingSession(func(p string) (string, error) {
		if strings.Contains(p, "synthesizing guardrails") {
			return phase2Reply(map[string][]map[string]any{}), nil
		}
		i := idx
		idx++
		if i == 0 {
			return "not json at all", nil
		}
		return phase1Reply("s", map[string][]map[string]any{
			"test": {{"category": "test", "summary": "m", "evidence_excerpt": "e", "confidence": 0.9}},
		}), nil
	}, cap)

	rc := RunConfig{Mode: ModeSequential, SessionFactory: func() (Session, error) { return sess, nil }}
	in := ChunkInputs{Chunks: []Chunk{
		{Index: 0, Transcript: "a"},
		{Index: 1, Transcript: "b"},
	}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	res, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{})
	if err != nil {
		t.Fatalf("RunChunks: %v", err)
	}
	if len(res.CompletedCategories) != 0 {
		t.Fatalf("CompletedCategories = %v, want empty (chunk 0 parse failed)", res.CompletedCategories)
	}
}

// Parallel: K=3 chunks; one returns garbage. CompletedCategories must be empty.
func TestRunChunksParallelParseFailDoesNotMarkCompleted(t *testing.T) {
	mk := func(badIndex int, at *int32) *fakeSession {
		cap := &capturedTranscript{}
		return newCapturingSession(func(p string) (string, error) {
			if strings.Contains(p, "synthesizing guardrails") {
				return phase2Reply(map[string][]map[string]any{}), nil
			}
			my := int(atomic.AddInt32(at, 1)) - 1
			if my == badIndex {
				return "not json", nil
			}
			return phase1Reply("s", map[string][]map[string]any{
				"test": {{"category": "test", "summary": "m", "evidence_excerpt": "e", "confidence": 0.9}},
			}), nil
		}, cap)
	}
	var counter int32
	pool := []*fakeSession{mk(1, &counter), mk(1, &counter), mk(1, &counter), mk(1, &counter)}
	rc := RunConfig{
		Mode:           ModeParallel,
		MaxConcurrency: 3,
		SessionFactory: func() (Session, error) {
			s := pool[0]
			pool = pool[1:]
			return s, nil
		},
	}
	in := ChunkInputs{Chunks: []Chunk{
		{Index: 0, Transcript: "a"},
		{Index: 1, Transcript: "b"},
		{Index: 2, Transcript: "c"},
	}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	res, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{})
	if err != nil {
		t.Fatalf("RunChunks: %v", err)
	}
	if len(res.CompletedCategories) != 0 {
		t.Fatalf("CompletedCategories = %v, want empty (one chunk parse failed)", res.CompletedCategories)
	}
}

// Sanity: when every chunk parses, all enabled categories are reported completed.
func TestRunChunksSequentialAllParseMarksCompleted(t *testing.T) {
	cap := &capturedTranscript{}
	sess := newCapturingSession(func(p string) (string, error) {
		if strings.Contains(p, "synthesizing guardrails") {
			return phase2Reply(map[string][]map[string]any{}), nil
		}
		return phase1Reply("s", map[string][]map[string]any{
			"test": {{"category": "test", "summary": "m", "evidence_excerpt": "e", "confidence": 0.9}},
		}), nil
	}, cap)
	rc := RunConfig{Mode: ModeSequential, SessionFactory: func() (Session, error) { return sess, nil }}
	in := ChunkInputs{Chunks: []Chunk{
		{Index: 0, Transcript: "a"},
		{Index: 1, Transcript: "b"},
	}}
	orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}

	res, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{})
	if err != nil {
		t.Fatalf("RunChunks: %v", err)
	}
	if len(res.CompletedCategories) != 1 || res.CompletedCategories[0] != string(RuleCategoryTest) {
		t.Fatalf("CompletedCategories = %v, want [test]", res.CompletedCategories)
	}
}

func TestRunChunksPhase2TimeoutMultiplier(t *testing.T) {
	// Verify that phase 2 uses a 3x timeout multiplier.
	timeout := chunkTimeout(10) * phase2ToolMultiplier
	if timeout != 30*time.Second {
		t.Fatalf("phase2 timeout = %v, want 30s (10s * 3)", timeout)
	}
}

func TestPromptBuilderPhase1UsesYAMLPreamble(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryTest, Enabled: true, Phase1Preamble: "Custom preamble for testing.",
			Phase1CategoryDescription: "custom category desc"},
	}
	builder := NewPromptBuilder(packs)
	chunk := Chunk{Index: 0, Transcript: "hello", SourceLabels: []string{"codex"}}
	prompt := builder.BuildPhase1(chunk, PhaseRequest{}, "", 1)

	if !strings.Contains(prompt, "Custom preamble for testing.") {
		t.Fatalf("phase-1 prompt missing custom preamble:\n%s", prompt)
	}
	if !strings.Contains(prompt, "custom category desc") {
		t.Fatalf("phase-1 prompt missing custom category description:\n%s", prompt)
	}
}

func TestPromptBuilderPhase2UsesYAMLTemplates(t *testing.T) {
	packs := []RulePack{
		{
			Category:                RuleCategoryTest,
			Enabled:                 true,
			GuardrailPromptTemplate: "Custom guardrail instructions for test.",
			Phase2Preamble:          "Custom phase2 preamble.",
		},
	}
	builder := NewPromptBuilder(packs)
	mistakes := map[RuleCategory][]Mistake{
		RuleCategoryTest: {{Summary: "m1", Confidence: 0.9}},
	}
	prompt, _ := builder.BuildPhase2(mistakes, nil, PhaseRequest{})

	if !strings.Contains(prompt, "Custom phase2 preamble.") {
		t.Fatalf("phase-2 prompt missing custom preamble:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Custom guardrail instructions for test.") {
		t.Fatalf("phase-2 prompt missing guardrail template:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Grep(") {
		t.Fatalf("phase-2 prompt missing default tool-use instructions:\n%s", prompt)
	}
}

func TestPromptBuilderSkipsDisabledPacks(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryLintRule, Enabled: false, Phase1Preamble: "SHOULD NOT APPEAR"},
		{Category: RuleCategoryTest, Enabled: true, Phase1Preamble: "Correct preamble."},
	}
	builder := NewPromptBuilder(packs)
	chunk := Chunk{Index: 0, Transcript: "x", SourceLabels: []string{"codex"}}
	prompt := builder.BuildPhase1(chunk, PhaseRequest{}, "", 1)

	if strings.Contains(prompt, "SHOULD NOT APPEAR") {
		t.Fatalf("phase-1 prompt used disabled pack's preamble:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Correct preamble.") {
		t.Fatalf("phase-1 prompt missing enabled pack's preamble:\n%s", prompt)
	}
}

func TestFirstEnabledPackNil(t *testing.T) {
	packs := []RulePack{
		{Category: RuleCategoryLintRule, Enabled: false},
		{Category: RuleCategoryTest, Enabled: false},
	}
	// Should not panic when all packs are disabled. The orchestrator
	// rejects this case before calling BuildPhase1, but the builder
	// itself should handle it gracefully (no preamble, no category list).
	builder := NewPromptBuilder(packs)
	chunk := Chunk{Index: 0, Transcript: "x", SourceLabels: []string{"codex"}}
	prompt := builder.BuildPhase1(chunk, PhaseRequest{}, "", 1)
	if strings.Contains(prompt, "Identify recurring mistakes") {
		// Categories section should be empty (no enabled packs).
		if strings.Contains(prompt, "- lint-rule:") || strings.Contains(prompt, "- test:") {
			t.Fatalf("phase-1 prompt should not list disabled categories:\n%s", prompt)
		}
	}
}

func sortFindings(f []Finding) {
	sort.Slice(f, func(i, j int) bool { return f[i].Mistake < f[j].Mistake })
}

func TestRunChunksParallelParityK1(t *testing.T) {
	mk := func(mode ExecutionMode) AnalysisResult {
		cap := &capturedTranscript{}
		sess := newCapturingSession(func(p string) (string, error) {
			if strings.Contains(p, "synthesizing guardrails") {
				return phase2Reply(map[string][]map[string]any{
					"test": {{"category": "test", "mistake": "m", "guardrail": map[string]any{"kind": "test", "tool": "go test", "rule": "r"}, "confidence": 0.9}},
				}), nil
			}
			return phase1Reply("s", map[string][]map[string]any{
				"test": {{"category": "test", "summary": "m", "evidence_excerpt": "e", "confidence": 0.9}},
			}), nil
		}, cap)
		rc := RunConfig{Mode: mode, MaxConcurrency: 1, SessionFactory: func() (Session, error) { return sess, nil }}
		in := ChunkInputs{Chunks: []Chunk{{Transcript: "x"}}}
		orch := &Orchestrator{Packs: minimalPacks(RuleCategoryTest)}
		res, err := orch.RunChunks(context.Background(), rc, in, PhaseRequest{})
		if err != nil {
			t.Fatalf("RunChunks: %v", err)
		}
		return res
	}
	seq := mk(ModeSequential)
	par := mk(ModeParallel)
	if !sliceFindingsEqual(seq.Findings, par.Findings) {
		t.Fatalf("K=1 parity broken: seq=%v par=%v", seq.Findings, par.Findings)
	}
	if !sliceMistakesEqual(seq.Mistakes, par.Mistakes) {
		t.Fatalf("K=1 parity broken: seq mistakes=%v par mistakes=%v", seq.Mistakes, par.Mistakes)
	}
}

func sliceFindingsEqual(a, b []Finding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Hash != b[i].Hash || a[i].Mistake != b[i].Mistake {
			return false
		}
	}
	return true
}
func sliceMistakesEqual(a, b []Mistake) bool {
	if len(a) != len(b) {
		return false
	}
	ax := make([]Mistake, len(a))
	bx := make([]Mistake, len(b))
	copy(ax, a)
	copy(bx, b)
	sort.Slice(ax, func(i, j int) bool { return ax[i].Summary < ax[j].Summary })
	sort.Slice(bx, func(i, j int) bool { return bx[i].Summary < bx[j].Summary })
	for i := range ax {
		if ax[i].Summary != bx[i].Summary || ax[i].Category != bx[i].Category {
			return false
		}
	}
	return true
}
