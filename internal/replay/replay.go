// Package replay re-runs captured LLM calls. Two modes:
//
//   - Reparse: decode a stored raw response again with the current rule
//     packs. No LLM call, no cost — recovers mistakes that were dropped
//     because the response failed to parse (or thresholds changed).
//   - Resend: send the stored prompt back to a provider session, optionally
//     overriding the provider and model, then decode the fresh response.
package replay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/capture"
	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/state"
)

// DefaultTimeoutSeconds mirrors the analyzer's default per-rule timeout.
const DefaultTimeoutSeconds = 45

// providerStartTimeout bounds how long a resent provider may take to start.
const providerStartTimeout = 30 * time.Second

// ErrCallNotFound is returned when callIndex is out of range for the run.
var ErrCallNotFound = errors.New("replay: call index out of range")

// Result reports the outcome of one replay operation.
type Result struct {
	Mode        string             `json:"mode"`
	ParentRunID string             `json:"parent_run_id"`
	CallIndex   int                `json:"call_index"`
	ReplayRunID string             `json:"replay_run_id,omitempty"` // resend only
	ProviderID  string             `json:"provider_id,omitempty"`
	Model       string             `json:"model,omitempty"`
	Summary     string             `json:"summary,omitempty"`
	Mistakes    []analyzer.Mistake `json:"mistakes"`
	ParseError  string             `json:"parse_error,omitempty"`
	Warnings    []string           `json:"warnings,omitempty"`
	DurationMS  int64              `json:"duration_ms"`
	ByCategory  map[string]int     `json:"by_category"`
}

// Reparse decodes a stored phase-1 response again using the given packs.
// Pure computation: no provider is started and nothing is persisted.
func Reparse(outputRoot, projectName, runID string, callIndex int, packs []analyzer.RulePack) (Result, error) {
	started := time.Now()
	_, records, err := capture.LoadRun(capture.RunsRoot(outputRoot, projectName), runID)
	if err != nil {
		return Result{}, fmt.Errorf("replay: load run %s: %w", runID, err)
	}
	rec, err := phase1Record(records, callIndex)
	if err != nil {
		return Result{}, err
	}

	mistakesByCat, summary, warnings, parseErr := analyzer.ParsePhase1Response(rec.Response, packs)
	result := Result{
		Mode:        capture.ReplayModeReparse,
		ParentRunID: runID,
		CallIndex:   callIndex,
		Summary:     summary,
		Mistakes:    flattenMistakes(mistakesByCat),
		Warnings:    warnings,
		DurationMS:  time.Since(started).Milliseconds(),
		ByCategory:  countByCategory(mistakesByCat),
	}
	if parseErr != nil {
		result.ParseError = parseErr.Error()
	}
	return result, nil
}

// Options configures a Resend operation.
type Options struct {
	OutputRoot  string
	ProjectName string
	RunID       string
	CallIndex   int

	// ProviderOverride / ModelOverride replace the captured values when
	// non-empty. Empty keeps the original session parameters.
	ProviderOverride string
	ModelOverride    string

	AppConfig *config.App
	// Packs are the rule packs used to decode the new response.
	Packs []analyzer.RulePack
	// RuleTimeoutSecs caps the resent LLM call; <=0 uses DefaultTimeoutSeconds.
	RuleTimeoutSecs int

	Logger *logging.Logger

	// sessionFor builds the provider session plus a teardown func. Nil
	// selects the real registry path; tests inject fakes here.
	sessionFor func(providerID, model, sandbox, systemMsg, workDir string) (analyzer.Session, func(), error)
}

