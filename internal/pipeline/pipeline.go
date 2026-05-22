package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/lintrules"
	"dreamer/internal/analyzer/toolchain"
	"dreamer/internal/chat"
	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/output"
	"dreamer/internal/state"
)

// Options captures everything the pipeline needs for one analyze invocation
// (cli flags + resolved global config).
type Options struct {
	Config      *config.Config
	ProjectPath string
	ProjectName string
	ProviderID  string
	Force       bool
	DryRun      bool
	Permissive  bool
	OutputDir   string
	Since       string

	// ParallelOverride: force analyzer.execution.mode=parallel when true.
	ParallelOverride bool
	// MaxConcurrencyOverride caps parallel session count. 0 = use config.
	MaxConcurrencyOverride int
	// MaxChunkBytesOverride overrides analyzer.chunking.max_chunk_bytes when MaxChunkBytesOverrideSet.
	MaxChunkBytesOverride    int
	MaxChunkBytesOverrideSet bool

	// DiscoveryCache is an optional mtime-based cache that lets the daemon skip
	// the expensive HashFile loop when source files haven't changed. Nil disables caching.
	DiscoveryCache *DiscoveryCache

	// Events, when non-nil, receives run.start/run.done events.
	Events *EventBus

	// LiveConfig, when non-nil, is consulted once at the top of Run and (if a
	// non-nil snapshot is present) replaces Config for the remainder of this
	// invocation. Lets the daemon hot-swap config without rebuilding Options.
	LiveConfig *atomic.Pointer[config.Config]
}

// Result bundles the metrics + paths the analyze command surfaces.
type Result struct {
	TodosPath       string
	Findings        int
	Mistakes        int
	Warnings        int
	SourcesAnalyzed int
	MessagesRead    int
	CacheHit        bool
	ProviderID      string
	NoMistakes      bool
}

// errProviderNotRegistered surfaces the spec §13 hard-fail when the resolved
// provider id has no factory available.
var errProviderNotRegistered = errors.New("provider not registered")

// resolveExecutionMode picks Sequential vs Parallel from CLI override > config.
// Logs a fallback warning when parallel was requested but the provider lacks support.
func resolveExecutionMode(appConfig *config.Config, opts Options, provider analyzer.Provider, logger *logging.Logger) analyzer.ExecutionMode {
	requested := analyzer.ModeSequential
	if opts.ParallelOverride || strings.EqualFold(appConfig.Analyzer.Execution.Mode, config.ExecutionModeParallel) {
		requested = analyzer.ModeParallel
	}
	if requested == analyzer.ModeParallel && !analyzer.ProviderSupportsParallel(provider) {
		logger.Warn("parallel fallback",
			logging.Any("provider", provider.ID()),
			logging.Any("reason", "provider does not support parallel"),
		)
		return analyzer.ModeSequential
	}
	return requested
}

// resolveMaxConcurrency: CLI override > config; 0 = let pool pick len(chunks).
func resolveMaxConcurrency(appConfig *config.Config, opts Options) int {
	if opts.MaxConcurrencyOverride > 0 {
		return opts.MaxConcurrencyOverride
	}
	return appConfig.Analyzer.Execution.MaxConcurrency
}

// recordProviderSuccess: Runs++, LastSuccessUTC = now, clear LastError.
// Caller persists state.
func recordProviderSuccess(currentState *state.State, providerID string, tokens int64) {
	if currentState == nil || strings.TrimSpace(providerID) == "" {
		return
	}
	if currentState.ProviderUsage == nil {
		currentState.ProviderUsage = map[string]state.ProviderUsage{}
	}
	usage := currentState.ProviderUsage[providerID]
	usage.Runs++
	if tokens > 0 {
		usage.TotalTokens += tokens
	}
	usage.LastSuccessUTC = time.Now().UTC()
	usage.LastError = ""
	currentState.ProviderUsage[providerID] = usage
}

