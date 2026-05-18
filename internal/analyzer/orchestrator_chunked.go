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
	CodebaseFiles   []string // path-only, fed to phase-2 grounding
	RuleTimeoutSecs int      // applies to each phase-1 chunk call and phase-2 call
}

// RunChunks executes phase 1 (per chunk) and phase 2 (single union call) against
// a SessionPool. Sequential mode chains summaries across chunks; parallel runs
// chunks independently.
func (o *Orchestrator) RunChunks(ctx context.Context, rc RunConfig, in ChunkInputs, req PhaseRequest) (AnalysisResult, error) {
	if rc.SessionFactory == nil {
		return AnalysisResult{}, errors.New("RunChunks: RunConfig.SessionFactory is required")
	}
	if len(in.Chunks) == 0 {
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
		if poolCap <= 0 || poolCap > len(in.Chunks) {
			poolCap = len(in.Chunks)
		}
	}
	pool := NewSessionPool(poolCap, rc.SessionFactory)
	defer pool.Close()

	mistakesByCategory, completedChunks, p1Warnings, err := o.runPhase1(ctx, rc, pool, builder, in, req)
	result := AnalysisResult{Warnings: p1Warnings}
	if err != nil {
		return result, err
	}
	result.Mistakes = orderedByCategory(mistakesByCategory, enabled)

	if req.DryRun || len(result.Mistakes) == 0 {
		if completedChunks == len(in.Chunks) {
			result.CompletedCategories = stringsFromCategories(enabled)
		}
		return result, nil
	}

	findingsByCategory, p2Warnings, err := o.runPhase2(ctx, pool, builder, mistakesByCategory, in.CodebaseFiles, in.RuleTimeoutSecs, req)
	result.Warnings = append(result.Warnings, p2Warnings...)
	if err != nil {
		return result, err
	}

	for _, c := range enabled {
		validated, validationWarnings := validateFindings(findingsByCategory[c], packForCategory(o.Packs, c), req)
		result.Warnings = append(result.Warnings, validationWarnings...)
		findingsByCategory[c] = validated
	}
	result.Findings = orderedByCategory(findingsByCategory, enabled)

	if completedChunks == len(in.Chunks) {
		result.CompletedCategories = stringsFromCategories(enabled)
	}
	return result, nil
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
func (o *Orchestrator) runPhase1(ctx context.Context, rc RunConfig, pool *SessionPool, builder *PromptBuilder, in ChunkInputs, req PhaseRequest) (map[RuleCategory][]Mistake, int, []string, error) {
	if rc.Mode == ModeParallel && len(in.Chunks) > 1 {
		return o.runPhase1Parallel(ctx, pool, builder, in, req)
	}
	return o.runPhase1Sequential(ctx, pool, builder, in, req)
}

func (o *Orchestrator) runPhase1Sequential(ctx context.Context, pool *SessionPool, builder *PromptBuilder, in ChunkInputs, req PhaseRequest) (map[RuleCategory][]Mistake, int, []string, error) {
	mistakes := map[RuleCategory][]Mistake{}
	warnings := []string{}
	priorSummary := ""
	completed := 0
	timeout := chunkTimeout(in.RuleTimeoutSecs)

	for i, chunk := range in.Chunks {
		prompt := builder.BuildPhase1(chunk, req, priorSummary, len(in.Chunks))
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
		final := i == len(in.Chunks)-1
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

func (o *Orchestrator) runPhase1Parallel(ctx context.Context, pool *SessionPool, builder *PromptBuilder, in ChunkInputs, req PhaseRequest) (map[RuleCategory][]Mistake, int, []string, error) {
	results := make([]chunkResult, len(in.Chunks))
	timeout := chunkTimeout(in.RuleTimeoutSecs)

	g, gctx := errgroup.WithContext(ctx)
	for i, chunk := range in.Chunks {
		i, chunk := i, chunk
		g.Go(func() error {
			prompt := builder.BuildPhase1(chunk, req, "", len(in.Chunks))
			raw, runErr := runWithPool(gctx, pool, prompt, timeout)
			if runErr != nil {
				results[i] = chunkResult{index: i, err: runErr}
				if errs.Is(runErr, errs.KindRateLimit) {
					return runErr
				}
				return nil
			}
			parsed, summary, parseWarns, parseErr := parsePhase1Response(raw, o.Packs)
			cr := chunkResult{index: i, mistakesByCat: parsed, summary: summary, warnings: parseWarns, parseErr: parseErr}
			if parseErr != nil {
				cr.warnings = append(cr.warnings, fmt.Sprintf("phase-1 chunk %d parse failed (%v); dropping its mistakes", i, parseErr))
			}
			results[i] = cr
			return nil
		})
	}
	gErr := g.Wait()

	mistakes := map[RuleCategory][]Mistake{}
	warnings := []string{}
	completed := 0
	for _, r := range results {
		warnings = append(warnings, r.warnings...)
		if r.err != nil {
			warnings = append(warnings, fmt.Sprintf("phase-1 chunk %d failed (%v)", r.index, r.err))
			continue
		}
		if r.parseErr != nil {
			continue
		}
		mergeMistakes(mistakes, r.mistakesByCat)
		completed++
	}
	if gErr != nil && errs.Is(gErr, errs.KindRateLimit) {
		return mistakes, completed, warnings, fmt.Errorf("phase-1 hit provider rate limit: %w", gErr)
	}
	return mistakes, completed, warnings, nil
}

func (o *Orchestrator) runPhase2(ctx context.Context, pool *SessionPool, builder *PromptBuilder, mistakes map[RuleCategory][]Mistake, files []string, ruleTimeoutSecs int, req PhaseRequest) (map[RuleCategory][]Finding, []string, error) {
	prompt, fileWarnings := builder.BuildPhase2(mistakes, files, req)
	raw, err := runWithPool(ctx, pool, prompt, chunkTimeout(ruleTimeoutSecs))
	if err != nil {
		if errs.Is(err, errs.KindRateLimit) {
			return nil, fileWarnings, fmt.Errorf("phase-2 hit provider rate limit: %w", err)
		}
		return nil, append(fileWarnings, fmt.Sprintf("phase-2 failed (%v)", err)), err
	}
	parsed, parseWarns, parseErr := parsePhase2Response(raw, o.Packs)
	warns := append(fileWarnings, parseWarns...)
	if parseErr != nil {
		return nil, append(warns, fmt.Sprintf("phase-2 parse failed (%v)", parseErr)), parseErr
	}
	return parsed, warns, nil
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

const defaultRuleTimeoutSecs = 45

func chunkTimeout(secs int) time.Duration {
	if secs <= 0 {
		secs = defaultRuleTimeoutSecs
	}
	return time.Duration(secs) * time.Second
}

// mergeMistakes merges src into dst, appending in encounter order.
func mergeMistakes(dst, src map[RuleCategory][]Mistake) {
	for c, ms := range src {
		dst[c] = append(dst[c], ms...)
	}
}

// packForCategory returns the matching pack from packs (zero value if absent).
func packForCategory(packs []RulePack, c RuleCategory) RulePack {
	for _, p := range packs {
		if p.Category == c {
			return p
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
