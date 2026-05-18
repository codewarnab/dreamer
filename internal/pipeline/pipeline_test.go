package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/state"
)

const fakeProviderID = "pipeline-test-provider"

var fakeProviderState = struct {
	sync.Mutex
	mode string
}{mode: "happy"}

func init() {
	analyzer.RegisterProvider(analyzer.ProviderID(fakeProviderID), func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		fakeProviderState.Lock()
		mode := fakeProviderState.mode
		fakeProviderState.Unlock()
		return fakeAnalysisProvider{mode: mode}, nil
	})
}

func TestRunHappyPathWritesTodosAndState(t *testing.T) {
	projectDir, outputRoot, cfg := newPipelineFixture(t)
	writeCodexChatFixture(t, projectDir)
	setFakeProviderMode(t, "happy")

	logger := newTestLogger(t, outputRoot)
	result, err := Run(context.Background(), Options{
		Config:      cfg,
		ProjectPath: projectDir,
	}, logger)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Findings != 1 {
		t.Fatalf("Findings = %d, want 1", result.Findings)
	}
	if result.SourcesAnalyzed != 1 || result.MessagesRead == 0 {
		t.Fatalf("source metrics = (%d, %d), want one source with messages", result.SourcesAnalyzed, result.MessagesRead)
	}

	todos := readFileString(t, result.TodosPath)
	assertContains(t, todos, "Add a regression test for empty chat payloads")
	assertContains(t, todos, "<!-- dreamer:finding:")

	current, err := state.Load(outputRoot, deriveProjectName(projectDir))
	if err != nil {
		t.Fatalf("state.Load returned error: %v", err)
	}
	if len(current.ChatHashes) != 1 {
		t.Fatalf("len(ChatHashes) = %d, want 1", len(current.ChatHashes))
	}
	if len(current.FindingHashes) != 1 {
		t.Fatalf("len(FindingHashes) = %d, want 1", len(current.FindingHashes))
	}
}

func TestRunDryRunDoesNotWriteTodosOrState(t *testing.T) {
	projectDir, outputRoot, cfg := newPipelineFixture(t)
	writeCodexChatFixture(t, projectDir)
	setFakeProviderMode(t, "happy")

	logger := newTestLogger(t, outputRoot)
	result, err := Run(context.Background(), Options{
		Config:      cfg,
		ProjectPath: projectDir,
		DryRun:      true,
	}, logger)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.TodosPath == "" {
		t.Fatalf("dry run should still report the todos path")
	}
	if _, err := os.Stat(result.TodosPath); !os.IsNotExist(err) {
		t.Fatalf("dry run should not write todos.md, stat err=%v", err)
	}

	statePath, err := state.PathForProject(outputRoot, deriveProjectName(projectDir))
	if err != nil {
		t.Fatalf("PathForProject returned error: %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("dry run should not write state.json, stat err=%v", err)
	}
}

func TestRunWritesWarningsWhenAnalyzerParseFails(t *testing.T) {
	projectDir, outputRoot, cfg := newPipelineFixture(t)
	writeCodexChatFixture(t, projectDir)
	setFakeProviderMode(t, "parse-warning")

	logger := newTestLogger(t, outputRoot)
	result, err := Run(context.Background(), Options{
		Config:      cfg,
		ProjectPath: projectDir,
	}, logger)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Warnings == 0 {
		t.Fatalf("Warnings = 0, want parse warning")
	}

	todos := readFileString(t, result.TodosPath)
	assertContains(t, todos, "## Warnings")
	assertContains(t, todos, "parse failed")
}

func TestRunReturnsCacheHitWhenSourcesAndRepoAreUnchanged(t *testing.T) {
	projectDir, outputRoot, cfg := newPipelineFixture(t)
	writeCodexChatFixture(t, projectDir)
	setFakeProviderMode(t, "happy")

	logger := newTestLogger(t, outputRoot)
	first, err := Run(context.Background(), Options{
		Config:      cfg,
		ProjectPath: projectDir,
	}, logger)
	if err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}

	second, err := Run(context.Background(), Options{
		Config:      cfg,
		ProjectPath: projectDir,
	}, logger)
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if !second.CacheHit {
		t.Fatalf("second run CacheHit = false, want true")
	}
	if second.TodosPath != first.TodosPath {
		t.Fatalf("cache-hit TodosPath = %q, want %q", second.TodosPath, first.TodosPath)
	}
}

