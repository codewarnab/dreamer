package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/chat"
	"dreamer/internal/chat/readers"
	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/output"
	"dreamer/internal/state"
)

const (
	defaultConfigFileName = "config.yaml"
)

var (
	errNoChatSources         = errors.New("no chat sources discovered")
	errNoLookbackChatSources = errors.New("no chat sources within lookback window")
	errNoNewChatSources      = errors.New("no new or changed chat sources to analyze")
)

type analyzeResult struct {
	TodosPath       string
	SourcesAnalyzed int
	MessagesRead    int
	FindingsFound   int
	TodosAdded      int
}

type analyzeOptions struct {
	Since string
	Now   time.Time
}

type claudeProcessingDiagnostics struct {
	TotalMessagesRead int
	MessagesKept      int
	MessagesDropped   int
	MessagesTruncated int
}

func (diagnostics *claudeProcessingDiagnostics) merge(next claudeProcessingDiagnostics) {
	diagnostics.TotalMessagesRead += next.TotalMessagesRead
	diagnostics.MessagesKept += next.MessagesKept
	diagnostics.MessagesDropped += next.MessagesDropped
	diagnostics.MessagesTruncated += next.MessagesTruncated
}

func resolveConfigPath(configPath string) (string, error) {
	if strings.TrimSpace(configPath) == "" {
		path, err := config.GlobalConfigPath()
		if err != nil {
			return "", fmt.Errorf("resolve global config path: %w", err)
		}
		return path, nil
	}

	expandedPath, err := expandHomePath(configPath)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expandedPath) {
		absolutePath, err := filepath.Abs(expandedPath)
		if err != nil {
			return "", fmt.Errorf("resolve absolute config path %q: %w", configPath, err)
		}
		return absolutePath, nil
	}

	return filepath.Clean(expandedPath), nil
}

func expandHomePath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		if path == "~" {
			return homeDir, nil
		}
		return filepath.Join(homeDir, path[2:]), nil
	}
	return path, nil
}

func selectProject(cfg *config.Config, projectName string) (*config.ProjectConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}

	name := strings.TrimSpace(projectName)
	if name == "" {
		return nil, fmt.Errorf("--project is required")
	}

	for i := range cfg.Projects {
		if cfg.Projects[i].Name == name {
			return &cfg.Projects[i], nil
		}
	}

	return nil, fmt.Errorf("project %q not found in config", name)
}

func analyzeProject(ctx context.Context, cfg *config.Config, project config.ProjectConfig, logger *logging.Logger, options analyzeOptions) (analyzeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	logger.Info("analysis started project=%q path=%q", project.Name, project.Path)
	currentState, err := state.LoadState(project.Name)
	if err != nil {
		logger.Error("load state failed project=%q error=%v", project.Name, err)
		return analyzeResult{}, fmt.Errorf("load state for project %q: %w", project.Name, err)
	}
	logger.Debug("state loaded project=%q analyzed_sources=%d last_run=%s", project.Name, len(currentState.ChatHashes), currentState.LastRunUTC.Format(time.RFC3339))

	sources, err := chat.DiscoverChats(project.Path)
	if err != nil {
		logger.Error("discover chats failed project=%q error=%v", project.Name, err)
		return analyzeResult{}, fmt.Errorf("discover chats for project %q: %w", project.Name, err)
	}
	logger.Info("chat discovery complete project=%q sources=%d", project.Name, len(sources))
	if len(sources) == 0 {
		logger.Warn("no chat sources discovered project=%q", project.Name)
		return analyzeResult{}, fmt.Errorf("%w for project %q", errNoChatSources, project.Name)
	}

	sinceValue := resolveLookbackValue(project, options)
	lookback, lookbackEnabled, err := parseLookbackWindow(sinceValue)
	if err != nil {
		return analyzeResult{}, fmt.Errorf("parse lookback window for project %q: %w", project.Name, err)
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}

	sources = filterSourcesByLookback(sources, options.Now, lookback, lookbackEnabled)
	if lookbackEnabled {
		logger.Info("lookback filtering complete project=%q since=%q selected=%d", project.Name, sinceValue, len(sources))
	}
	if len(sources) == 0 {
		logger.Warn("no chat sources within lookback window project=%q since=%q", project.Name, sinceValue)
		return analyzeResult{}, fmt.Errorf("%w %q for project %q", errNoLookbackChatSources, sinceValue, project.Name)
	}

	sourcesToAnalyze := filterSourcesToAnalyze(sources, currentState)
	logger.Info("source filtering complete project=%q selected=%d total=%d", project.Name, len(sourcesToAnalyze), len(sources))
	if len(sourcesToAnalyze) == 0 {
		logger.Warn("no new or changed chat sources project=%q", project.Name)
		return analyzeResult{}, fmt.Errorf("%w for project %q", errNoNewChatSources, project.Name)
	}

	provider, err := analyzer.NewProvider(analyzer.ProviderCopilotSDK, analyzerProviderConfigFromConfig(cfg))
	if err != nil {
		logger.Error("create analyzer provider failed project=%q error=%v", project.Name, err)
		return analyzeResult{}, wrapAnalyzerIntegrationError(err)
	}
	defer func() {
		_ = provider.Close()
	}()

	orchestrator := analyzer.NewOrchestrator(mergeRuleOverrides(cfg))
	result := analyzeResult{}
	var lastErr error
	for _, source := range sourcesToAnalyze {
		sourceResult, err := analyzeSource(ctx, cfg, project, source, logger, currentState, orchestrator, provider)
		if err != nil {
			logger.Error("chat source analysis failed project=%q source=%q error=%v", project.Name, source.Path, err)
			lastErr = err
			continue
		}

		result.TodosPath = sourceResult.TodosPath
		result.SourcesAnalyzed += sourceResult.SourcesAnalyzed
		result.MessagesRead += sourceResult.MessagesRead
		result.FindingsFound += sourceResult.FindingsFound
		result.TodosAdded += sourceResult.TodosAdded
	}
	if result.SourcesAnalyzed == 0 {
		return analyzeResult{}, fmt.Errorf("analyze chat sources for project %q: %w", project.Name, lastErr)
	}
	logger.Info("analysis complete project=%q sources=%d messages=%d findings=%d todos_added=%d", project.Name, result.SourcesAnalyzed, result.MessagesRead, result.FindingsFound, result.TodosAdded)

	return result, nil
}

