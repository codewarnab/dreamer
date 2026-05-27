package pipeline

import (
	"context"
	"os"
	"strings"
	"testing"

	"dreamer/internal/state"
)

// #14: Preflight short-circuit completes fast for an empty project and saves state.
func TestPreflightShortCircuitForEmptyProject(t *testing.T) {
	projectDir, outputRoot, cfg := newPipelineFixture(t)
	setFakeProviderMode(t, "happy")

	logger := newTestLogger(t, outputRoot)
	res, err := Run(context.Background(), Options{Config: cfg, ProjectPath: projectDir}, logger)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.NoMistakes {
		t.Fatalf("res.NoMistakes = false, want true (preflight skip)")
	}
	if res.SourcesAnalyzed != 0 || res.MessagesRead != 0 {
		t.Fatalf("expected zero analyzed sources/messages, got %d / %d", res.SourcesAnalyzed, res.MessagesRead)
	}

	statePath, _ := state.PathForProject(outputRoot, DeriveProjectName(projectDir, nil))
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("preflight must save state to enable next-run cache hit, got stat err=%v", err)
	}
	loaded, err := state.Load(outputRoot, DeriveProjectName(projectDir, nil))
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	if loaded.LastRunUTC.IsZero() {
		t.Fatalf("preflight save must populate LastRunUTC")
	}

	// Second run: cache hit.
	res2, err := Run(context.Background(), Options{Config: cfg, ProjectPath: projectDir}, logger)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !res2.CacheHit {
		t.Fatalf("second run must cache-hit, got %+v", res2)
	}
}

// #19: --parallel against a provider lacking ParallelCapable falls back to sequential.
func TestParallelFallbackWhenProviderNotCapable(t *testing.T) {
	projectDir, outputRoot, cfg := newPipelineFixture(t)
	writeCodexChatFixture(t, projectDir)
	setFakeProviderMode(t, "happy")

	logger := newTestLogger(t, outputRoot)
	_, err := Run(context.Background(), Options{
		Config:           cfg,
		ProjectPath:      projectDir,
		ParallelOverride: true,
	}, logger)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = logger.Close()

	logBytes, readErr := os.ReadFile(logger.Path())
	if readErr != nil {
		t.Fatalf("read log %q: %v", logger.Path(), readErr)
	}
	if !strings.Contains(string(logBytes), "parallel fallback") {
		t.Fatalf("expected 'parallel fallback' log line, got:\n%s", string(logBytes))
	}
}

// #22 + #23: ProviderUsage.LastSuccessUTC advances on success and LastError clears.
func TestProviderHealthRoundTrip(t *testing.T) {
	projectDir, outputRoot, cfg := newPipelineFixture(t)
	writeCodexChatFixture(t, projectDir)
	setFakeProviderMode(t, "happy")

	logger := newTestLogger(t, outputRoot)
	if _, err := Run(context.Background(), Options{Config: cfg, ProjectPath: projectDir}, logger); err != nil {
		t.Fatalf("Run: %v", err)
	}
	loaded, err := state.Load(outputRoot, DeriveProjectName(projectDir, nil))
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	got := loaded.ProviderUsage[fakeProviderID]
	if got.Runs != 1 {
		t.Fatalf("Runs = %d, want 1", got.Runs)
	}
	if got.LastSuccessUTC.IsZero() {
		t.Fatalf("LastSuccessUTC must be set on success")
	}
	if got.LastError != "" {
		t.Fatalf("LastError must be empty on success, got %q", got.LastError)
	}
}

// #22: completed categories appear in LastRunPerCategory after a successful run.
func TestPerCategoryTimestampAfterSuccess(t *testing.T) {
	projectDir, outputRoot, cfg := newPipelineFixture(t)
	writeCodexChatFixture(t, projectDir)
	setFakeProviderMode(t, "happy")

	logger := newTestLogger(t, outputRoot)
	if _, err := Run(context.Background(), Options{Config: cfg, ProjectPath: projectDir}, logger); err != nil {
		t.Fatalf("Run: %v", err)
	}
	loaded, err := state.Load(outputRoot, DeriveProjectName(projectDir, nil))
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	if _, ok := loaded.LastRunPerCategory["test"]; !ok {
		t.Fatalf("LastRunPerCategory missing 'test' entry; got %v", loaded.LastRunPerCategory)
	}
}