// recordProviderFailure: Timeouts++ on DeadlineExceeded, else Failures++.
// Caller persists state.
func recordProviderFailure(currentState *state.State, providerID string, err error) {
	if currentState == nil || strings.TrimSpace(providerID) == "" || err == nil {
		return
	}
	if currentState.ProviderUsage == nil {
		currentState.ProviderUsage = map[string]state.ProviderUsage{}
	}
	usage := currentState.ProviderUsage[providerID]
	if errors.Is(err, context.DeadlineExceeded) {
		usage.Timeouts++
	} else {
		usage.Failures++
	}
	usage.LastError = state.TruncateError(err.Error())
	currentState.ProviderUsage[providerID] = usage
}

// runCtx bundles the per-invocation context shared by pipeline helpers.
// Created once in Run() after all local variables are resolved.
type runCtx struct {
	outputRoot string
	project    string
	state      *state.State
	packs      []analyzer.RulePack
	providerID string
}

// persistFailureState saves currentState on a failure path; logs but does
// not propagate the save error because the caller is already returning a
// more useful error. Also prunes (B28/B29) so a perma-failing project does
// not accumulate stale entries forever.
func (rc *runCtx) persistFailureState(cause error, logger *logging.Logger) {
	pruneLastRunPerCategory(rc.state.LastRunPerCategory, rc.packs)
	pruneProviderUsage(rc.state.ProviderUsage, rc.providerID)
	if err := state.Save(rc.outputRoot, rc.project, rc.state); err != nil && logger != nil {
		logger.Warn("failure-path state save failed",
			logging.Any("err", err),
			logging.Any("cause", cause),
		)
	}
}

// savePrunedState prunes stale entries (B28/B29) then writes state. Used by
// every successful-save site in pipeline.Run so every save path gets the
// same hygiene, not just the happy path.
func (rc *runCtx) savePrunedState() error {
	pruneLastRunPerCategory(rc.state.LastRunPerCategory, rc.packs)
	pruneProviderUsage(rc.state.ProviderUsage, rc.providerID)
	return state.Save(rc.outputRoot, rc.project, rc.state)
}

// discoveryResult holds the output of the discovery stage.
type discoveryResult struct {
	projectPath  string
	projectName  string
	outputRoot   string
	providerID   string
	providerBlock config.ProviderBlock
	projectFile  *config.ProjectFileConfig
	appConfig    *config.Config
	sources      []chat.ChatSource
}

// runDiscovery resolves paths, loads project config, discovers chat sources,
// and applies lookback filtering.
func runDiscovery(opts Options, appConfig *config.Config, logger *logging.Logger) (discoveryResult, error) {
	var dr discoveryResult
	dr.appConfig = appConfig

	projectPath, err := resolveAbsoluteProjectPath(opts.ProjectPath)
	if err != nil {
		return dr, err
	}
	dr.projectPath = projectPath

	dr.projectName = strings.TrimSpace(opts.ProjectName)
	if dr.projectName == "" {
		dr.projectName = deriveProjectName(projectPath)
	}
	logger.Info("analyze begin", logging.Any("project", dr.projectName), logging.Any("path", projectPath))

	projectFile, err := config.LoadProjectFileConfig(projectPath)
	if err != nil {
		return dr, fmt.Errorf("load project config: %w", err)
	}
	dr.projectFile = projectFile

	dr.providerID, dr.providerBlock = appConfig.ResolveProviderConfig(projectFile, opts.ProviderID)
	logger.Info("provider resolved", logging.Any("id", dr.providerID))

	dr.outputRoot = strings.TrimSpace(opts.OutputDir)
	if dr.outputRoot == "" {
		dr.outputRoot = appConfig.Daemon.OutputRoot
	}

	discoverEnv, err := chat.DefaultDiscoveryEnvironment()
	if err != nil {
		return dr, fmt.Errorf("resolve discovery environment: %w", err)
	}
	if copilotHome := strings.TrimSpace(dr.providerBlock.CopilotHome); copilotHome != "" {
		discoverEnv.CopilotHome = copilotHome
	} else if isCopilotProvider(config.ProviderID(dr.providerID)) && dr.outputRoot != "" {
		discoverEnv.CopilotHome = filepath.Join(dr.outputRoot, ".copilot-state")
	}
	sources, err := chat.DiscoverChatsWithEnvironment(discoverEnv, projectPath)
	if err != nil {
		return dr, fmt.Errorf("discover chats: %w", err)
	}
	logDiscoveredSources(logger, sources)

	if strings.TrimSpace(opts.Since) != "" {
		lookback, enabled, err := parseLookbackWindow(opts.Since)
		if err != nil {
			return dr, fmt.Errorf("parse --since: %w", err)
		}
		before := len(sources)
		sources = filterSourcesByLookback(sources, time.Now().UTC(), lookback, enabled)
		logger.Info("lookback filter", logging.Any("since", opts.Since), logging.Any("kept", len(sources)), logging.Any("total", before))
	}
	dr.sources = sources
	return dr, nil
}

