package analyzer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dreamer/internal/errs"
	"golang.org/x/sync/errgroup"
)

// ChunkInputs is the phase-1 input bundle for the chunked orchestrator path.
type ChunkInputs struct {
	Chunks          []Chunk
	RuleTimeoutSecs int // applies to each phase-1 chunk call and phase-2 call
}

// RunChunks executes phase 1 (per chunk) and phase 2 (single union call) against
// a SessionPool. Sequential mode chains summaries across chunks; parallel runs
// chunks independently.
func (o *Orchestrator) RunChunks(ctx context.Context, rc RunConfig, chunkInputs ChunkInputs, req PhaseRequest) (AnalysisResult, error) {
	if rc.Phase1SessionFactory == nil {
		return AnalysisResult{}, errors.New("RunChunks: RunConfig.Phase1SessionFactory is required")
	}
	if len(chunkInputs.Chunks) == 0 {
		return AnalysisResult{}, errors.New("RunChunks: at least one chunk is required")
	}

	builder := NewPromptBuilder(o.Packs)
	enabled := builder.EnabledCategories()
	if len(enabled) == 0 {
		return AnalysisResult{}, errors.New("RunChunks: no enabled categories")
	}

	poolCap := 1
	if rc.Mode == ModeParallel {
		poolCap = rc.MaxConcurrency
		if poolCap <= 0 || poolCap > len(chunkInputs.Chunks) {
			poolCap = len(chunkInputs.Chunks)
		}
	}
	phase1Pool := NewSessionPool(poolCap, rc.Phase1Factory())
	defer phase1Pool.Close()

	mistakesByCategory, completedChunks, p1Warnings, err := o.runPhase1(ctx, rc, phase1Pool, builder, chunkInputs, req)
	analysisResult := AnalysisResult{Warnings: p1Warnings}
	if err != nil {
		return analysisResult, err
	}
	analysisResult.Mistakes = orderedByCategory(mistakesByCategory, enabled)

	if req.DryRun || len(analysisResult.Mistakes) == 0 {
		if completedChunks == len(chunkInputs.Chunks) {
			analysisResult.CompletedCategories = stringsFromCategories(enabled)
		}
		return analysisResult, nil
	}

	// Phase 2 uses a single session — a fresh pool with a Phase-2-enabled
	// factory so MCP / CLI tool wiring stays out of Phase 1 sessions.
	phase2Pool := NewSessionPool(1, rc.Phase2Factory())
	defer phase2Pool.Close()

	findingsByCategory, p2Warnings, err := o.runPhase2(ctx, phase2Pool, builder, mistakesByCategory, chunkInputs.RuleTimeoutSecs, req)
	analysisResult.Warnings = append(analysisResult.Warnings, p2Warnings...)
	if err != nil {
		return analysisResult, err
	}

	for _, c := range enabled {
		validated, validationWarnings := validateFindings(findingsByCategory[c], packForCategory(o.Packs, c), req)
		analysisResult.Warnings = append(analysisResult.Warnings, validationWarnings...)
		findingsByCategory[c] = validated
	}
	analysisResult.Findings = orderedByCategory(findingsByCategory, enabled)

	if completedChunks == len(chunkInputs.Chunks) {
		analysisResult.CompletedCategories = stringsFromCategories(enabled)
	}
	return analysisResult, nil
}

type chunkResult struct {
	index         int
	mistakesByCat map[RuleCategory][]Mistake
	summary       string
	warnings      []string
	err           error
	parseErr      error
}

// runPhase1 dispatches per-chunk calls; returns the union, count of clean chunks, warnings.
func (o *Orchestrator) runPhase1(ctx context.Context, rc RunConfig, pool *SessionPool, builder *PromptBuilder, chunkInputs ChunkInputs, req PhaseRequest) (map[RuleCategory][]Mistake, int, []string, error) {
	if rc.Mode == ModeParallel && len(chunkInputs.Chunks) > 1 {
		return o.runPhase1Parallel(ctx, pool, builder, chunkInputs, req)
	}
	return o.runPhase1Sequential(ctx, pool, builder, chunkInputs, req)
}

func (o *Orchestrator) runPhase1Sequential(ctx context.Context, pool *SessionPool, builder *PromptBuilder, chunkInputs ChunkInputs, req PhaseRequest) (map[RuleCategory][]Mistake, int, []string, error) {
	mistakes := map[RuleCategory][]Mistake{}
	warnings := []string{}
	priorSummary := ""
	completed := 0
	timeout := chunkTimeout(chunkInputs.RuleTimeoutSecs)

	for i, chunk := range chunkInputs.Chunks {
		prompt := builder.BuildPhase1(chunk, req, priorSummary, len(chunkInputs.Chunks))
		raw, runErr := runWithPool(ctx, pool, prompt, timeout)
		if runErr != nil {
			if errs.Is(runErr, errs.KindRateLimit) {
				return mistakes, completed, warnings, fmt.Errorf("phase-1 chunk %d hit provider rate limit: %w", i, runErr)
			}
			warnings = append(warnings, fmt.Sprintf("phase-1 chunk %d failed (%v); aborting", i, runErr))
			return mistakes, completed, warnings, runErr
		}
		parsed, summary, parseWarns, parseErr := parsePhase1Response(raw, o.Packs)
		warnings = append(warnings, parseWarns...)
		if parseErr != nil {
			warnings = append(warnings, fmt.Sprintf("phase-1 chunk %d parse failed (%v); dropping its mistakes", i, parseErr))
			priorSummary = ""
			continue
		}
		final := i == len(chunkInputs.Chunks)-1
		if !final && summary == "" {
			warnings = append(warnings, fmt.Sprintf("schema violation chunk=%d (missing summary)", i))
			return mistakes, completed, warnings, fmt.Errorf("phase-1 schema violation: chunk %d missing summary", i)
		}
		mergeMistakes(mistakes, parsed)
		priorSummary = summary
		completed++
	}
	return mistakes, completed, warnings, nil
}

