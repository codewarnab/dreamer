package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"

	"gopkg.in/yaml.v3"
)

func TestRenderCommentedConfig_ListsAllRegisteredProviders(t *testing.T) {
	out, err := renderCommentedConfig(setupAnswers{})
	if err != nil {
		t.Fatalf("renderCommentedConfig: %v", err)
	}
	rendered := string(out)
	for _, m := range analyzer.RegisteredProviderMeta() {
		// The id must appear as a real providers:<id> block, not merely in
		// the "# Options:" comment — a comment-only match is how the
		// missing opencode/codebuff blocks slipped through (bug: --model
		// was silently dropped for providers without a template block).
		if !strings.Contains(rendered, "  "+string(m.ID)+":") {
			t.Errorf("rendered config template missing providers block for id %q", m.ID)
		}
	}
}

// TestRenderCommentedConfig_ModelRoundTripsForEveryProvider guards against a
// regression where `setup --model` is silently discarded because the selected
// provider has no block in the config template. For every registered provider
// id the rendered YAML must carry the user's chosen model under
// providers.<id>.model.
func TestRenderCommentedConfig_ModelRoundTripsForEveryProvider(t *testing.T) {
	const sentinelModel = "test-sentinel/model-x"
	for _, meta := range analyzer.RegisteredProviderMeta() {
		id := string(meta.ID)
		t.Run(id, func(t *testing.T) {
			out, err := renderCommentedConfig(setupAnswers{
				provider: id,
				model:    sentinelModel,
			})
			if err != nil {
				t.Fatalf("renderCommentedConfig(%s): %v", id, err)
			}
			var parsed config.App
			if err := yaml.Unmarshal(out, &parsed); err != nil {
				t.Fatalf("parse rendered config for %s: %v\n%s", id, err, out)
			}
			block, ok := parsed.Providers[id]
			if !ok {
				t.Fatalf("rendered config has no providers.%s block:\n%s", id, out)
			}
			if block.Model != sentinelModel {
				t.Errorf("providers.%s.model = %q, want user model %q", id, block.Model, sentinelModel)
			}
			if parsed.DefaultProvider != id {
				t.Errorf("default_provider = %q, want %q", parsed.DefaultProvider, id)
			}
		})
	}
}

func TestRenderCommentedConfig_WithProviderOverride(t *testing.T) {
	out, err := renderCommentedConfig(setupAnswers{
		provider: "codex-cli",
	})
	if err != nil {
		t.Fatalf("renderCommentedConfig: %v", err)
	}
	rendered := string(out)
	if !strings.Contains(rendered, "default_provider: codex-cli") {
		t.Error("rendered config missing selected provider as default_provider")
	}
}

func TestRenderCommentedConfig_Empty(t *testing.T) {
	out, err := renderCommentedConfig(setupAnswers{})
	if err != nil {
		t.Fatalf("renderCommentedConfig empty: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty config template")
	}
}

// TestSetupNonInteractiveWritesSelectedModel covers the full non-interactive
// path on a fresh machine: no <UserConfigDir>/dreamer directory exists yet,
// and the passed --model must land in the written config instead of being
// replaced by the provider default.
func TestSetupNonInteractiveWritesSelectedModel(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	stdout, _, err := executeRootCommand(
		"setup", "--non-interactive",
		"--provider", "opencode-server",
		"--model", "opencode-go/ox-alpha-free",
		"--output-root", filepath.Join(home, "out"),
	)
	if err != nil {
		t.Fatalf("setup non-interactive: %v\nstdout: %s", err, stdout)
	}

	cfgPath := filepath.Join(home, ".config", "dreamer", "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	var parsed config.App
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse written config: %v", err)
	}
	if got := parsed.Providers["opencode-server"].Model; got != "opencode-go/ox-alpha-free" {
		t.Errorf("providers.opencode-server.model = %q, want passed flag value; config:\n%s", got, data)
	}
}

// TestSetupNonInteractiveCreatesMissingConfigDir is the first-run regression:
// WriteFileAtomic requires an existing parent dir, so setup must create
// <UserConfigDir>/dreamer itself.
func TestSetupNonInteractiveCreatesMissingConfigDir(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	if _, _, err := executeRootCommand(
		"setup", "--non-interactive",
		"--provider", "claude-cli",
		"--output-root", filepath.Join(home, "out"),
	); err != nil {
		t.Fatalf("setup non-interactive on fresh machine: %v", err)
	}
	cfgPath := filepath.Join(home, ".config", "dreamer", "config.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config not written to %s: %v", cfgPath, err)
	}
}

// TestSetupNonInteractiveRejectsUnknownProvider ensures a typo'd --provider
// fails at setup time instead of writing a config that fails at runtime.
func TestSetupNonInteractiveRejectsUnknownProvider(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	_, _, err := executeRootCommand(
		"setup", "--non-interactive",
		"--provider", "opencode-servr",
		"--output-root", filepath.Join(home, "out"),
	)
	if err == nil {
		t.Fatal("expected error for unknown provider id")
	}
	if !strings.Contains(err.Error(), "opencode-servr") || !strings.Contains(err.Error(), "opencode-server") {
		t.Errorf("error should name the bad id and suggest known ones, got: %v", err)
	}
}