// Resend sends the stored prompt of one captured phase-1 call back through
// a provider session and persists the exchange as a new capture run tagged
// as a replay of the parent.
func Resend(ctx context.Context, opts Options) (Result, error) {
	if opts.AppConfig == nil {
		return Result{}, errors.New("replay: AppConfig is required")
	}
	timeout := opts.RuleTimeoutSecs
	if timeout <= 0 {
		timeout = DefaultTimeoutSeconds
	}

	meta, records, err := capture.LoadRun(capture.RunsRoot(opts.OutputRoot, opts.ProjectName), opts.RunID)
	if err != nil {
		return Result{}, fmt.Errorf("replay: load run %s: %w", opts.RunID, err)
	}
	parent, err := phase1Record(records, opts.CallIndex)
	if err != nil {
		return Result{}, err
	}
	if _, err := os.Stat(meta.ProjectPath); err != nil {
		return Result{}, fmt.Errorf("replay: project path from captured run %q: %w", meta.ProjectPath, err)
	}

	providerID := firstNonEmpty(opts.ProviderOverride, meta.ProviderID)
	if !analyzer.IsRegisteredProvider(providerID) {
		return Result{}, fmt.Errorf("replay: provider %q is not registered (known: %s)",
			providerID, strings.Join(analyzer.RegisteredProviderIDStrings(), ", "))
	}
	model := firstNonEmpty(opts.ModelOverride, meta.Model)

	replayRunID, err := capture.NewRunID()
	if err != nil {
		return Result{}, err
	}
	buildSession := opts.sessionFor
	if buildSession == nil {
		buildSession = func(pid, mdl, sandbox, systemMsg, workDir string) (analyzer.Session, func(), error) {
			s, closeP, serr := newRegistrySession(ctx, opts.AppConfig, pid, mdl, sandbox, systemMsg, workDir, replayRunID)
			return s, closeP, serr
		}
	}
	session, closeProvider, err := buildSession(providerID, model, meta.Sandbox, meta.SystemMessage, meta.ProjectPath)
	if err != nil {
		return Result{}, fmt.Errorf("replay: open session: %w", err)
	}
	defer closeProvider()

	started := time.Now()
	raw, runErr := session.Run(ctx, parent.Prompt, time.Duration(timeout)*time.Second)
	elapsed := time.Since(started)

	mistakesByCat, summary, warnings, parseErr := analyzer.ParsePhase1Response(raw, opts.Packs)

	status := capture.StatusOK
	errText := ""
	switch {
	case runErr != nil:
		status = capture.StatusError
		errText = state.TruncateError(runErr.Error())
	case parseErr != nil:
		status = capture.StatusParseFailed
		errText = state.TruncateError(parseErr.Error())
	}

	replayMeta := capture.RunMeta{
		RunID:         replayRunID,
		ProjectName:   opts.ProjectName,
		ProjectPath:   meta.ProjectPath,
		ProviderID:    providerID,
		Model:         model,
		Sandbox:       meta.Sandbox,
		SystemMessage: meta.SystemMessage,
		StartedAt:     time.Now().UTC(),
		Kind:          capture.KindReplay,
		ReplayMode:    capture.ReplayModeResend,
		ParentRunID:   opts.RunID,
		ParentCallIdx: opts.CallIndex,
	}
	w, werr := capture.Open(capture.RunDir(opts.OutputRoot, opts.ProjectName, replayRunID), replayMeta)
	if werr == nil {
		defer func() { _ = w.Close() }()
		recordErr := w.Append(capture.Record{
			Phase:      capture.PhasePhase1,
			ChunkIndex: parent.ChunkIndex,
			ChunkCount: parent.ChunkCount,
			Prompt:     parent.Prompt,
			Response:   redactForStorage(opts, raw),
			Status:     status,
			Error:      errText,
			DurationMS: elapsed.Milliseconds(),
		})
		if recordErr != nil && opts.Logger != nil {
			opts.Logger.Warn("replay capture append failed", logging.Any("err", recordErr))
		}
	} else if opts.Logger != nil {
		opts.Logger.Warn("replay capture setup failed", logging.Any("err", werr))
	}

	result := Result{
		Mode:        capture.ReplayModeResend,
		ParentRunID: opts.RunID,
		CallIndex:   opts.CallIndex,
		ReplayRunID: replayRunID,
		ProviderID:  providerID,
		Model:       model,
		Summary:     summary,
		Mistakes:    flattenMistakes(mistakesByCat),
		Warnings:    warnings,
		DurationMS:  elapsed.Milliseconds(),
		ByCategory:  countByCategory(mistakesByCat),
	}
	switch {
	case runErr != nil:
		return result, fmt.Errorf("replay: provider call failed: %w", runErr)
	case parseErr != nil:
		result.ParseError = parseErr.Error()
	}
	return result, nil
}