func (o *Orchestrator) runPhase1Parallel(ctx context.Context, pool *SessionPool, builder *PromptBuilder, chunkInputs ChunkInputs, req PhaseRequest) (map[RuleCategory][]Mistake, int, []string, error) {
	results := make([]chunkResult, len(chunkInputs.Chunks))
	timeout := chunkTimeout(chunkInputs.RuleTimeoutSecs)

	g, gctx := errgroup.WithContext(ctx)
	for i, chunk := range chunkInputs.Chunks {
		i, chunk := i, chunk
		g.Go(func() error {
			prompt := builder.BuildPhase1(chunk, req, "", len(chunkInputs.Chunks))
			raw, runErr := runWithPool(gctx, pool, prompt, timeout)
			if runErr != nil {
				results[i] = chunkResult{index: i, err: runErr}
				if errs.Is(runErr, errs.KindRateLimit) {
					return runErr
				}
				return nil
			}
			parsed, summary, parseWarns, parseErr := parsePhase1Response(raw, o.Packs)
			chunkRes := chunkResult{index: i, mistakesByCat: parsed, summary: summary, warnings: parseWarns, parseErr: parseErr}
			if parseErr != nil {
				chunkRes.warnings = append(chunkRes.warnings, fmt.Sprintf("phase-1 chunk %d parse failed (%v); dropping its mistakes", i, parseErr))
			}
			results[i] = chunkRes
			return nil
		})
	}
	gErr := g.Wait()

	mistakes := map[RuleCategory][]Mistake{}
	warnings := []string{}
	completed := 0
	for _, chunkRes := range results {
		warnings = append(warnings, chunkRes.warnings...)
		if chunkRes.err != nil {
			warnings = append(warnings, fmt.Sprintf("phase-1 chunk %d failed (%v)", chunkRes.index, chunkRes.err))
			continue
		}
		if chunkRes.parseErr != nil {
			continue
		}
		mergeMistakes(mistakes, chunkRes.mistakesByCat)
		completed++
	}
	if gErr != nil && errs.Is(gErr, errs.KindRateLimit) {
		return mistakes, completed, warnings, fmt.Errorf("phase-1 hit provider rate limit: %w", gErr)
	}
	return mistakes, completed, warnings, nil
}

// phase2ToolMultiplier extends the timeout for phase 2 to account for
// tool-use verification (Grep/Read/Glob calls take longer than a single
// LLM completion).
const phase2ToolMultiplier = 3

// runPhase2 dispatches to the transport's decoder. The decoder owns the
// "where do findings come from" question — inline JSON for the legacy
// transport, or the JSONL file the recording transport wrote.
func (o *Orchestrator) runPhase2(ctx context.Context, pool *SessionPool, builder *PromptBuilder, mistakes map[RuleCategory][]Mistake, ruleTimeoutSecs int, req PhaseRequest) (map[RuleCategory][]Finding, []string, error) {
	prompt, promptWarnings := builder.BuildPhase2(mistakes, req)
	timeout := chunkTimeout(ruleTimeoutSecs) * phase2ToolMultiplier
	raw, err := runWithPool(ctx, pool, prompt, timeout)
	if err != nil {
		if errs.Is(err, errs.KindRateLimit) {
			return nil, promptWarnings, fmt.Errorf("phase-2 hit provider rate limit: %w", err)
		}
		return nil, append(promptWarnings, fmt.Sprintf("phase-2 failed (%v)", err)), err
	}

	decoder := lookupPhase2Decoder(req.Phase2Mode)
	findingsByCategory, decoderWarnings, decodeErr := decoder.decode(raw, req, o.Packs)
	warnings := append(promptWarnings, decoderWarnings...)
	if decodeErr != nil {
		return nil, append(warnings, fmt.Sprintf("phase-2 decode failed (%v)", decodeErr)), decodeErr
	}
	if findingsByCategory == nil {
		findingsByCategory = map[RuleCategory][]Finding{}
	}
	return findingsByCategory, warnings, nil
}

// runWithPool acquires a session, runs the prompt once, releases.
// Sessions are discarded (ok=false) on errors so the pool rebuilds them.
func runWithPool(ctx context.Context, pool *SessionPool, prompt string, timeout time.Duration) (string, error) {
	s, err := pool.Acquire(ctx)
	if err != nil {
		return "", err
	}
	out, runErr := s.Run(ctx, prompt, timeout)
	pool.Release(s, runErr == nil)
	return out, runErr
}

func chunkTimeout(secs int) time.Duration {
	if secs <= 0 {
		secs = defaultRuleTimeoutSeconds
	}
	return time.Duration(secs) * time.Second
}

// mergeMistakes merges src into dst, appending in encounter order.
func mergeMistakes(dst, src map[RuleCategory][]Mistake) {
	for category, mistakes := range src {
		dst[category] = append(dst[category], mistakes...)
	}
}

// packForCategory returns the matching pack from packs (zero value if absent).
func packForCategory(packs []RulePack, c RuleCategory) RulePack {
	// Range by index to avoid copying the RulePack struct (≈150 bytes) on
	// every iteration.
	for i := range packs {
		if packs[i].Category == c {
			return packs[i]
		}
	}
	return RulePack{Category: c, Enabled: true}
}

// stringsFromCategories converts []RuleCategory to []string preserving order.
func stringsFromCategories(in []RuleCategory) []string {
	out := make([]string, len(in))
	for i, c := range in {
		out[i] = string(c)
	}
	return out
}