// cachingResult holds the output of the caching stage.
type cachingResult struct {
	cacheKeys map[string]string
	cacheHit  bool
}

// runCaching checks the discovery cache, computes per-source cache keys,
// and determines whether the run can be skipped.
func runCaching(opts Options, dr discoveryResult, currentState *state.State, repoHeadSHA string, logger *logging.Logger) (cachingResult, error) {
	if !opts.Force && opts.DiscoveryCache != nil {
		if opts.DiscoveryCache.Check(dr.projectPath, dr.sources, repoHeadSHA) {
			logger.Info("discovery cache hit", logging.Any("project", dr.projectName), logging.Any("sources", len(dr.sources)))
			publishRunDone(opts.Events, dr.projectName, 0, 0, 0)
			return cachingResult{cacheHit: true}, nil
		}
	}

	cacheKeys, cacheStats := computeCacheKeys(dr.sources, currentState.ChatHashes, repoHeadSHA, logger)
	logger.Info("chat cache summary",
		logging.Any("total", len(dr.sources)),
		logging.Any("cached", cacheStats.Cached),
		logging.Any("changed", cacheStats.Changed),
		logging.Any("new", cacheStats.Fresh),
		logging.Any("hash_failed_kept", cacheStats.HashFailedKept),
		logging.Any("hash_failed_dropped", cacheStats.HashFailedDropped),
		logging.Any("force", opts.Force),
	)

	if !opts.Force && cacheUnchanged(currentState, cacheKeys, repoHeadSHA) {
		logger.Info("cache hit", logging.Any("analyzing", 0), logging.Any("skipping", len(dr.sources)), logging.Any("reason", "all cached, head unchanged"))
		publishRunDone(opts.Events, dr.projectName, 0, 0, 0)
		return cachingResult{cacheHit: true}, nil
	}
	logger.Info("cache miss", logging.Any("analyzing", len(dr.sources)), logging.Any("changed", cacheStats.Changed), logging.Any("new", cacheStats.Fresh), logging.Any("cached", cacheStats.Cached))
	return cachingResult{cacheKeys: cacheKeys}, nil
}

// transcriptResult holds the output of the transcript preparation stage.
type transcriptResult struct {
	blocks          []ProviderBlock
	sourcesUsed     []chat.ChatSource
	messageCount    int
	warnings        []string
	chunks          []analyzer.Chunk
	redactionTotal  int
	transcriptBytes int
	rulePacks       []analyzer.RulePack
}

