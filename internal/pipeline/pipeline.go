package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
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
	"dreamer/internal/mcpserver"
	"dreamer/internal/output"
	"dreamer/internal/state"
)

// FindingsTempFilePattern is the glob pattern used for Phase 2 findings
// temp files. Used both at creation time and by the daemon's stale-file
// sweep so the pattern is defined in one place.
const FindingsTempFilePattern = "dreamer-findings-*.jsonl"

// runIDLength is the number of hex characters in a run ID.
// 8 hex chars = 32 bits of entropy — enough for collision avoidance
// within a single daemon lifetime, short enough to not bloat prompts.
const runIDLength = 8

// providerStartTimeout is the maximum time to wait for a provider process
// to start before giving up.
const providerStartTimeout = 30 * time.Second

// generateRunID returns a random 8-character hex string for correlating
// all prompts and outputs from a single analysis run.
func generateRunID() string {
	randomBytes := make([]byte, runIDLength/2)
	_, _ = rand.Read(randomBytes)
	return hex.EncodeToString(randomBytes)
}

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
	projectPath   string
	projectName   string
	outputRoot    string
	providerID    string
	providerBlock config.ProviderBlock
	projectFile   *config.ProjectFileConfig
	appConfig     *config.Config
	sources       []chat.ChatSource
}

// runDiscovery resolves paths, loads project config, discovers chat sources,
// and applies lookback filtering.
func runDiscovery(opts Options, appConfig *config.Config, logger *logging.Logger) (discoveryResult, error) {
	var discovery discoveryResult
	discovery.appConfig = appConfig

	projectPath, err := resolveAbsoluteProjectPath(opts.ProjectPath)
	if err != nil {
		return discovery, err
	}
	discovery.projectPath = projectPath

	discovery.projectName = strings.TrimSpace(opts.ProjectName)
	if discovery.projectName == "" {
		// Build collision map from configured projects so two projects
		// with the same basename get distinct directory names.
		usedNames := make(map[string]string, len(appConfig.Projects)*2)
		for _, p := range appConfig.Projects {
			if p.Name != "" && p.Path != "" {
				// Seed with what DeriveProjectName queries: "project-" + safe(base(path)).
				safe := projectNameSafePattern.ReplaceAllString(filepath.Base(filepath.Clean(p.Path)), "_")
				usedNames["project-"+safe] = p.Path
				// Also seed with the bare on-disk directory name so
				// DeriveProjectName cannot generate a name that
				// collides with an existing project's output dir.
				usedNames[p.Name] = p.Path
			}
		}
		discovery.projectName = DeriveProjectName(projectPath, usedNames)
	}
	logger.Info("analyze begin", logging.Any("project", discovery.projectName), logging.Any("path", projectPath))

	projectFile, err := config.LoadProjectFileConfig(projectPath)
	if err != nil {
		return discovery, fmt.Errorf("load project config: %w", err)
	}
	discovery.projectFile = projectFile

	discovery.providerID, discovery.providerBlock = appConfig.ResolveProviderConfig(projectFile, opts.ProviderID)
	logger.Info("provider resolved", logging.Any("id", discovery.providerID))

	discovery.outputRoot = strings.TrimSpace(opts.OutputDir)
	if discovery.outputRoot == "" {
		discovery.outputRoot = appConfig.Daemon.OutputRoot
	}

	discoverEnv, err := chat.DefaultDiscoveryEnvironment()
	if err != nil {
		return discovery, fmt.Errorf("resolve discovery environment: %w", err)
	}
	if copilotHome := strings.TrimSpace(discovery.providerBlock.CopilotHome); copilotHome != "" {
		discoverEnv.CopilotHome = copilotHome
	} else if isCopilotProvider(config.ProviderID(discovery.providerID)) && discovery.outputRoot != "" {
		discoverEnv.CopilotHome = filepath.Join(discovery.outputRoot, ".copilot-state")
	}
	sources, err := chat.DiscoverChatsWithEnvironment(discoverEnv, projectPath)
	if err != nil {
		return discovery, fmt.Errorf("discover chats: %w", err)
	}
	logDiscoveredSources(logger, sources)

	if strings.TrimSpace(opts.Since) != "" {
		lookback, enabled, err := parseLookbackWindow(opts.Since)
		if err != nil {
			return discovery, fmt.Errorf("parse --since: %w", err)
		}
		before := len(sources)
		sources = filterSourcesByLookback(sources, time.Now().UTC(), lookback, enabled)
		logger.Info("lookback filter", logging.Any("since", opts.Since), logging.Any("kept", len(sources)), logging.Any("total", before))
	}
	discovery.sources = sources
	return discovery, nil
}