func analyzeSource(ctx context.Context, cfg *config.Config, project config.ProjectConfig, source chat.ChatSource, logger *logging.Logger, currentState *state.State, orchestrator *analyzer.Orchestrator, provider analyzer.Provider) (analyzeResult, error) {
	analysisInput, analyzedSourceIDs, messageCount, diagnostics, err := buildAnalysisInputWithDiagnostics([]chat.ChatSource{source})
	if err != nil {
		return analyzeResult{}, fmt.Errorf("build analysis input for source %q: %w", source.Path, err)
	}
	logger.Info("analysis input built project=%q source=%q messages=%d", project.Name, source.Path, messageCount)

	session, err := provider.NewSession(ctx, analyzer.SessionConfig{
		WorkingDirectory: project.Path,
		Model:            cfg.Analyzer.Model,
		ReadOnly:         true,
	})
	if err != nil {
		return analyzeResult{}, wrapAnalyzerIntegrationError(err)
	}
	defer func() {
		_ = session.Close()
	}()

	response, err := orchestrator.Run(ctx, session, analyzer.PhaseRequest{
		Transcript:  analysisInput,
		ProjectRoot: project.Path,
	})
	if err != nil {
		return analyzeResult{}, wrapAnalyzerIntegrationError(err)
	}
	logger.Info("analyzer response received project=%q source=%q findings=%d", project.Name, source.Path, len(response.Findings))

	generateResult, err := output.GenerateTodos(project.Name, response.Findings, output.GenerateOptions{
		OutputRoot: cfg.Daemon.OutputRoot,
	})
	if err != nil {
		return analyzeResult{}, fmt.Errorf("generate todos for project %q source %q: %w", project.Name, source.Path, err)
	}
	logger.Info("todos generated project=%q source=%q added=%d path=%q", project.Name, source.Path, generateResult.AddedFindings, generateResult.Path)

	currentState.LastRunUTC = time.Now().UTC()
	for _, sourceID := range analyzedSourceIDs {
		if _, exists := currentState.ChatHashes[sourceID]; !exists {
			currentState.ChatHashes[sourceID] = ""
		}
	}
	currentState.UsageStats["analyze_runs"]++
	currentState.UsageStats["sources_analyzed"] += int64(len(analyzedSourceIDs))
	currentState.UsageStats["messages_analyzed"] += int64(messageCount)
	currentState.UsageStats["findings_found"] += int64(len(response.Findings))
	currentState.UsageStats["todos_added"] += int64(generateResult.AddedFindings)
	applyClaudeProcessingDiagnostics(currentState.UsageStats, diagnostics)

	if err := state.SaveState(project.Name, currentState); err != nil {
		return analyzeResult{}, fmt.Errorf("save state for project %q: %w", project.Name, err)
	}

	return analyzeResult{
		TodosPath:       generateResult.Path,
		SourcesAnalyzed: len(analyzedSourceIDs),
		MessagesRead:    messageCount,
		FindingsFound:   len(response.Findings),
		TodosAdded:      generateResult.AddedFindings,
	}, nil
}