// redactForStorage scrubs secret-shaped text from a response before it is
// written into the replay's capture file.
func redactForStorage(opts Options, body string) string {
	if body == "" || opts.AppConfig == nil {
		return body
	}
	redactor, err := analyzer.NewRedactor(opts.AppConfig.Redaction.Patterns)
	if err != nil {
		return body // redaction config problem must not lose the payload
	}
	out, _ := redactor.Redact(body)
	return out
}

// newRegistrySession instantiates a real provider session through the
// analyzer registry, mirroring pipeline.runAnalysis's wiring. The returned
// func tears the provider down; callers must invoke it exactly once.
func newRegistrySession(ctx context.Context, appConfig *config.App, providerID, model, sandbox, systemMsg, workDir, runID string) (analyzer.Session, func(), error) {
	block := appConfig.Providers[providerID]
	if block.Model == "" {
		block.Model = model
	}
	providerCfg := analyzer.ProviderConfigFromBlock(providerID, block, appConfig.Sandbox)
	provider, err := analyzer.NewProvider(analyzer.ProviderID(providerID), providerCfg)
	if err != nil {
		return nil, func() {}, fmt.Errorf("instantiate provider %q: %w (%s)", providerID, err, config.RemediationMessage(providerID))
	}
	startCtx, cancel := context.WithTimeout(ctx, providerStartTimeout)
	defer cancel()
	if err := provider.Start(startCtx); err != nil {
		_ = provider.Close()
		return nil, func() {}, fmt.Errorf("start provider %q: %w (%s)", providerID, err, config.RemediationMessage(providerID))
	}
	session, err := provider.NewSession(ctx, analyzer.SessionConfig{
		WorkingDirectory: workDir,
		Model:            model,
		ReadOnly:         true,
		SystemMessage:    systemMsg,
		RunID:            runID,
		Sandbox:          sandbox,
	})
	if err != nil {
		_ = provider.Close()
		return nil, func() {}, err
	}
	closed := false
	return session, func() {
		if closed {
			return
		}
		closed = true
		_ = session.Close()
		_ = provider.Close()
	}, nil
}

func phase1Record(records []capture.Record, index int) (capture.Record, error) {
	if index < 0 || index >= len(records) {
		return capture.Record{}, fmt.Errorf("%w: run has %d calls, got index %d", ErrCallNotFound, len(records), index)
	}
	rec := records[index]
	if rec.Phase != capture.PhasePhase1 {
		return capture.Record{}, fmt.Errorf("replay: only phase-1 calls can be replayed (call %d is %s)", index, rec.Phase)
	}
	return rec, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// flattenMistakes converts the by-category map into an ordered slice so
// JSON output stays deterministic.
func flattenMistakes(byCat map[analyzer.RuleCategory][]analyzer.Mistake) []analyzer.Mistake {
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, string(c))
	}
	sort.Strings(cats)
	out := make([]analyzer.Mistake, 0, 16)
	for _, c := range cats {
		out = append(out, byCat[analyzer.RuleCategory(c)]...)
	}
	return out
}

func countByCategory(byCat map[analyzer.RuleCategory][]analyzer.Mistake) map[string]int {
	out := make(map[string]int, len(byCat))
	for c, list := range byCat {
		out[string(c)] = len(list)
	}
	return out
}