// cachingResult holds the output of the caching stage.
type cachingResult struct {
	cacheKeys map[string]string
	cacheHit  bool
}

// runCaching checks the discovery cache, computes per-source cache keys,
// and determines whether the run can be skipped.
func runCaching(opts Options, discovery discoveryResult, currentState *state.State, repoHeadSHA string, logger *logging.Logger) (cachingResult, error) {
	if !opts.Force && opts.DiscoveryCache != nil {
		if opts.DiscoveryCache.Check(discovery.projectPath, discovery.sources, repoHeadSHA) {
			logger.Info("discovery cache hit", logging.Any("project", discovery.projectName), logging.Any("sources", len(discovery.sources)))
			publishRunDone(opts.Events, discovery.projectName, "", 0, 0, 0)
			return cachingResult{cacheHit: true}, nil
		}
	}

	cacheKeys, cacheStats := computeCacheKeys(discovery.sources, currentState.ChatHashes, repoHeadSHA, logger)
	logger.Info("chat cache summary",
		logging.Any("total", len(discovery.sources)),
		logging.Any("cached", cacheStats.Cached),
		logging.Any("changed", cacheStats.Changed),
		logging.Any("new", cacheStats.Fresh),
		logging.Any("hash_failed_kept", cacheStats.HashFailedKept),
		logging.Any("hash_failed_dropped", cacheStats.HashFailedDropped),
		logging.Any("force", opts.Force),
	)

	if !opts.Force && cacheUnchanged(currentState, cacheKeys, repoHeadSHA) {
		logger.Info("cache hit", logging.Any("analyzing", 0), logging.Any("skipping", len(discovery.sources)), logging.Any("reason", "all cached, head unchanged"))
		publishRunDone(opts.Events, discovery.projectName, "", 0, 0, 0)
		return cachingResult{cacheHit: true}, nil
	}
	logger.Info("cache miss", logging.Any("analyzing", len(discovery.sources)), logging.Any("changed", cacheStats.Changed), logging.Any("new", cacheStats.Fresh), logging.Any("cached", cacheStats.Cached))
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
	// redactor is reused on the outbound side — applied to every Phase 2
	// Finding's text fields so a model that echoed a transcript secret
	// back into a finding cannot persist it to todos.md.
	redactor *analyzer.Redactor
}

