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
	"dreamer/internal/output"
	"dreamer/internal/state"
)

const (
	defaultConfigFileName = "config.yaml"
)

var (
	errNoChatSources    = errors.New("no chat sources discovered")
	errNoNewChatSources = errors.New("no new or changed chat sources to analyze")
)

type analyzeResult struct {
	TodosPath       string
	SourcesAnalyzed int
	MessagesRead    int
	FindingsFound   int
	TodosAdded      int
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
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for config path: %w", err)
		}
		return filepath.Join(homeDir, ".dreamer", defaultConfigFileName), nil
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

func analyzeProject(ctx context.Context, cfg *config.Config, project config.ProjectConfig) (analyzeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	currentState, err := state.LoadState(project.Name)
	if err != nil {
		return analyzeResult{}, fmt.Errorf("load state for project %q: %w", project.Name, err)
	}

	sources, err := chat.DiscoverChats(project.Path)
	if err != nil {
		return analyzeResult{}, fmt.Errorf("discover chats for project %q: %w", project.Name, err)
	}
	if len(sources) == 0 {
		return analyzeResult{}, fmt.Errorf("%w for project %q", errNoChatSources, project.Name)
	}

	sourcesToAnalyze := filterSourcesToAnalyze(sources, currentState)
	if len(sourcesToAnalyze) == 0 {
		return analyzeResult{}, fmt.Errorf("%w for project %q", errNoNewChatSources, project.Name)
	}

	analysisInput, analyzedSourceIDs, messageCount, diagnostics, err := buildAnalysisInputWithDiagnostics(sourcesToAnalyze)
	if err != nil {
		return analyzeResult{}, err
	}

	client, err := analyzer.NewClient(analyzerClientOptionsFromConfig(cfg, project.Path))
	if err != nil {
		return analyzeResult{}, wrapAnalyzerIntegrationError(err)
	}
	defer func() {
		_ = client.Close()
	}()

	session, err := client.NewSession(ctx)
	if err != nil {
		return analyzeResult{}, wrapAnalyzerIntegrationError(err)
	}
	defer func() {
		_ = session.Close()
	}()

	orchestrator := analyzer.NewOrchestrator(mergeRuleOverrides(cfg))
	response, err := orchestrator.Analyze(ctx, session, analysisInput)
	if err != nil {
		return analyzeResult{}, wrapAnalyzerIntegrationError(err)
	}

	generateResult, err := output.GenerateTodos(project.Name, response.Findings, output.GenerateOptions{
		OutputRoot: cfg.Daemon.OutputRoot,
	})
	if err != nil {
		return analyzeResult{}, fmt.Errorf("generate todos for project %q: %w", project.Name, err)
	}

	currentState.LastRun = time.Now().UTC()
	currentState.AnalyzedChatIDs = mergeAnalyzedIDs(currentState.AnalyzedChatIDs, analyzedSourceIDs)
	currentState.UsageStats["analyze_runs"]++
	currentState.UsageStats["sources_analyzed"] += int64(len(sourcesToAnalyze))
	currentState.UsageStats["messages_analyzed"] += int64(messageCount)
	currentState.UsageStats["findings_found"] += int64(len(response.Findings))
	currentState.UsageStats["todos_added"] += int64(generateResult.AddedFindings)
	applyClaudeProcessingDiagnostics(currentState.UsageStats, diagnostics)

	if err := state.SaveState(project.Name, currentState); err != nil {
		return analyzeResult{}, fmt.Errorf("save state for project %q: %w", project.Name, err)
	}

	return analyzeResult{
		TodosPath:       generateResult.Path,
		SourcesAnalyzed: len(sourcesToAnalyze),
		MessagesRead:    messageCount,
		FindingsFound:   len(response.Findings),
		TodosAdded:      generateResult.AddedFindings,
	}, nil
}

func filterSourcesToAnalyze(sources []chat.ChatSource, currentState *state.State) []chat.ChatSource {
	if currentState == nil {
		return slices.Clone(sources)
	}

	analyzedSet := make(map[string]struct{}, len(currentState.AnalyzedChatIDs))
	for _, analyzedID := range currentState.AnalyzedChatIDs {
		analyzedSet[analyzedID] = struct{}{}
	}

	filtered := make([]chat.ChatSource, 0, len(sources))
	for _, source := range sources {
		_, wasAnalyzed := analyzedSet[source.Path]
		if !wasAnalyzed || source.ModifiedTime.After(currentState.LastRun) {
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
	switch strings.ToLower(filepath.Ext(source.Path)) {
	case ".jsonl":
		messages, err := readers.ReadJSONLWithOptions(source.Path, readers.JSONLReadOptions{
			SanitizeClaude: source.Tool == chat.SourceTypeClaudeCodeSession,
		})
		if err != nil {
			return nil, fmt.Errorf("read jsonl chat source %q: %w", source.Path, err)
		}
		return messages, nil
	case ".pb", ".pbtxt":
		messages, err := readers.ReadProtobuf(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read protobuf chat source %q: %w", source.Path, err)
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported chat source file %q (supported: .jsonl, .pb, .pbtxt)", source.Path)
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

func mergeRuleOverrides(cfg *config.Config) []analyzer.AnalysisRule {
	rules := analyzer.DefaultRules()
	if cfg == nil || len(cfg.Analyzer.Rules) == 0 {
		return rules
	}

	for i := range rules {
		if override, ok := cfg.Analyzer.Rules[strings.ToLower(string(rules[i].Category))]; ok {
			rules[i].Enabled = override.Enabled
			continue
		}
		if override, ok := cfg.Analyzer.Rules[string(rules[i].Category)]; ok {
			rules[i].Enabled = override.Enabled
		}
	}

	return rules
}

func analyzerClientOptionsFromConfig(cfg *config.Config, projectPath string) analyzer.ClientOptions {
	options := analyzer.ClientOptions{}
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
	options.WorkingDirectory = strings.TrimSpace(projectPath)
	return options
}

func wrapAnalyzerIntegrationError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("analyzer request failed: %w", err)
}