func newPipelineFixture(t *testing.T) (projectDir string, outputRoot string, cfg *config.Config) {
	t.Helper()

	home := t.TempDir()
	setPipelineTestHome(t, home)
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("GEMINI_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("KIRO_CLI_DB", "")

	projectDir = filepath.Join(t.TempDir(), "dreamer-test-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write project file: %v", err)
	}

	outputRoot = t.TempDir()
	cfg = &config.Config{
		DefaultProvider: fakeProviderID,
		Daemon: config.DaemonConfig{
			FrequencySeconds: 60,
			OutputRoot:       outputRoot,
		},
		Logging: config.LoggingConfig{Level: "debug"},
		Providers: map[string]config.ProviderBlock{
			fakeProviderID: {},
		},
		Analyzer: config.AnalyzerConfig{
			RuleTimeoutSeconds: 1,
			Rules:              onlyTestRuleEnabled(),
		},
	}
	return projectDir, outputRoot, cfg
}

func onlyTestRuleEnabled() map[string]config.RuleConfig {
	rules := map[string]config.RuleConfig{}
	for _, category := range analyzer.AllRuleCategories() {
		rules[string(category)] = config.RuleConfig{Enabled: category == analyzer.RuleCategoryTest}
	}
	return rules
}

func writeCodexChatFixture(t *testing.T, projectDir string) {
	t.Helper()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir returned error: %v", err)
	}
	chatPath := filepath.Join(home, ".codex", "sessions", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(chatPath), 0o755); err != nil {
		t.Fatalf("create codex session dir: %v", err)
	}
	content := `{"type":"session_meta","payload":{"cwd":` + quoteJSON(projectDir) + `}}` + "\n" +
		`{"role":"user","content":"assistant forgot the empty payload regression","timestamp":"2026-05-01T00:00:00Z"}` + "\n" +
		`{"role":"assistant","content":"I fixed it without adding a test","timestamp":"2026-05-01T00:00:01Z"}` + "\n"
	if err := os.WriteFile(chatPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write codex session fixture: %v", err)
	}
}

func setFakeProviderMode(t *testing.T, mode string) {
	t.Helper()

	fakeProviderState.Lock()
	previous := fakeProviderState.mode
	fakeProviderState.mode = mode
	fakeProviderState.Unlock()
	t.Cleanup(func() {
		fakeProviderState.Lock()
		fakeProviderState.mode = previous
		fakeProviderState.Unlock()
	})
}

func newTestLogger(t *testing.T, outputRoot string) *logging.Logger {
	t.Helper()

	logger, err := logging.New(outputRoot, "debug")
	if err != nil {
		t.Fatalf("logging.New returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = logger.Close()
	})
	return logger
}

func setPipelineTestHome(t *testing.T, home string) {
	t.Helper()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

func quoteJSON(value string) string {
	data, err := json.Marshal(filepath.ToSlash(value))
	if err != nil {
		return `""`
	}
	return string(data)
}

type fakeAnalysisProvider struct {
	mode string
}

func (p fakeAnalysisProvider) ID() string {
	return fakeProviderID
}

func (p fakeAnalysisProvider) Start(ctx context.Context) error {
	return nil
}

func (p fakeAnalysisProvider) NewSession(ctx context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	return fakeAnalysisSession{mode: p.mode}, nil
}

func (p fakeAnalysisProvider) Close() error {
	return nil
}

type fakeAnalysisSession struct {
	mode string
}

func (s fakeAnalysisSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	if s.mode == "parse-warning" {
		return "{not-json", nil
	}
	if isPhase2Prompt(prompt) {
		return `{"findings":{"test":[{"category":"test","mistake":"Add a regression test for empty chat payloads","guardrail":{"kind":"test","tool":"go test","rule":"empty chat payload regression","config_snippet":"func TestEmptyPayload(t *testing.T) {\n\n    // assert empty payload\n}"},"codebase_evidence":[{"path":"main.go","lines":"1-3","symbol":"main"}],"confidence":0.99}]}}`, nil
	}
	return `{"summary":"developer worked on empty-payload regression with no test","mistakes":{"test":[{"category":"test","summary":"Add a regression test for empty chat payloads","evidence_excerpt":"fixed it without adding a test","confidence":0.99}]}}`, nil
}

func (s fakeAnalysisSession) Close() error {
	return nil
}

func isPhase2Prompt(prompt string) bool {
	return strings.Contains(prompt, "synthesizing guardrails")
}