// runTranscriptPrep loads rule packs, builds redacted transcripts, and packs
// chunks. Returns zeroMessages=true when the preflight check finds no readable
// messages (caller should save state and return early).
func runTranscriptPrep(opts Options, discovery discoveryResult, sources []chat.ChatSource, logger *logging.Logger) (transcriptResult, bool, error) {
	var transcript transcriptResult

	rulePacks := mergeRulePacks(discovery.appConfig, discovery.projectFile)
	if !anyEnabled(rulePacks) {
		return transcript, false, fmt.Errorf("all rule packs disabled; nothing to analyze")
	}

	redactor, err := buildRedactor(discovery.appConfig, discovery.projectFile)
	if err != nil {
		return transcript, false, err
	}

	includeSubagents := discovery.appConfig.Analyzer.IncludeSubagentTranscripts != nil && *discovery.appConfig.Analyzer.IncludeSubagentTranscripts
	blocks, sourcesUsed, messageCount, warnings, redactionTotal, err := buildProviderBlocks(sources, redactor, logger, includeSubagents)
	if err != nil {
		return transcript, false, err
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

	chunkCfg := discovery.appConfig.Analyzer.Chunking
	if opts.MaxChunkBytesOverrideSet {
		chunkCfg.MaxChunkBytes = opts.MaxChunkBytesOverride
	}
	chunks, chunkWarnings := PackChunks(blocks, chunkCfg, opts.Since)
	if len(chunks) == 0 {
		return transcript, false, fmt.Errorf("chunker produced zero chunks despite non-empty transcript")
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
		redactor:        redactor,
	}, false, nil
}

// analysisResult bundles the orchestrator's outputs.
type analysisResult struct {
	result   analyzer.AnalysisResult
	provider analyzer.Provider
	mode     analyzer.ExecutionMode
}

// phase2Setup bundles the resolved Phase 2 transport state and its cleanup.
type phase2Setup struct {
	mode             analyzer.Phase2Mode
	config           *analyzer.Phase2Config
	findingsFilePath string
	binaryPath       string
}

// setupPhase2Transport resolves the Phase 2 transport mode, builds the
// config, and returns a cleanup function that removes any temp files.
// The caller must defer the returned cleanup.
func setupPhase2Transport(ctx context.Context, opts Options, providerID string, events *EventBus, logger *logging.Logger) (phase2Setup, func()) {
	mode := analyzer.LookupPhase2Mode(analyzer.ProviderID(providerID))
	if opts.DryRun {
		mode = analyzer.Phase2ModeNone
	}

	cfg, findingsPath, binaryPath, buildErr := buildPhase2Config(mode)
	if buildErr != nil {
		logger.Warn("phase 2 tool wiring failed — falling back to inline JSON",
			logging.Any("mode", string(mode)),
			logging.Any("err", buildErr),
		)
		if events != nil {
			events.Publish(Event{
				Type:    "phase2.fallback",
				Payload: map[string]any{"mode": string(mode), "error": buildErr.Error()},
			})
		}
		mode = analyzer.Phase2ModeNone
		cfg = nil
	}
	if mode != analyzer.Phase2ModeNone {
		logger.Info("phase2 tool mode",
			logging.Any("mode", string(mode)),
			logging.Any("output", findingsPath),
		)
	}

	cleanup := func() {
		if findingsPath != "" {
			if err := os.Remove(findingsPath); err != nil && !os.IsNotExist(err) {
				logger.Warn("remove findings temp file failed", logging.Any("err", err))
			}
		}
		if cfg != nil && cfg.MCP != nil && cfg.MCP.ConfigFilePath != "" {
			if err := os.Remove(cfg.MCP.ConfigFilePath); err != nil && !os.IsNotExist(err) {
				logger.Warn("remove mcp config temp file failed", logging.Any("err", err))
			}
		}
	}

	return phase2Setup{
		mode:             mode,
		config:           cfg,
		findingsFilePath: findingsPath,
		binaryPath:       binaryPath,
	}, cleanup
}

// buildSessionFactories creates the Phase 1 and Phase 2 session factory
// functions. Phase 1 sessions never see MCP/CLI tool wiring so a rogue
// Phase 1 model cannot pollute the findings file.
func buildSessionFactories(ctx context.Context, provider analyzer.Provider, discovery discoveryResult, phase2Cfg *analyzer.Phase2Config, sandboxMode string, runID string, logger *logging.Logger) (phase1, phase2 func() (analyzer.Session, error)) {
	systemMsg := analyzer.BuildReadOnlySystemMessage(discovery.projectPath, runID)
	phase1 = func() (analyzer.Session, error) {
		raw, err := provider.NewSession(ctx, analyzer.SessionConfig{
			WorkingDirectory: discovery.projectPath,
			Model:            discovery.providerBlock.Model,
			ReadOnly:         true,
			SystemMessage:    systemMsg,
			RunID:            runID,
			Sandbox:          sandboxMode,
		})
		if err != nil {
			return nil, err
		}
		return analyzer.NewLoggingSession(raw, logger, discovery.providerID), nil
	}
	if phase2Cfg == nil {
		return phase1, phase1
	}
	phase2 = func() (analyzer.Session, error) {
		raw, err := provider.NewSession(ctx, analyzer.SessionConfig{
			WorkingDirectory: discovery.projectPath,
			Model:            discovery.providerBlock.Model,
			ReadOnly:         true,
			SystemMessage:    systemMsg,
			RunID:            runID,
			Phase2:           phase2Cfg,
			Sandbox:          sandboxMode,
		})
		if err != nil {
			return nil, err
		}
		return analyzer.NewLoggingSession(raw, logger, discovery.providerID), nil
	}
	return phase1, phase2
}

// collectDismissedHashes builds a set of finding hashes that includes both
// persisted hashes and any that have been dismissed via the UI. Dismissed
// hashes are folded into the dedup set so the analyzer never re-emits them.
func collectDismissedHashes(currentState *state.State) map[string]struct{} {
	if currentState == nil {
		return nil
	}
	hashes := stringSliceToSet(currentState.FindingHashes)
	for hash, findingState := range currentState.Findings {
		if findingState.Status == state.FindingStatusDismissed {
			if hashes == nil {
				hashes = map[string]struct{}{}
			}
			hashes[hash] = struct{}{}
		}
	}
	return hashes
}

// runAnalysis instantiates the provider, detects the toolchain, and runs the
// orchestrator. The caller must close the returned provider.
func runAnalysis(ctx context.Context, opts Options, discovery discoveryResult, transcript transcriptResult, runContext *runCtx, currentState *state.State, runID string, logger *logging.Logger) (analysisResult, error) {
	var analysis analysisResult

	providerCfg := analyzer.ProviderConfigFromBlock(discovery.providerID, discovery.providerBlock)
	provider, err := analyzer.NewProvider(analyzer.ProviderID(discovery.providerID), providerCfg)
	if err != nil {
		return analysis, fmt.Errorf("instantiate provider %q: %w (%s)", discovery.providerID, err, config.RemediationMessage(discovery.providerID))
	}

	logger.Info("provider starting", logging.Any("id", discovery.providerID))
	startCtx, startCancel := context.WithTimeout(ctx, providerStartTimeout)
	if err := provider.Start(startCtx); err != nil {
		startCancel()
		_ = provider.Close()
		recordProviderFailure(currentState, discovery.providerID, err)
		runContext.persistFailureState(err, logger)
		return analysis, fmt.Errorf("start provider %q: %w (%s)", discovery.providerID, err, config.RemediationMessage(discovery.providerID))
	}
	startCancel()
	logger.Info("provider ready", logging.Any("id", discovery.providerID))

	mode := resolveExecutionMode(discovery.appConfig, opts, provider, logger)

	detectedToolchain := toolchain.Detect(discovery.projectPath)
	logger.Info("toolchain detected", logging.Any("summary", detectedToolchain.String()))

	codebaseContext, err := analyzer.BuildCodebaseContext(discovery.projectPath)
	if err != nil {
		logger.Warn("codebase context build failed", logging.Any("err", err))
	}
	logger.Info("codebase context", logging.Any("bytes", len(codebaseContext)))

	p2, p2Cleanup := setupPhase2Transport(ctx, opts, discovery.providerID, opts.Events, logger)
	defer p2Cleanup()

	phase1Factory, phase2Factory := buildSessionFactories(ctx, provider, discovery, p2.config, providerCfg.Sandbox, runID, logger)

	existingFindingHashes := collectDismissedHashes(currentState)
	phaseReq := analyzer.PhaseRequest{
		ProjectRoot:        discovery.projectPath,
		ToolchainSummary:   detectedToolchain.Summary(),
		PrimaryLinter:      detectedToolchain.PrimaryLinter(),
		TestFramework:      detectedToolchain.PrimaryTestFramework(),
		CodebaseContext:    codebaseContext,
		RunID:              runID,
		DryRun:             opts.DryRun,
		StrictLintRules:    !opts.Permissive,
		LintRuleValidator:  lintrules.NewValidator(),
		ExistingHashes:     existingFindingHashes,
		Phase2Mode:         p2.mode,
		FindingsOutputPath: p2.findingsFilePath,
		CLIBinaryPath:      p2.binaryPath,
		FindingRedactor:    findingRedactorFunc(transcript.redactor),
	}

	logEnabledRulePacks(logger, runContext.packs)
	orchestrator := analyzer.NewOrchestrator(runContext.packs)
	rc := analyzer.RunConfig{
		Phase1SessionFactory: phase1Factory,
		Phase2SessionFactory: phase2Factory,
		Mode:                 mode,
		MaxConcurrency:       resolveMaxConcurrency(discovery.appConfig, opts),
	}
	chunkInputs := analyzer.ChunkInputs{
		Chunks:          transcript.chunks,
		RuleTimeoutSecs: discovery.appConfig.Analyzer.RuleTimeoutSeconds,
	}
	logger.Info("phase dispatch", logging.Any("mode", mode.String()), logging.Any("chunks", len(transcript.chunks)), logging.Any("concurrency", rc.MaxConcurrency))
	pipelineResult, err := orchestrator.RunChunks(ctx, rc, chunkInputs, phaseReq)
	if err != nil {
		_ = provider.Close()
		recordProviderFailure(currentState, discovery.providerID, err)
		runContext.persistFailureState(err, logger)
		return analysis, fmt.Errorf("run analyzer: %w", err)
	}
	logger.Info("orchestrator done", logging.Any("mistakes", len(pipelineResult.Mistakes)), logging.Any("findings", len(pipelineResult.Findings)), logging.Any("warnings", len(pipelineResult.Warnings)))
	for _, w := range pipelineResult.Warnings {
		logger.Warn("orchestrator warning", logging.Any("warning", w))
	}

	return analysisResult{result: pipelineResult, provider: provider, mode: mode}, nil
}

// runOutputAndPersist generates todos, updates state, saves, updates the
// discovery cache, and records history.
func runOutputAndPersist(opts Options, discovery discoveryResult, analysis analysisResult, transcript transcriptResult, runContext *runCtx, cacheKeys map[string]string, repoHeadSHA string, runStart time.Time, runID string, logger *logging.Logger) (Result, error) {
	currentState := runContext.state
	warnings := transcript.warnings
	warnings = append(warnings, analysis.result.Warnings...)

	pipelineResult := Result{
		ProviderID:      discovery.providerID,
		Findings:        len(analysis.result.Findings),
		Mistakes:        len(analysis.result.Mistakes),
		Warnings:        len(warnings),
		SourcesAnalyzed: len(transcript.sourcesUsed),
		MessagesRead:    transcript.messageCount,
	}

	if opts.DryRun {
		pipelineResult.TodosPath = todosOutputPath(discovery.outputRoot, discovery.projectName)
		publishRunDone(opts.Events, discovery.projectName, runID, pipelineResult.Findings, pipelineResult.SourcesAnalyzed, pipelineResult.MessagesRead)
		return pipelineResult, nil
	}

	if len(analysis.result.Mistakes) == 0 {
		warnings = append(warnings, "no recurring mistakes found")
		pipelineResult.NoMistakes = true
	}

	generateResult, err := output.GenerateTodos(discovery.projectName, analysis.result.Findings, output.GenerateOptions{
		OutputRoot:   discovery.outputRoot,
		ProjectTitle: discovery.projectName,
		Warnings:     warnings,
		RunID:        runID,
	})
	if err != nil {
		return Result{}, fmt.Errorf("generate todos: %w", err)
	}
	pipelineResult.TodosPath = generateResult.Path
	pipelineResult.Findings = generateResult.AddedFindings

	now := time.Now().UTC()
	currentState.LastRunUTC = now
	currentState.RepoHeadSHA = repoHeadSHA
	currentState.ChatHashes = cacheKeys
	currentState.FindingHashes = mergeHashLists(currentState.FindingHashes, collectFindingHashes(analysis.result.Findings))
	recordFindingApplySpecs(currentState, analysis.result.Findings, discovery.projectName)
	if currentState.LastRunPerCategory == nil {
		currentState.LastRunPerCategory = map[string]time.Time{}
	}
	for _, cat := range analysis.result.CompletedCategories {
		currentState.LastRunPerCategory[cat] = now
	}
	recordProviderSuccess(currentState, discovery.providerID, 0)

	if currentState.UsageStats == nil {
		currentState.UsageStats = map[string]int64{}
	}
	currentState.UsageStats["sources_analyzed"] += int64(len(transcript.sourcesUsed))
	currentState.UsageStats["messages_analyzed"] += int64(transcript.messageCount)
	currentState.UsageStats["mistakes_found"] += int64(len(analysis.result.Mistakes))
	currentState.UsageStats["findings_added"] += int64(generateResult.AddedFindings)
	currentState.UsageStats["redaction_hits"] += int64(transcript.redactionTotal)

	if err := runContext.savePrunedState(); err != nil {
		if logger != nil {
			logger.Warn("state save failed after todos were written — next run may re-analyze",
				logging.Any("err", err),
				logging.Any("project", discovery.projectName),
				logging.Any("todos_path", generateResult.Path),
			)
		}
		return Result{}, fmt.Errorf("save state: %w", err)
	}

	if opts.DiscoveryCache != nil {
		opts.DiscoveryCache.Update(discovery.projectPath, discovery.sources, repoHeadSHA)
	}

	perCategory := map[string]int{}
	for _, f := range analysis.result.Findings {
		perCategory[string(f.Category)]++
	}
	today := time.Now().UTC().Format("2006-01-02")
	if err := state.UpdateHistoryToday(discovery.outputRoot, discovery.projectName, today, state.DaySummaryDelta{
		Runs:          1,
		FindingsNew:   pipelineResult.Findings,
		FindingsTotal: len(currentState.FindingHashes),
		Tokens:        0,
		RunMillis:     time.Since(runStart).Milliseconds(),
		PerCategory:   perCategory,
	}); err != nil {
		logger.Warn("history update failed", logging.Any("err", err))
	}

	publishRunDone(opts.Events, discovery.projectName, runID, pipelineResult.Findings, pipelineResult.SourcesAnalyzed, pipelineResult.MessagesRead)
	return pipelineResult, nil
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
	runID := generateRunID()
	logger.Info("run id", logging.Any("run_id", runID))
	if opts.Events != nil {
		opts.Events.Publish(Event{Type: EventRunStart, Payload: map[string]any{"project": opts.ProjectName, "run_id": runID}})
	}

	// Stage 1: Discovery — paths, project config, chat sources.
	discovery, err := runDiscovery(opts, appConfig, logger)
	if err != nil {
		return Result{}, err
	}

	// Load state and repo HEAD for caching decisions.
	loadResult, err := state.LoadWithResult(discovery.outputRoot, discovery.projectName)
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
	repoHeadSHA := state.RepoHeadSHA(discovery.projectPath, logger)
	logger.Info("repo state", logging.Any("head_sha", repoHeadSHA), logging.Any("prior_run", currentState.LastRunUTC.Format(time.RFC3339)), logging.Any("prior_chats", len(currentState.ChatHashes)))

	// Stage 2: Caching — skip if nothing changed.
	cachingOutcome, err := runCaching(opts, discovery, currentState, repoHeadSHA, logger)
	if err != nil {
		return Result{}, err
	}
	if cachingOutcome.cacheHit {
		return Result{
			ProviderID: discovery.providerID,
			CacheHit:   true,
			TodosPath:  todosOutputPath(discovery.outputRoot, discovery.projectName),
		}, nil
	}

	// Stage 3: Transcript preparation — rule packs, redaction, chunking.
	transcript, zeroMessages, err := runTranscriptPrep(opts, discovery, discovery.sources, logger)
	if err != nil {
		return Result{}, err
	}

	runContext := &runCtx{
		outputRoot: discovery.outputRoot,
		project:    discovery.projectName,
		state:      currentState,
		packs:      transcript.rulePacks,
		providerID: discovery.providerID,
	}

	if zeroMessages {
		currentState.LastRunUTC = time.Now().UTC()
		currentState.RepoHeadSHA = repoHeadSHA
		currentState.ChatHashes = cachingOutcome.cacheKeys
		if saveErr := runContext.savePrunedState(); saveErr != nil {
			logger.Warn("preflight state save failed", logging.Any("err", saveErr))
		}
		publishRunDone(opts.Events, discovery.projectName, runID, 0, 0, 0)
		return Result{
			ProviderID:      discovery.providerID,
			SourcesAnalyzed: 0,
			MessagesRead:    0,
			Warnings:        len(transcript.warnings),
			TodosPath:       todosOutputPath(discovery.outputRoot, discovery.projectName),
			NoMistakes:      true,
		}, nil
	}

	// Stage 4: Analysis — provider, toolchain, orchestrator.
	analysis, err := runAnalysis(ctx, opts, discovery, transcript, runContext, currentState, runID, logger)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = analysis.provider.Close() }()

	// Stage 5: Output and persistence.
	return runOutputAndPersist(opts, discovery, analysis, transcript, runContext, cachingOutcome.cacheKeys, repoHeadSHA, runStart, runID, logger)
}