// runTranscriptPrep loads rule packs, builds redacted transcripts, and packs
// chunks. Returns zeroMessages=true when the preflight check finds no readable
// messages (caller should save state and return early).
func runTranscriptPrep(opts Options, dr discoveryResult, sources []chat.ChatSource, logger *logging.Logger) (transcriptResult, bool, error) {
	var tr transcriptResult

	rulePacks := mergeRulePacks(dr.appConfig, dr.projectFile)
	if !anyEnabled(rulePacks) {
		return tr, false, fmt.Errorf("all rule packs disabled; nothing to analyze")
	}

	redactor, err := buildRedactor(dr.appConfig, dr.projectFile)
	if err != nil {
		return tr, false, err
	}

	blocks, sourcesUsed, messageCount, warnings, redactionTotal, err := buildProviderBlocks(sources, redactor, logger, dr.appConfig.Analyzer.IncludeSubagentTranscripts)
	if err != nil {
		return tr, false, err
	}
	transcriptBytes := 0
	for _, b := range blocks {
		transcriptBytes += b.Bytes()
	}
	logger.Info("transcript built", logging.Any("sources_used", len(sourcesUsed)), logging.Any("messages", messageCount), logging.Any("transcript_bytes", transcriptBytes), logging.Any("redaction_hits", redactionTotal))

	if messageCount == 0 {
		logger.Info("preflight skip",
			logging.Any("reason", "no readable chat messages"),
			logging.Any("sources_discovered", len(sources)),
		)
		return transcriptResult{
			sourcesUsed:    sourcesUsed,
			messageCount:   0,
			warnings:       append(warnings, "no readable messages in discovered chats"),
			redactionTotal: redactionTotal,
			rulePacks:      rulePacks,
		}, true, nil
	}

	chunkCfg := dr.appConfig.Analyzer.Chunking
	if opts.MaxChunkBytesOverrideSet {
		chunkCfg.MaxChunkBytes = opts.MaxChunkBytesOverride
	}
	chunks, chunkWarnings := PackChunks(blocks, chunkCfg, opts.Since)
	if len(chunks) == 0 {
		return tr, false, fmt.Errorf("chunker produced zero chunks despite non-empty transcript")
	}
	totalSplits := 0
	for _, c := range chunks {
		if c.Split {
			totalSplits++
		}
	}
	logger.Info("chunked transcript",
		logging.Any("chunks", len(chunks)),
		logging.Any("total_bytes", transcriptBytes),
		logging.Any("max_chunk_bytes", chunkCfg.MaxChunkBytes),
		logging.Any("hard_splits", totalSplits),
	)
	warnings = append(warnings, chunkWarnings...)

	return transcriptResult{
		blocks:          blocks,
		sourcesUsed:     sourcesUsed,
		messageCount:    messageCount,
		warnings:        warnings,
		chunks:          chunks,
		redactionTotal:  redactionTotal,
		transcriptBytes: transcriptBytes,
		rulePacks:       rulePacks,
	}, false, nil
}

// analysisResult holds the output of the analysis stage.
type analysisResult struct {
	result   analyzer.AnalysisResult
	provider analyzer.Provider
	mode     analyzer.ExecutionMode
}

