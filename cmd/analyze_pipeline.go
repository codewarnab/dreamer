package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/grounding"
	"dreamer/internal/analyzer/lintrules"
	"dreamer/internal/analyzer/toolchain"
	"dreamer/internal/chat"
	"dreamer/internal/chat/readers"
	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/output"
	"dreamer/internal/state"
)

// executeOptions captures everything the spec §17 pipeline needs to know
// for a single analyze invocation (cli flags + resolved global config).
type executeOptions struct {
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

// executeResult bundles the metrics + paths the analyze command surfaces.
type executeResult struct {
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

var (
	projectNameSafePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// executeAnalyze runs the end-to-end analyze pipeline against a single
// project path, following the sequence in spec §17.
func executeAnalyze(ctx context.Context, opts executeOptions, logger *logging.Logger) (executeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := opts.Config
	if cfg == nil {
		return executeResult{}, fmt.Errorf("executeAnalyze: config is required")
	}

	projectPath, err := resolveAbsoluteProjectPath(opts.ProjectPath)
	if err != nil {
		return executeResult{}, err
	}
	projectName := strings.TrimSpace(opts.ProjectName)
	if projectName == "" {
		projectName = DeriveProjectName(projectPath)
	}
	logger.Info("analyze begin project=%q path=%q", projectName, projectPath)

	projectFile, err := config.LoadProjectFileConfig(projectPath)
	if err != nil {
		return executeResult{}, fmt.Errorf("load project config: %w", err)
	}

	providerID, providerBlock := cfg.ResolveProviderConfig(projectFile, opts.ProviderID)
	logger.Info("provider resolved id=%q", providerID)

	outputRoot := strings.TrimSpace(opts.OutputDir)
	if outputRoot == "" {
		outputRoot = cfg.Daemon.OutputRoot
	}

	sources, err := chat.DiscoverChats(projectPath)
	if err != nil {
		return executeResult{}, fmt.Errorf("discover chats: %w", err)
	}
	logDiscoveredSources(logger, sources)

	if strings.TrimSpace(opts.Since) != "" {
		lookback, enabled, err := parseLookbackWindow(opts.Since)
		if err != nil {
			return executeResult{}, fmt.Errorf("parse --since: %w", err)
		}
		before := len(sources)
		sources = filterSourcesByLookback(sources, time.Now().UTC(), lookback, enabled)
		logger.Info("lookback filter since=%q kept=%d/%d", opts.Since, len(sources), before)
	}

	currentState, err := state.Load(outputRoot, projectName)
	if err != nil {
		return executeResult{}, fmt.Errorf("load state: %w", err)
	}
	repoHeadSHA := state.RepoHeadSHA(projectPath)
	logger.Info("repo head_sha=%q prior_run=%q prior_chats=%d",
		repoHeadSHA, currentState.LastRunUTC.Format(time.RFC3339), len(currentState.ChatHashes))

	cacheKeys := make(map[string]string, len(sources))
	hashFailures := 0
	cached := 0
	changed := 0
	fresh := 0
	for _, source := range sources {
		fileHash, hashErr := state.HashFile(source.Path)
		if hashErr != nil {
			hashFailures++
			logger.Warn("hash chat source failed path=%q error=%v", source.Path, hashErr)
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
	logger.Info("chat cache summary total=%d cached=%d changed=%d new=%d hash_failed=%d force=%v",
		len(sources), cached, changed, fresh, hashFailures, opts.Force)

	if !opts.Force && cacheUnchanged(currentState, cacheKeys, repoHeadSHA) {
		logger.Info("cache hit; analyzing=0 skipping=%d (all cached, head unchanged)", len(sources))
		return executeResult{
			ProviderID: providerID,
			CacheHit:   true,
			TodosPath:  todosOutputPath(outputRoot, projectName),
		}, nil
	}
	logger.Info("cache miss; analyzing=%d (changed=%d new=%d cached=%d will-re-bundle)",
		len(sources), changed, fresh, cached)

	rulePacks := mergeRulePacks(cfg, projectFile)
	if !anyEnabled(rulePacks) {
		return executeResult{}, fmt.Errorf("all rule packs disabled; nothing to analyze")
	}

	redactor, err := buildRedactor(cfg, projectFile)
	if err != nil {
		return executeResult{}, err
	}

	transcript, sourcesUsed, messageCount, warnings, redactionTotal, err := buildRedactedTranscript(sources, redactor, logger)
	if err != nil {
		return executeResult{}, err
	}
	logger.Info("transcript built sources_used=%d messages=%d transcript_bytes=%d redaction_hits=%d",
		len(sourcesUsed), messageCount, len(transcript), redactionTotal)
	if messageCount == 0 {
		warnings = append(warnings, "no readable messages in discovered chats")
	}

	tc := toolchain.Detect(projectPath)
	logger.Info("toolchain detected %s", tc.String())

	codebaseContext, err := buildCodebaseContext(projectPath, tc)
	if err != nil {
		logger.Warn("codebase context build failed: %v", err)
	}
	logger.Info("codebase context bytes=%d", len(codebaseContext))

	providerCfg := buildProviderConfig(providerBlock)
	provider, err := analyzer.NewProvider(analyzer.ProviderID(providerID), providerCfg)
	if err != nil {
		return executeResult{}, fmt.Errorf("instantiate provider %q: %w (%s)", providerID, err, config.RemediationMessage(providerID))
	}
	defer func() { _ = provider.Close() }()

	logger.Info("provider starting id=%q", providerID)
	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	if err := provider.Start(startCtx); err != nil {
		startCancel()
		return executeResult{}, fmt.Errorf("start provider %q: %w (%s)", providerID, err, config.RemediationMessage(providerID))
	}
	startCancel()
	logger.Info("provider ready id=%q", providerID)

	logger.Info("session opening provider=%q model=%q workdir=%q",
		providerID, providerBlock.Model, projectPath)
	rawSession, err := provider.NewSession(ctx, analyzer.SessionConfig{
		WorkingDirectory: projectPath,
		Model:            providerBlock.Model,
		ReadOnly:         true,
		SystemMessage:    analyzer.BuildReadOnlySystemMessage(projectPath),
	})
	if err != nil {
		return executeResult{}, fmt.Errorf("create session: %w", err)
	}
	defer func() { _ = rawSession.Close() }()
	session := newLoggingSession(rawSession, logger, providerID)

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
		return executeResult{}, fmt.Errorf("run analyzer: %w", err)
	}
	logger.Info("orchestrator done mistakes=%d findings=%d warnings=%d",
		len(analysisResult.Mistakes), len(analysisResult.Findings), len(analysisResult.Warnings))
	for _, w := range analysisResult.Warnings {
		logger.Warn("orchestrator warning: %s", w)
	}
	warnings = append(warnings, analysisResult.Warnings...)

	result := executeResult{
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
		return executeResult{}, fmt.Errorf("generate todos: %w", err)
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
		return executeResult{}, fmt.Errorf("save state: %w", err)
	}
	return result, nil
}

// DeriveProjectName converts an absolute project path into the directory
// name dreamer uses under the output root (spec §11.1). Output is always
// "project-<basename>"; characters outside [A-Za-z0-9._-] become "_" and a
// short hash suffix disambiguates paths whose basename had to be rewritten
// (so /a/foo and /b/foo bar don't collide).
func DeriveProjectName(projectPath string) string {
	clean := filepath.Clean(projectPath)
	base := filepath.Base(clean)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "root"
	}
	safe := projectNameSafePattern.ReplaceAllString(base, "_")
	if safe == "" {
		safe = "root"
	}
	if safe == base {
		return "project-" + safe
	}
	sum := sha256.Sum256([]byte(clean))
	return "project-" + safe + "-" + hex.EncodeToString(sum[:])[:8]
}

func resolveAbsoluteProjectPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("--path is required")
	}
	expanded, err := config.ExpandUserHome(trimmed)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func cacheUnchanged(currentState *state.State, cacheKeys map[string]string, repoHeadSHA string) bool {
	if currentState == nil || len(currentState.ChatHashes) == 0 {
		return false
	}
	if currentState.RepoHeadSHA != repoHeadSHA {
		return false
	}
	if len(cacheKeys) != len(currentState.ChatHashes) {
		return false
	}
	for path, key := range cacheKeys {
		existing, ok := currentState.ChatHashes[path]
		if !ok || existing != key {
			return false
		}
	}
	return true
}

func anyEnabled(packs []analyzer.RulePack) bool {
	for _, p := range packs {
		if p.Enabled {
			return true
		}
	}
	return false
}

func mergeRulePacks(cfg *config.Config, project *config.ProjectFileConfig) []analyzer.RulePack {
	packs, err := analyzer.LoadDefaultRulePacks()
	if err != nil {
		return nil
	}
	applyRuleToggles(packs, cfg.Analyzer.Rules)
	if project != nil {
		applyRuleToggles(packs, project.Rules)
	}
	if cfg.Analyzer.RuleTimeoutSeconds > 0 {
		for i := range packs {
			packs[i].TimeoutSeconds = cfg.Analyzer.RuleTimeoutSeconds
		}
	}
	return packs
}

func applyRuleToggles(packs []analyzer.RulePack, overrides map[string]config.RuleConfig) {
	if len(overrides) == 0 {
		return
	}
	for i := range packs {
		key := strings.ToLower(string(packs[i].Category))
		override, ok := overrides[key]
		if !ok {
			override, ok = overrides[string(packs[i].Category)]
		}
		if !ok {
			continue
		}
		packs[i].Enabled = override.Enabled
	}
}

func buildRedactor(cfg *config.Config, project *config.ProjectFileConfig) (*analyzer.Redactor, error) {
	patterns := append([]string{}, cfg.Redaction.Patterns...)
	if project != nil {
		patterns = append(patterns, project.Redaction.Patterns...)
	}
	return analyzer.NewRedactor(patterns)
}

func buildRedactedTranscript(sources []chat.ChatSource, redactor *analyzer.Redactor, logger *logging.Logger) (string, []chat.ChatSource, int, []string, int, error) {
	var b strings.Builder
	usedSources := make([]chat.ChatSource, 0, len(sources))
	warnings := []string{}
	messageCount := 0
	totalHits := 0
	for _, source := range sources {
		messages, err := readMessagesFromSource(source)
		if err != nil {
			logger.Warn("source read failed path=%q tool=%s error=%v", source.Path, source.Tool, err)
			warnings = append(warnings, fmt.Sprintf("Skipped %s (%v).", source.Path, err))
			continue
		}
		raw := len(messages)
		if source.Tool == chat.SourceTypeClaudeCodeSession && strings.ToLower(filepath.Ext(source.Path)) == ".jsonl" {
			messages = readers.SanitizeClaudeMessages(messages)
		}
		if len(messages) == 0 {
			logger.Info("source empty path=%q tool=%s raw=%d", source.Path, source.Tool, raw)
			continue
		}
		usedSources = append(usedSources, source)
		fmt.Fprintf(&b, "source: %s\n", source.Path)
		fmt.Fprintf(&b, "tool: %s\n\n", source.Tool)
		sourceMessages := 0
		sourceHits := 0
		for _, message := range messages {
			text := strings.TrimSpace(message.Content)
			if text == "" {
				continue
			}
			redacted, result := redactor.Redact(text)
			totalHits += result.TotalHits()
			sourceHits += result.TotalHits()
			messageCount++
			sourceMessages++
			if !message.Timestamp.IsZero() {
				b.WriteString("[")
				b.WriteString(message.Timestamp.UTC().Format(time.RFC3339))
				b.WriteString("] ")
			}
			b.WriteString(message.Role)
			b.WriteString(": ")
			b.WriteString(redacted)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
		logger.Info("source read path=%q tool=%s raw=%d kept=%d redactions=%d",
			source.Path, source.Tool, raw, sourceMessages, sourceHits)
	}
	return b.String(), usedSources, messageCount, warnings, totalHits, nil
}

func buildCodebaseContext(projectRoot string, tc toolchain.Toolchain) (string, error) {
	files, err := grounding.DetectFiles(projectRoot, grounding.DefaultFileCap)
	if err != nil {
		return "", fmt.Errorf("detect files: %w", err)
	}
	symbols := grounding.BuildSymbolIndex(projectRoot, files, 800)
	return grounding.BuildContext(files, symbols, grounding.DefaultFileCap, 800), nil
}

func buildProviderConfig(block config.ProviderBlock) analyzer.ProviderConfig {
	out := analyzer.ProviderConfig{
		Model:          block.Model,
		CopilotHome:    block.CopilotHome,
		CLIURL:         block.CLIURL,
		Command:        append([]string(nil), block.Command...),
		APIKeyEnv:      block.APIKeyEnv,
		MaxInputTokens: block.MaxInputTokens,
	}
	if block.UseLoggedInUser != nil {
		out.UseLoggedInUser = *block.UseLoggedInUser
	}
	if block.AutoStart != nil {
		out.AutoStart = *block.AutoStart
	}
	if len(block.Env) > 0 {
		out.Env = map[string]string{}
		for k, v := range block.Env {
			out.Env[k] = v
		}
	}
	return out
}

func collectFindingHashes(findings []analyzer.Finding) []string {
	hashes := make([]string, 0, len(findings))
	for _, finding := range findings {
		if strings.TrimSpace(finding.Hash) == "" {
			continue
		}
		hashes = append(hashes, finding.Hash)
	}
	return hashes
}

func mergeHashLists(base []string, addition []string) []string {
	seen := make(map[string]struct{}, len(base)+len(addition))
	merged := make([]string, 0, len(base)+len(addition))
	for _, h := range base {
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		merged = append(merged, h)
	}
	for _, h := range addition {
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		merged = append(merged, h)
	}
	sort.Strings(merged)
	return merged
}

func stringSliceToSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}

func todosOutputPath(outputRoot string, projectName string) string {
	root := strings.TrimSpace(outputRoot)
	if root == "" {
		return ""
	}
	return filepath.Join(root, projectName, "todos.md")
}

// errProviderNotRegistered surfaces the spec §13 hard-fail when the resolved
// provider id has no factory available.
var errProviderNotRegistered = errors.New("provider not registered")

func logDiscoveredSources(logger *logging.Logger, sources []chat.ChatSource) {
	logger.Info("discovery done sources=%d", len(sources))
	if len(sources) == 0 {
		return
	}
	byTool := map[chat.SourceType]int{}
	for _, s := range sources {
		byTool[s.Tool]++
		logger.Info("discovered source tool=%s mtime=%s path=%q",
			s.Tool, s.ModifiedTime.UTC().Format(time.RFC3339), s.Path)
	}
	tools := make([]string, 0, len(byTool))
	for tool, count := range byTool {
		tools = append(tools, fmt.Sprintf("%s=%d", tool, count))
	}
	sort.Strings(tools)
	logger.Info("discovery breakdown %s", strings.Join(tools, " "))
}

func logEnabledRulePacks(logger *logging.Logger, packs []analyzer.RulePack) {
	enabled := make([]string, 0, len(packs))
	disabled := make([]string, 0, len(packs))
	for _, p := range packs {
		if p.Enabled {
			enabled = append(enabled, string(p.Category))
		} else {
			disabled = append(disabled, string(p.Category))
		}
	}
	sort.Strings(enabled)
	sort.Strings(disabled)
	logger.Info("rule packs enabled=[%s] disabled=[%s]",
		strings.Join(enabled, ","), strings.Join(disabled, ","))
}

// loggingSession wraps an analyzer.Session and records every prompt/response
// pair to the dreamer log. We log the full prompt and full response so that
// operators can see exactly what was sent to the model. The wrapper is a
// transparent pass-through aside from logging.
type loggingSession struct {
	inner    analyzer.Session
	logger   *logging.Logger
	provider string
	runIndex int
}

func newLoggingSession(inner analyzer.Session, logger *logging.Logger, providerID string) analyzer.Session {
	return &loggingSession{inner: inner, logger: logger, provider: providerID}
}

func (s *loggingSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	s.runIndex++
	idx := s.runIndex
	s.logger.Info("provider call #%d provider=%q timeout=%s prompt_bytes=%d",
		idx, s.provider, timeout, len(prompt))
	start := time.Now()
	out, err := s.inner.Run(ctx, prompt, timeout)
	elapsed := time.Since(start)
	if err != nil {
		s.logger.Error("provider call #%d FAILED elapsed=%s error=%v", idx, elapsed, err)
		return out, err
	}
	s.logger.Info("provider call #%d response_bytes=%d elapsed=%s", idx, len(out), elapsed)
	s.logger.Info("provider call #%d RESPONSE BEGIN >>>\n%s\n<<< provider call #%d RESPONSE END",
		idx, out, idx)
	return out, nil
}

func (s *loggingSession) Close() error { return s.inner.Close() }