// publishRunDone is a nil-safe helper for the run.done event payload.
func publishRunDone(bus *EventBus, project, runID string, findingsNew, sources, messages int) {
	if bus == nil {
		return
	}
	bus.Publish(Event{Type: EventRunDone, Payload: map[string]any{
		"project":      project,
		"run_id":       runID,
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

// findingRedactorFunc adapts an *analyzer.Redactor to the simple
// func(string) string hook PhaseRequest expects. Returns nil when the
// redactor is nil so the analyzer-side hook stays a cheap no-op.
func findingRedactorFunc(r *analyzer.Redactor) func(string) string {
	if r == nil {
		return nil
	}
	return func(text string) string {
		out, _ := r.Redact(text)
		return out
	}
}

// buildPhase2Config assembles the per-mode Phase 2 wiring: a temp file for
// findings, the absolute dreamer binary path, and the transport-specific
// sub-config (MCP launch spec or CLI binary path). All wire-format detail
// lives in internal/mcpserver — pipeline only ferries the result.
//
// Returns (nil, "", "", nil) when the provider does not support tool-based
// Phase 2 (mode == Phase2ModeNone). Returns a non-nil error if any setup
// step fails; callers fall back to the legacy JSON transport.
//
// On error the returned findingsOutputPath may still be non-empty — the
// caller is responsible for removing it (the defer in pipeline.Run handles
// this for both success and failure paths).
func buildPhase2Config(mode analyzer.Phase2Mode) (cfg *analyzer.Phase2Config, findingsOutputPath, dreamerBinaryPath string, err error) {
	if mode == analyzer.Phase2ModeNone {
		return nil, "", "", nil
	}

	tmpFile, tmpErr := os.CreateTemp("", FindingsTempFilePattern)
	if tmpErr != nil {
		return nil, "", "", fmt.Errorf("create findings temp file: %w", tmpErr)
	}
	findingsOutputPath = tmpFile.Name()
	if closeErr := tmpFile.Close(); closeErr != nil {
		return nil, findingsOutputPath, "", fmt.Errorf("close findings temp file: %w", closeErr)
	}

	dreamerBinaryPath, binErr := mcpserver.FindDreamerBinary()
	if binErr != nil {
		return nil, findingsOutputPath, "", binErr
	}

	switch mode {
	case analyzer.Phase2ModeMCP:
		spec, specErr := mcpserver.BuildClientLaunchSpec(findingsOutputPath, dreamerBinaryPath)
		if specErr != nil {
			return nil, findingsOutputPath, dreamerBinaryPath, specErr
		}
		return &analyzer.Phase2Config{
			FindingsOutputPath: findingsOutputPath,
			MCP: &analyzer.Phase2MCPConfig{
				ConfigFilePath: spec.ConfigFilePath,
				ToolNames:      spec.ToolNames,
			},
		}, findingsOutputPath, dreamerBinaryPath, nil
	case analyzer.Phase2ModeCLI:
		return &analyzer.Phase2Config{
			FindingsOutputPath: findingsOutputPath,
			CLI: &analyzer.Phase2CLIConfig{
				DreamerBinaryPath: dreamerBinaryPath,
			},
		}, findingsOutputPath, dreamerBinaryPath, nil
	default:
		return nil, findingsOutputPath, dreamerBinaryPath, fmt.Errorf("unknown phase 2 mode %q", mode)
	}
}