// runAnalysis instantiates the provider, detects the toolchain, and runs the
// orchestrator. The caller must close the returned provider.
func runAnalysis(ctx context.Context, opts Options, dr discoveryResult, tr transcriptResult, rctx *runCtx, currentState *state.State, logger *logging.Logger) (analysisResult, error) {
	var ar analysisResult

	providerCfg := buildProviderConfig(dr.providerID, dr.providerBlock)
	provider, err := analyzer.NewProvider(analyzer.ProviderID(dr.providerID), providerCfg)
	if err != nil {
		return ar, fmt.Errorf("instantiate provider %q: %w (%s)", dr.providerID, err, config.RemediationMessage(dr.providerID))
	}

	logger.Info("provider starting", logging.Any("id", dr.providerID))
	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	if err := provider.Start(startCtx); err != nil {
		startCancel()
		_ = provider.Close()
		recordProviderFailure(currentState, dr.providerID, err)
		rctx.persistFailureState(err, logger)
		return ar, fmt.Errorf("start provider %q: %w (%s)", dr.providerID, err, config.RemediationMessage(dr.providerID))
	}
	startCancel()
	logger.Info("provider ready", logging.Any("id", dr.providerID))

	mode := resolveExecutionMode(dr.appConfig, opts, provider, logger)

	tc := toolchain.Detect(dr.projectPath)
	logger.Info("toolchain detected", logging.Any("summary", tc.String()))

	codebaseContext, err := analyzer.BuildCodebaseContext(dr.projectPath, tc)
	if err != nil {
		logger.Warn("codebase context build failed", logging.Any("err", err))
	}
	logger.Info("codebase context", logging.Any("bytes", len(codebaseContext)))

	sessionFactory := func() (analyzer.Session, error) {
		raw, ferr := provider.NewSession(ctx, analyzer.SessionConfig{
			WorkingDirectory: dr.projectPath,
			Model:            dr.providerBlock.Model,
			ReadOnly:         true,
			SystemMessage:    analyzer.BuildReadOnlySystemMessage(dr.projectPath),
		})
		if ferr != nil {
			return nil, ferr
		}
		return analyzer.NewLoggingSession(raw, logger, dr.providerID), nil
	}

	existingFindingHashes := stringSliceToSet(currentState.FindingHashes)
	if currentState != nil {
		for hash, fs := range currentState.Findings {
			if fs.Status == state.FindingStatusDismissed {
				if existingFindingHashes == nil {
					existingFindingHashes = map[string]struct{}{}
				}
				existingFindingHashes[hash] = struct{}{}
			}
		}
	}
	phaseReq := analyzer.PhaseRequest{
		ProjectRoot:       dr.projectPath,
		ToolchainSummary:  tc.Summary(),
		PrimaryLinter:     tc.PrimaryLinter(),
		TestFramework:     tc.PrimaryTestFramework(),
		CodebaseContext:   codebaseContext,
		DryRun:            opts.DryRun,
		StrictLintRules:   !opts.Permissive,
		LintRuleValidator: lintrules.NewValidator(),
		ExistingHashes:    existingFindingHashes,
	}

	logEnabledRulePacks(logger, rctx.packs)
	orchestrator := analyzer.NewOrchestrator(rctx.packs)
	rc := analyzer.RunConfig{
		SessionFactory: sessionFactory,
		Mode:           mode,
		MaxConcurrency: resolveMaxConcurrency(dr.appConfig, opts),
	}
	in := analyzer.ChunkInputs{
		Chunks:          tr.chunks,
		RuleTimeoutSecs: dr.appConfig.Analyzer.RuleTimeoutSeconds,
	}
	logger.Info("phase dispatch", logging.Any("mode", mode.String()), logging.Any("chunks", len(tr.chunks)), logging.Any("concurrency", rc.MaxConcurrency))
	result, err := orchestrator.RunChunks(ctx, rc, in, phaseReq)
	if err != nil {
		_ = provider.Close()
		recordProviderFailure(currentState, dr.providerID, err)
		rctx.persistFailureState(err, logger)
		return ar, fmt.Errorf("run analyzer: %w", err)
	}
	logger.Info("orchestrator done", logging.Any("mistakes", len(result.Mistakes)), logging.Any("findings", len(result.Findings)), logging.Any("warnings", len(result.Warnings)))
	for _, w := range result.Warnings {
		logger.Warn("orchestrator warning", logging.Any("warning", w))
	}

	return analysisResult{result: result, provider: provider, mode: mode}, nil
}

