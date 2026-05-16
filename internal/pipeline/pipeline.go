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

// Options captures everything the spec §17 pipeline needs for a single
// analyze invocation (cli flags + resolved global config).
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

// Run executes the end-to-end analyze pipeline against a single project path,
// following the sequence in spec §17.
func Run(ctx context.Context, opts Options, logger *logging.Logger) (Result, error) {
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

	currentState, err := state.Load(outputRoot, projectName)
	if err != nil {
		return Result{}, fmt.Errorf("load state: %w", err)
	}
	repoHeadSHA := state.RepoHeadSHA(projectPath, logger)
	logger.Info("repo state", logging.Any("head_sha", repoHeadSHA), logging.Any("prior_run", currentState.LastRunUTC.Format(time.RFC3339)), logging.Any("prior_chats", len(currentState.ChatHashes)))

	cacheKeys := make(map[string]string, len(sources))
	hashFailures := 0
	cached := 0
	changed := 0
	fresh := 0
	for _, source := range sources {
		fileHash, hashErr := state.HashFile(source.Path)
		if hashErr != nil {
			hashFailures++
			logger.Warn("hash chat source failed", logging.Any("path", source.Path), logging.Any("err", hashErr))
			continue
		}
		key := state.ChatCacheKey(source.Path, fileHash, repoHeadSHA)
		cacheKeys[source.Path] = key
		existing, seen := currentState.ChatHashes[source.Path]
		switch {
		case !seen:
			fresh++
		case existing != key:
			changed++
		default:
			cached++
		}
	}
	logger.Info("chat cache summary", logging.Any("total", len(sources)), logging.Any("cached", cached), logging.Any("changed", changed), logging.Any("new", fresh), logging.Any("hash_failed", hashFailures), logging.Any("force", opts.Force))

	if !opts.Force && cacheUnchanged(currentState, cacheKeys, repoHeadSHA) {
		logger.Info("cache hit", logging.Any("analyzing", 0), logging.Any("skipping", len(sources)), logging.Any("reason", "all cached, head unchanged"))
		return Result{
			ProviderID: providerID,
			CacheHit:   true,
			TodosPath:  todosOutputPath(outputRoot, projectName),
		}, nil
	}
	logger.Info("cache miss", logging.Any("analyzing", len(sources)), logging.Any("changed", changed), logging.Any("new", fresh), logging.Any("cached", cached))

	rulePacks := mergeRulePacks(cfg, projectFile)
	if !anyEnabled(rulePacks) {
		return Result{}, fmt.Errorf("all rule packs disabled; nothing to analyze")
	}

	redactor, err := buildRedactor(cfg, projectFile)
	if err != nil {
		return Result{}, err
	}

	transcript, sourcesUsed, messageCount, warnings, redactionTotal, err := buildRedactedTranscript(sources, redactor, logger)
	if err != nil {
		return Result{}, err
	}
	logger.Info("transcript built", logging.Any("sources_used", len(sourcesUsed)), logging.Any("messages", messageCount), logging.Any("transcript_bytes", len(transcript)), logging.Any("redaction_hits", redactionTotal))
	if messageCount == 0 {
		warnings = append(warnings, "no readable messages in discovered chats")
	}

	tc := toolchain.Detect(projectPath)
	logger.Info("toolchain detected", logging.Any("summary", tc.String()))

	codebaseContext, err := analyzer.BuildCodebaseContext(projectPath, tc)
	if err != nil {
		logger.Warn("codebase context build failed", logging.Any("err", err))
	}
	logger.Info("codebase context", logging.Any("bytes", len(codebaseContext)))

	providerCfg := buildProviderConfig(providerBlock)
	provider, err := analyzer.NewProvider(analyzer.ProviderID(providerID), providerCfg)
	if err != nil {
		return Result{}, fmt.Errorf("instantiate provider %q: %w (%s)", providerID, err, config.RemediationMessage(providerID))
	}
	defer func() { _ = provider.Close() }()

	logger.Info("provider starting", logging.Any("id", providerID))
	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	if err := provider.Start(startCtx); err != nil {
		startCancel()
		return Result{}, fmt.Errorf("start provider %q: %w (%s)", providerID, err, config.RemediationMessage(providerID))
	}
	startCancel()
	logger.Info("provider ready", logging.Any("id", providerID))

	logger.Info("session opening", logging.Any("provider", providerID), logging.Any("model", providerBlock.Model), logging.Any("workdir", projectPath))
	rawSession, err := provider.NewSession(ctx, analyzer.SessionConfig{
		WorkingDirectory: projectPath,
		Model:            providerBlock.Model,
		ReadOnly:         true,
		SystemMessage:    analyzer.BuildReadOnlySystemMessage(projectPath),
	})
	if err != nil {
		return Result{}, fmt.Errorf("create session: %w", err)
	}
	defer func() { _ = rawSession.Close() }()
	session := analyzer.NewLoggingSession(rawSession, logger, providerID)

	existingFindingHashes := stringSliceToSet(currentState.FindingHashes)
	phaseReq := analyzer.PhaseRequest{
		Transcript:        transcript,
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
	analysisResult, err := orchestrator.Run(ctx, session, phaseReq)
	if err != nil {
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

	currentState.LastRunUTC = time.Now().UTC()
	currentState.RepoHeadSHA = repoHeadSHA
	currentState.ChatHashes = cacheKeys
	currentState.FindingHashes = mergeHashLists(currentState.FindingHashes, collectFindingHashes(analysisResult.Findings))
	usage := currentState.ProviderUsage[providerID]
	usage.Runs++
	currentState.ProviderUsage[providerID] = usage

	if currentState.UsageStats == nil {
		currentState.UsageStats = map[string]int64{}
	}
	currentState.UsageStats["sources_analyzed"] += int64(len(sourcesUsed))
	currentState.UsageStats["messages_analyzed"] += int64(messageCount)
	currentState.UsageStats["mistakes_found"] += int64(len(analysisResult.Mistakes))
	currentState.UsageStats["findings_added"] += int64(generateResult.AddedFindings)
	currentState.UsageStats["redaction_hits"] += int64(redactionTotal)

	if err := state.Save(outputRoot, projectName, currentState); err != nil {
		return Result{}, fmt.Errorf("save state: %w", err)
	}
	return result, nil
}