func resolveLookbackValue(project config.ProjectConfig, options analyzeOptions) string {
	if strings.TrimSpace(options.Since) != "" {
		return options.Since
	}
	return project.Since
}

func filterSourcesToAnalyze(sources []chat.ChatSource, currentState *state.State) []chat.ChatSource {
	if currentState == nil {
		return slices.Clone(sources)
	}

	filtered := make([]chat.ChatSource, 0, len(sources))
	for _, source := range sources {
		_, wasAnalyzed := currentState.ChatHashes[source.Path]
		if !wasAnalyzed || source.ModifiedTime.After(currentState.LastRunUTC) {
			filtered = append(filtered, source)
		}
	}

	return filtered
}

func buildAnalysisInput(sources []chat.ChatSource) (string, []string, int, error) {
	input, sourceIDs, messageCount, _, err := buildAnalysisInputWithDiagnostics(sources)
	return input, sourceIDs, messageCount, err
}

func buildAnalysisInputWithDiagnostics(sources []chat.ChatSource) (string, []string, int, claudeProcessingDiagnostics, error) {
	var builder strings.Builder
	sourceIDs := make([]string, 0, len(sources))
	messageCount := 0
	var diagnostics claudeProcessingDiagnostics

	for _, source := range sources {
		messages, sourceDiagnostics, err := readMessagesForAnalysis(source)
		if err != nil {
			return "", nil, 0, diagnostics, err
		}
		diagnostics.merge(sourceDiagnostics)
		if len(messages) == 0 {
			if source.Tool == chat.SourceTypeAntigravityGemini {
				continue
			}
			return "", nil, 0, diagnostics, fmt.Errorf("chat source %q did not contain readable messages", source.Path)
		}

		sourceIDs = append(sourceIDs, source.Path)
		builder.WriteString(fmt.Sprintf("source: %s\n", source.Path))
		builder.WriteString(fmt.Sprintf("tool: %s\n\n", source.Tool))
		for _, message := range messages {
			if message.Content == "" {
				continue
			}

			messageCount++
			if !message.Timestamp.IsZero() {
				builder.WriteString(fmt.Sprintf("[%s] ", message.Timestamp.UTC().Format(time.RFC3339)))
			}
			builder.WriteString(message.Role)
			builder.WriteString(": ")
			builder.WriteString(message.Content)
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}

	if messageCount == 0 {
		return "", nil, 0, diagnostics, fmt.Errorf("no readable messages found in discovered chat sources")
	}

	return builder.String(), sourceIDs, messageCount, diagnostics, nil
}

func readMessagesForAnalysis(source chat.ChatSource) ([]readers.ChatMessage, claudeProcessingDiagnostics, error) {
	if source.Tool != chat.SourceTypeClaudeCodeSession || strings.ToLower(filepath.Ext(source.Path)) != ".jsonl" {
		messages, err := readMessagesFromSource(source)
		return messages, claudeProcessingDiagnostics{}, err
	}

	rawMessages, err := readers.ReadJSONL(source.Path)
	if err != nil {
		return nil, claudeProcessingDiagnostics{}, fmt.Errorf("read jsonl chat source %q: %w", source.Path, err)
	}
	sanitizedMessages := readers.SanitizeClaudeMessages(rawMessages)
	diagnostics := claudeProcessingDiagnostics{
		TotalMessagesRead: len(rawMessages),
		MessagesKept:      len(sanitizedMessages),
		MessagesDropped:   len(rawMessages) - len(sanitizedMessages),
	}
	return sanitizedMessages, diagnostics, nil
}

func applyClaudeProcessingDiagnostics(usageStats map[string]int64, diagnostics claudeProcessingDiagnostics) {
	if usageStats == nil || diagnostics.TotalMessagesRead == 0 {
		return
	}

	usageStats["claude_messages_total"] += int64(diagnostics.TotalMessagesRead)
	usageStats["claude_messages_kept"] += int64(diagnostics.MessagesKept)
	usageStats["claude_messages_dropped"] += int64(diagnostics.MessagesDropped)
	if diagnostics.MessagesTruncated > 0 {
		usageStats["claude_messages_truncated"] += int64(diagnostics.MessagesTruncated)
	}
}

func readMessagesFromSource(source chat.ChatSource) ([]readers.ChatMessage, error) {
	if source.Tool == chat.SourceTypeAntigravityGemini {
		messages, err := readers.ReadAntigravityGemini(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read antigravity chat source %q: %w", source.Path, err)
		}
		return messages, nil
	}

	if source.Tool == chat.SourceTypeGeminiCLISession {
		messages, err := readers.ReadGeminiCLI(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read gemini cli chat source %q: %w", source.Path, err)
		}
		return messages, nil
	}

	if source.Tool == chat.SourceTypeOpenCodeSession {
		dbPath, sessionID := chat.SplitSQLiteSourcePath(source.Path)
		messages, err := readers.ReadOpenCodeMessages(dbPath, sessionID)
		if err != nil {
			return nil, fmt.Errorf("read opencode chat source %q: %w", source.Path, err)
		}
		return messages, nil
	}

	if source.Tool == chat.SourceTypeKiroCLISession {
		dbPath, conversationID := chat.SplitSQLiteSourcePath(source.Path)
		messages, err := readers.ReadKiroConversation(dbPath, conversationID)
		if err != nil {
			return nil, fmt.Errorf("read kiro cli chat source %q: %w", source.Path, err)
		}
		return messages, nil
	}

	switch strings.ToLower(filepath.Ext(source.Path)) {
	case ".jsonl":
		if source.Tool == chat.SourceTypeVSCodeChatSession {
			messages, err := readers.ReadVSCodeChat(source.Path)
			if err != nil {
				return nil, fmt.Errorf("read vscode chat source %q: %w", source.Path, err)
			}
			return messages, nil
		}
		messages, err := readers.ReadJSONLWithOptions(source.Path, readers.JSONLReadOptions{
			SanitizeClaude:         source.Tool == chat.SourceTypeClaudeCodeSession,
			SanitizeCodex:          source.Tool == chat.SourceTypeCodexSessionJSONL,
			SanitizeCopilotSession: source.Tool == chat.SourceTypeCopilotSessionJSONL,
		})
		if err != nil {
			return nil, fmt.Errorf("read jsonl chat source %q: %w", source.Path, err)
		}
		return messages, nil
	case ".json":
		if source.Tool != chat.SourceTypeVSCodeChatSession {
			return nil, fmt.Errorf("unsupported chat source file %q (supported: .json only for vscode chat sources)", source.Path)
		}
		messages, err := readers.ReadVSCodeChat(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read vscode chat source %q: %w", source.Path, err)
		}
		return messages, nil
	case ".pb", ".pbtxt":
		messages, err := readers.ReadProtobuf(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read protobuf chat source %q: %w", source.Path, err)
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported chat source file %q (supported: .jsonl, .json for vscode, .pb, .pbtxt)", source.Path)
	}
}

func mergeAnalyzedIDs(existing []string, next []string) []string {
	unique := make(map[string]struct{}, len(existing)+len(next))
	merged := make([]string, 0, len(existing)+len(next))

	for _, id := range existing {
		if _, ok := unique[id]; ok {
			continue
		}
		unique[id] = struct{}{}
		merged = append(merged, id)
	}
	for _, id := range next {
		if _, ok := unique[id]; ok {
			continue
		}
		unique[id] = struct{}{}
		merged = append(merged, id)
	}

	return merged
}

func mergeRuleOverrides(cfg *config.Config) []analyzer.RulePack {
	packs, err := analyzer.LoadDefaultRulePacks()
	if err != nil {
		return nil
	}
	if cfg == nil || len(cfg.Analyzer.Rules) == 0 {
		return packs
	}
	for i := range packs {
		categoryKey := strings.ToLower(string(packs[i].Category))
		if override, ok := cfg.Analyzer.Rules[categoryKey]; ok {
			packs[i].Enabled = override.Enabled
			continue
		}
		if override, ok := cfg.Analyzer.Rules[string(packs[i].Category)]; ok {
			packs[i].Enabled = override.Enabled
		}
	}
	return packs
}

func analyzerProviderConfigFromConfig(cfg *config.Config) analyzer.ProviderConfig {
	options := analyzer.ProviderConfig{}
	if cfg == nil {
		return options
	}

	options.CopilotHome = cfg.Analyzer.CopilotHome
	options.CLIURL = cfg.Analyzer.CLIURL
	options.Model = cfg.Analyzer.Model
	if cfg.Analyzer.UseLoggedInUser != nil {
		options.UseLoggedInUser = *cfg.Analyzer.UseLoggedInUser
	}
	if cfg.Analyzer.AutoStart != nil {
		options.AutoStart = *cfg.Analyzer.AutoStart
	}
	return options
}

func wrapAnalyzerIntegrationError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("analyzer request failed: %w", err)
}