// runOutputAndPersist generates todos, updates state, saves, updates the
// discovery cache, and records history.
func runOutputAndPersist(opts Options, dr discoveryResult, ar analysisResult, tr transcriptResult, rctx *runCtx, cacheKeys map[string]string, repoHeadSHA string, runStart time.Time, logger *logging.Logger) (Result, error) {
	currentState := rctx.state
	warnings := tr.warnings
	warnings = append(warnings, ar.result.Warnings...)

	result := Result{
		ProviderID:      dr.providerID,
		Findings:        len(ar.result.Findings),
		Mistakes:        len(ar.result.Mistakes),
		Warnings:        len(warnings),
		SourcesAnalyzed: len(tr.sourcesUsed),
		MessagesRead:    tr.messageCount,
	}

	if opts.DryRun {
		result.TodosPath = todosOutputPath(dr.outputRoot, dr.projectName)
		publishRunDone(opts.Events, dr.projectName, result.Findings, result.SourcesAnalyzed, result.MessagesRead)
		return result, nil
	}

	if len(ar.result.Mistakes) == 0 {
		warnings = append(warnings, "no recurring mistakes found")
		result.NoMistakes = true
	}

	generateResult, err := output.GenerateTodos(dr.projectName, ar.result.Findings, output.GenerateOptions{
		OutputRoot:   dr.outputRoot,
		ProjectTitle: dr.projectName,
		Warnings:     warnings,
	})
	if err != nil {
		return Result{}, fmt.Errorf("generate todos: %w", err)
	}
	result.TodosPath = generateResult.Path
	result.Findings = generateResult.AddedFindings

	now := time.Now().UTC()
	currentState.LastRunUTC = now
	currentState.RepoHeadSHA = repoHeadSHA
	currentState.ChatHashes = cacheKeys
	currentState.FindingHashes = mergeHashLists(currentState.FindingHashes, collectFindingHashes(ar.result.Findings))
	recordFindingApplySpecs(currentState, ar.result.Findings, dr.projectName)
	if currentState.LastRunPerCategory == nil {
		currentState.LastRunPerCategory = map[string]time.Time{}
	}
	for _, cat := range ar.result.CompletedCategories {
		currentState.LastRunPerCategory[cat] = now
	}
	recordProviderSuccess(currentState, dr.providerID, 0)

	if currentState.UsageStats == nil {
		currentState.UsageStats = map[string]int64{}
	}
	currentState.UsageStats["sources_analyzed"] += int64(len(tr.sourcesUsed))
	currentState.UsageStats["messages_analyzed"] += int64(tr.messageCount)
	currentState.UsageStats["mistakes_found"] += int64(len(ar.result.Mistakes))
	currentState.UsageStats["findings_added"] += int64(generateResult.AddedFindings)
	currentState.UsageStats["redaction_hits"] += int64(tr.redactionTotal)

	if err := rctx.savePrunedState(); err != nil {
		return Result{}, fmt.Errorf("save state: %w", err)
	}

	if opts.DiscoveryCache != nil {
		opts.DiscoveryCache.Update(dr.projectPath, dr.sources, repoHeadSHA)
	}

	perCategory := map[string]int{}
	for _, f := range ar.result.Findings {
		perCategory[string(f.Category)]++
	}
	today := time.Now().UTC().Format("2006-01-02")
	if err := state.UpdateHistoryToday(dr.outputRoot, dr.projectName, today, state.DaySummaryDelta{
		Runs:          1,
		FindingsNew:   result.Findings,
		FindingsTotal: len(currentState.FindingHashes),
		Tokens:        0,
		RunMillis:     time.Since(runStart).Milliseconds(),
		PerCategory:   perCategory,
	}); err != nil {
		logger.Warn("history update failed", logging.Any("err", err))
	}

	publishRunDone(opts.Events, dr.projectName, result.Findings, result.SourcesAnalyzed, result.MessagesRead)
	return result, nil
}

