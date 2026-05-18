package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
func resolveExecutionMode(cfg *config.Config, opts Options, provider analyzer.Provider, logger *logging.Logger) analyzer.ExecutionMode {
	requested := analyzer.ModeSequential
	if opts.ParallelOverride || strings.EqualFold(cfg.Analyzer.Execution.Mode, config.ExecutionModeParallel) {
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
func resolveMaxConcurrency(cfg *config.Config, opts Options) int {
	if opts.MaxConcurrencyOverride > 0 {
		return opts.MaxConcurrencyOverride
	}
	return cfg.Analyzer.Execution.MaxConcurrency
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

// persistFailureState saves currentState on a failure path; logs but does
// not propagate the save error because the caller is already returning a
// more useful error. Also prunes (B28/B29) so a perma-failing project does
// not accumulate stale entries forever.
func persistFailureState(currentState *state.State, outputRoot, projectName string, packs []analyzer.RulePack, activeProviderID string, cause error, logger *logging.Logger) {
	pruneLastRunPerCategory(currentState.LastRunPerCategory, packs)
	pruneProviderUsage(currentState.ProviderUsage, activeProviderID)
	if err := state.Save(outputRoot, projectName, currentState); err != nil && logger != nil {
		logger.Warn("failure-path state save failed",
			logging.Any("err", err),
			logging.Any("cause", cause),
		)
	}
}

// savePrunedState prunes stale entries (B28/B29) then writes state. Used by
// every successful-save site in pipeline.Run so every save path gets the
// same hygiene, not just the happy path.
func savePrunedState(outputRoot, projectName string, currentState *state.State, packs []analyzer.RulePack, activeProviderID string) error {
	pruneLastRunPerCategory(currentState.LastRunPerCategory, packs)
	pruneProviderUsage(currentState.ProviderUsage, activeProviderID)
	return state.Save(outputRoot, projectName, currentState)
}

// Run executes the end-to-end analyze pipeline against a single project path,
// following the sequence in spec §17.
func Run(ctx context.Context, opts Options, logger *logging.Logger) (Result, error) {
	// Top-level CLI/daemon entry: a nil context here means the caller did
	// not wire signal cancellation, which is a one-shot run from a script.
	// Substitute Background defensively. Provider-layer Run methods, by
	// contrast, treat nil as a programmer error (analyzer.ErrNilContext).
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := opts.Config
	if cfg == nil {
		return Result{}, fmt.Errorf("pipeline.Run: config is required")
	}

	projectPath, err := resolveAbsoluteProjectPath(opts.ProjectPath)
	if err != nil {
		return Result{}, err
	}
	projectName := strings.TrimSpace(opts.ProjectName)
	if projectName == "" {
		projectName = deriveProjectName(projectPath)
	}
	logger.Info("analyze begin", logging.Any("project", projectName), logging.Any("path", projectPath))

	projectFile, err := config.LoadProjectFileConfig(projectPath)
	if err != nil {
		return Result{}, fmt.Errorf("load project config: %w", err)
	}

	providerID, providerBlock := cfg.ResolveProviderConfig(projectFile, opts.ProviderID)
	logger.Info("provider resolved", logging.Any("id", providerID))

	outputRoot := strings.TrimSpace(opts.OutputDir)
	if outputRoot == "" {
		outputRoot = cfg.Daemon.OutputRoot
	}

	sources, err := chat.DiscoverChats(projectPath)
	if err != nil {
		return Result{}, fmt.Errorf("discover chats: %w", err)
	}
	logDiscoveredSources(logger, sources)

	if strings.TrimSpace(opts.Since) != "" {
		lookback, enabled, err := parseLookbackWindow(opts.Since)
		if err != nil {
			return Result{}, fmt.Errorf("parse --since: %w", err)
		}
		before := len(sources)
		sources = filterSourcesByLookback(sources, time.Now().UTC(), lookback, enabled)
		logger.Info("lookback filter", logging.Any("since", opts.Since), logging.Any("kept", len(sources)), logging.Any("total", before))
	}

	loadResult, err := state.LoadWithResult(outputRoot, projectName)
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
	repoHeadSHA := state.RepoHeadSHA(projectPath, logger)
	logger.Info("repo state", logging.Any("head_sha", repoHeadSHA), logging.Any("prior_run", currentState.LastRunUTC.Format(time.RFC3339)), logging.Any("prior_chats", len(currentState.ChatHashes)))

	// Fast path: skip the expensive HashFile loop if the daemon's discovery
	// cache confirms nothing changed since the last successful run.
	if !opts.Force && opts.DiscoveryCache != nil {
		if opts.DiscoveryCache.Check(projectPath, sources, repoHeadSHA) {
			logger.Info("discovery cache hit", logging.Any("project", projectName), logging.Any("sources", len(sources)))
			return Result{
				ProviderID: providerID,
				CacheHit:   true,
				TodosPath:  todosOutputPath(outputRoot, projectName),
			}, nil
		}
	}

	cacheKeys, cacheStats := computeCacheKeys(sources, currentState.ChatHashes, repoHeadSHA, logger)
	logger.Info("chat cache summary",
		logging.Any("total", len(sources)),
		logging.Any("cached", cacheStats.Cached),
		logging.Any("changed", cacheStats.Changed),
		logging.Any("new", cacheStats.Fresh),
		logging.Any("hash_failed_kept", cacheStats.HashFailedKept),
		logging.Any("hash_failed_dropped", cacheStats.HashFailedDropped),
		logging.Any("force", opts.Force),
	)

	if !opts.Force && cacheUnchanged(currentState, cacheKeys, repoHeadSHA) {
		logger.Info("cache hit", logging.Any("analyzing", 0), logging.Any("skipping", len(sources)), logging.Any("reason", "all cached, head unchanged"))
		return Result{
			ProviderID: providerID,
			CacheHit:   true,
			TodosPath:  todosOutputPath(outputRoot, projectName),
		}, nil
	}
	logger.Info("cache miss", logging.Any("analyzing", len(sources)), logging.Any("changed", cacheStats.Changed), logging.Any("new", cacheStats.Fresh), logging.Any("cached", cacheStats.Cached))

	rulePacks := mergeRulePacks(cfg, projectFile)
	if !anyEnabled(rulePacks) {
		return Result{}, fmt.Errorf("all rule packs disabled; nothing to analyze")
	}

	redactor, err := buildRedactor(cfg, projectFile)
	if err != nil {
		return Result{}, err
	}

	blocks, sourcesUsed, messageCount, warnings, redactionTotal, err := buildProviderBlocks(sources, redactor, logger)
	if err != nil {
		return Result{}, err
	}
	transcriptBytes := 0
	for _, b := range blocks {
		transcriptBytes += b.Bytes()
	}
	logger.Info("transcript built", logging.Any("sources_used", len(sourcesUsed)), logging.Any("messages", messageCount), logging.Any("transcript_bytes", transcriptBytes), logging.Any("redaction_hits", redactionTotal))

	// Preflight skip: zero readable messages -> save empty state, no provider call.
	if messageCount == 0 {
		logger.Info("preflight skip",
			logging.Any("reason", "no readable chat messages"),
			logging.Any("sources_discovered", len(sources)),
		)
		warnings = append(warnings, "no readable messages in discovered chats")

		currentState.LastRunUTC = time.Now().UTC()
		currentState.RepoHeadSHA = repoHeadSHA
		currentState.ChatHashes = cacheKeys
		if err := savePrunedState(outputRoot, projectName, currentState, rulePacks, providerID); err != nil {
			logger.Warn("preflight state save failed", logging.Any("err", err))
		}

		return Result{
			ProviderID:      providerID,
			SourcesAnalyzed: 0,
			MessagesRead:    0,
			Warnings:        len(warnings),
			TodosPath:       todosOutputPath(outputRoot, projectName),
			NoMistakes:      true,
		}, nil
	}

	tc := toolchain.Detect(projectPath)
	logger.Info("toolchain detected", logging.Any("summary", tc.String()))

	codebaseContext, err := analyzer.BuildCodebaseContext(projectPath, tc)
	if err != nil {
		logger.Warn("codebase context build failed", logging.Any("err", err))
	}
	logger.Info("codebase context", logging.Any("bytes", len(codebaseContext)))

	codebaseFiles, err := analyzer.CodebaseFiles(projectPath)
	if err != nil {
		logger.Warn("codebase files detection failed", logging.Any("err", err))
	}

	chunkCfg := cfg.Analyzer.Chunking
	if opts.MaxChunkBytesOverrideSet {
		chunkCfg.MaxChunkBytes = opts.MaxChunkBytesOverride
	}
	chunks, chunkWarnings := PackChunks(blocks, chunkCfg, opts.Since)
	if len(chunks) == 0 {
		return Result{}, fmt.Errorf("chunker produced zero chunks despite non-empty transcript")
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

	providerCfg := buildProviderConfig(providerID, providerBlock)
	provider, err := analyzer.NewProvider(analyzer.ProviderID(providerID), providerCfg)
	if err != nil {
		return Result{}, fmt.Errorf("instantiate provider %q: %w (%s)", providerID, err, config.RemediationMessage(providerID))
	}
	defer func() { _ = provider.Close() }()

	logger.Info("provider starting", logging.Any("id", providerID))
	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	if err := provider.Start(startCtx); err != nil {
		startCancel()
		recordProviderFailure(currentState, providerID, err)
		persistFailureState(currentState, outputRoot, projectName, rulePacks, providerID, err, logger)
		return Result{}, fmt.Errorf("start provider %q: %w (%s)", providerID, err, config.RemediationMessage(providerID))
	}
	startCancel()
	logger.Info("provider ready", logging.Any("id", providerID))

	mode := resolveExecutionMode(cfg, opts, provider, logger)

	sessionFactory := func() (analyzer.Session, error) {
		raw, ferr := provider.NewSession(ctx, analyzer.SessionConfig{
			WorkingDirectory: projectPath,
			Model:            providerBlock.Model,
			ReadOnly:         true,
			SystemMessage:    analyzer.BuildReadOnlySystemMessage(projectPath),
		})
		if ferr != nil {
			return nil, ferr
		}
		return analyzer.NewLoggingSession(raw, logger, providerID), nil
	}

	existingFindingHashes := stringSliceToSet(currentState.FindingHashes)
	phaseReq := analyzer.PhaseRequest{
		ProjectRoot:       projectPath,
		ToolchainSummary:  tc.Summary(),
		PrimaryLinter:     tc.PrimaryLinter(),
		TestFramework:     tc.PrimaryTestFramework(),
		CodebaseContext:   codebaseContext,
		DryRun:            opts.DryRun,
		StrictLintRules:   !opts.Permissive,
		LintRuleValidator: lintrules.NewValidator(),
		ExistingHashes:    existingFindingHashes,
	}

	logEnabledRulePacks(logger, rulePacks)
	orchestrator := analyzer.NewOrchestrator(rulePacks)
	rc := analyzer.RunConfig{
		SessionFactory: sessionFactory,
		Mode:           mode,
		MaxConcurrency: resolveMaxConcurrency(cfg, opts),
	}
	in := analyzer.ChunkInputs{
		Chunks:          chunks,
		CodebaseFiles:   codebaseFiles,
		RuleTimeoutSecs: cfg.Analyzer.RuleTimeoutSeconds,
	}
	logger.Info("phase dispatch", logging.Any("mode", mode.String()), logging.Any("chunks", len(chunks)), logging.Any("concurrency", rc.MaxConcurrency))
	analysisResult, err := orchestrator.RunChunks(ctx, rc, in, phaseReq)
	if err != nil {
		recordProviderFailure(currentState, providerID, err)
		persistFailureState(currentState, outputRoot, projectName, rulePacks, providerID, err, logger)
		return Result{}, fmt.Errorf("run analyzer: %w", err)
	}
	logger.Info("orchestrator done", logging.Any("mistakes", len(analysisResult.Mistakes)), logging.Any("findings", len(analysisResult.Findings)), logging.Any("warnings", len(analysisResult.Warnings)))
	for _, w := range analysisResult.Warnings {
		logger.Warn("orchestrator warning", logging.Any("warning", w))
	}
	warnings = append(warnings, analysisResult.Warnings...)

	result := Result{
		ProviderID:      providerID,
		Findings:        len(analysisResult.Findings),
		Mistakes:        len(analysisResult.Mistakes),
		Warnings:        len(warnings),
		SourcesAnalyzed: len(sourcesUsed),
		MessagesRead:    messageCount,
	}

	if opts.DryRun {
		result.TodosPath = todosOutputPath(outputRoot, projectName)
		return result, nil
	}

	if len(analysisResult.Mistakes) == 0 {
		warnings = append(warnings, "no recurring mistakes found")
		result.NoMistakes = true
	}

	generateResult, err := output.GenerateTodos(projectName, analysisResult.Findings, output.GenerateOptions{
		OutputRoot:   outputRoot,
		ProjectTitle: projectName,
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
	currentState.FindingHashes = mergeHashLists(currentState.FindingHashes, collectFindingHashes(analysisResult.Findings))
	if currentState.LastRunPerCategory == nil {
		currentState.LastRunPerCategory = map[string]time.Time{}
	}
	for _, cat := range analysisResult.CompletedCategories {
		currentState.LastRunPerCategory[cat] = now
	}
	recordProviderSuccess(currentState, providerID, 0)

	if currentState.UsageStats == nil {
		currentState.UsageStats = map[string]int64{}
	}
	currentState.UsageStats["sources_analyzed"] += int64(len(sourcesUsed))
	currentState.UsageStats["messages_analyzed"] += int64(messageCount)
	currentState.UsageStats["mistakes_found"] += int64(len(analysisResult.Mistakes))
	currentState.UsageStats["findings_added"] += int64(generateResult.AddedFindings)
	currentState.UsageStats["redaction_hits"] += int64(redactionTotal)

	if err := savePrunedState(outputRoot, projectName, currentState, rulePacks, providerID); err != nil {
		return Result{}, fmt.Errorf("save state: %w", err)
	}

	if opts.DiscoveryCache != nil {
		opts.DiscoveryCache.Update(projectPath, sources, repoHeadSHA)
	}

	return result, nil
}