// Run executes the end-to-end analyze pipeline against a single project path,
// following the sequence in spec §17.
func Run(ctx context.Context, opts Options, logger *logging.Logger) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.LiveConfig != nil {
		if snap := opts.LiveConfig.Load(); snap != nil {
			opts.Config = snap
		}
	}
	appConfig := opts.Config
	if appConfig == nil {
		return Result{}, fmt.Errorf("pipeline.Run: config is required")
	}

	runStart := time.Now()
	if opts.Events != nil {
		opts.Events.Publish(Event{Type: EventRunStart, Payload: map[string]any{"project": opts.ProjectName}})
	}

	// Stage 1: Discovery — paths, project config, chat sources.
	dr, err := runDiscovery(opts, appConfig, logger)
	if err != nil {
		return Result{}, err
	}

	// Load state and repo HEAD for caching decisions.
	loadResult, err := state.LoadWithResult(dr.outputRoot, dr.projectName)
	if err != nil {
		return Result{}, fmt.Errorf("load state: %w", err)
	}
	currentState := loadResult.State
	if loadResult.Migrated {
		logger.Warn("state migrated",
			logging.Any("from_version", loadResult.PriorVersion),
			logging.Any("to_version", loadResult.CurrentVersion),
			logging.Any("note", "v1 ChatHashes invalidated by length-prefix change (B21); prior file backed up as state.json.v<old>.bak; this run will re-analyze all chats"),
		)
	}
	repoHeadSHA := state.RepoHeadSHA(dr.projectPath, logger)
	logger.Info("repo state", logging.Any("head_sha", repoHeadSHA), logging.Any("prior_run", currentState.LastRunUTC.Format(time.RFC3339)), logging.Any("prior_chats", len(currentState.ChatHashes)))

	// Stage 2: Caching — skip if nothing changed.
	cr, err := runCaching(opts, dr, currentState, repoHeadSHA, logger)
	if err != nil {
		return Result{}, err
	}
	if cr.cacheHit {
		return Result{
			ProviderID: dr.providerID,
			CacheHit:   true,
			TodosPath:  todosOutputPath(dr.outputRoot, dr.projectName),
		}, nil
	}

	// Stage 3: Transcript preparation — rule packs, redaction, chunking.
	tr, zeroMessages, err := runTranscriptPrep(opts, dr, dr.sources, logger)
	if err != nil {
		return Result{}, err
	}

	rctx := &runCtx{
		outputRoot: dr.outputRoot,
		project:    dr.projectName,
		state:      currentState,
		packs:      tr.rulePacks,
		providerID: dr.providerID,
	}

	if zeroMessages {
		currentState.LastRunUTC = time.Now().UTC()
		currentState.RepoHeadSHA = repoHeadSHA
		currentState.ChatHashes = cr.cacheKeys
		if saveErr := rctx.savePrunedState(); saveErr != nil {
			logger.Warn("preflight state save failed", logging.Any("err", saveErr))
		}
		publishRunDone(opts.Events, dr.projectName, 0, 0, 0)
		return Result{
			ProviderID:      dr.providerID,
			SourcesAnalyzed: 0,
			MessagesRead:    0,
			Warnings:        len(tr.warnings),
			TodosPath:       todosOutputPath(dr.outputRoot, dr.projectName),
			NoMistakes:      true,
		}, nil
	}

	// Stage 4: Analysis — provider, toolchain, orchestrator.
	ar, err := runAnalysis(ctx, opts, dr, tr, rctx, currentState, logger)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = ar.provider.Close() }()

	// Stage 5: Output and persistence.
	return runOutputAndPersist(opts, dr, ar, tr, rctx, cr.cacheKeys, repoHeadSHA, runStart, logger)
}

// publishRunDone is a nil-safe helper for the run.done event payload.
func publishRunDone(bus *EventBus, project string, findingsNew, sources, messages int) {
	if bus == nil {
		return
	}
	bus.Publish(Event{Type: EventRunDone, Payload: map[string]any{
		"project":      project,
		"findings_new": findingsNew,
		"sources":      sources,
		"messages":     messages,
	}})
}

// PublishRunError is a nil-safe helper for the run.error event payload.
func PublishRunError(bus *EventBus, project string, err error) {
	if bus == nil {
		return
	}
	bus.Publish(Event{Type: EventRunError, Payload: map[string]any{
		"project": project,
		"error":   err.Error(),
	}})
}

// isCopilotProvider reports whether the provider ID is a Copilot variant
// (SDK or ACP) that writes session files to the Copilot home directory.
func isCopilotProvider(id config.ProviderID) bool {
	return id == config.ProviderCopilotSDK || id == config.ProviderCopilotACP
}
