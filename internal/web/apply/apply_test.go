package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestApply_AppendSection_AppendsWhenAnchorMissing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")
	original := "# Doc\n\nIntro.\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rev, err := Apply(ApplyRequest{
		ProjectRoot: dir, TargetFile: "CLAUDE.md",
		Strategy: "append-section", Anchor: "Cache", Snippet: "Rules for cache.",
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rev.Strategy != "append-section" {
		t.Fatalf("rev.Strategy = %q", rev.Strategy)
	}
	got, _ := os.ReadFile(target)
	want := original + "\n\n## Cache\n\nRules for cache.\n"
	if string(got) != want {
		t.Fatalf("file =\n%q\nwant\n%q", got, want)
	}
	if rev.PreImageSHA256 != sha([]byte(original)) {
		t.Fatalf("pre SHA mismatch")
	}
	if rev.PostImageSHA256 != sha(got) {
		t.Fatalf("post SHA mismatch")
	}
}

func TestApply_AppendSection_PromotesToReplaceWhenAnchorExists(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")
	original := "# Doc\n\n## Cache\n\nOld rules.\n\n## After\n\nz\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rev, err := Apply(ApplyRequest{
		ProjectRoot: dir, TargetFile: "CLAUDE.md",
		Strategy: "append-section", Anchor: "Cache", Snippet: "Rules for cache.",
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rev.Strategy != "replace-section" {
		t.Fatalf("rev.Strategy = %q, want replace-section (auto-promoted)", rev.Strategy)
	}
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "## Cache\n\nRules for cache.") {
		t.Fatalf("missing replaced section, got:\n%s", got)
	}
	if strings.Contains(string(got), "Old rules.") {
		t.Fatalf("old section content survived: %s", got)
	}
	if !strings.Contains(string(got), "## After") {
		t.Fatalf("subsequent section dropped: %s", got)
	}
}

func TestApply_RejectsTargetOutsideProjectRoot(t *testing.T) {
	dir := t.TempDir()
	_, err := Apply(ApplyRequest{
		ProjectRoot: dir, TargetFile: "../escape.md",
		Strategy: "append-file", Snippet: "x",
	})
	if err == nil {
		t.Fatalf("expected containment rejection")
	}
	if !strings.Contains(err.Error(), "containment") && !strings.Contains(err.Error(), "outside project root") {
		t.Fatalf("error %q should mention containment", err)
	}
}

func TestApply_RejectsOversize(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "big.txt")
	big := make([]byte, MaxApplyTargetBytes+1)
	if err := os.WriteFile(target, big, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := Apply(ApplyRequest{
		ProjectRoot: dir, TargetFile: "big.txt",
		Strategy: "append-file", Snippet: "x",
	})
	if !IsTargetTooLarge(err) {
		t.Fatalf("err = %v, want IsTargetTooLarge", err)
	}
}

func TestUndo_RestoresPreImageWhenPostSHAUnchanged(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.md")
	original := "hello\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rev, err := Apply(ApplyRequest{ProjectRoot: dir, TargetFile: "x.md", Strategy: "append-file", Snippet: "world\n"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := Undo(dir, *rev); err != nil {
		t.Fatalf("undo: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != original {
		t.Fatalf("undo did not restore: got %q", got)
	}
}

func TestUndo_RefusesWhenTargetModifiedExternally(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.md")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rev, err := Apply(ApplyRequest{ProjectRoot: dir, TargetFile: "x.md", Strategy: "append-file", Snippet: "world\n"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Simulate operator edit.
	if err := os.WriteFile(target, []byte("operator edit\n"), 0o644); err != nil {
		t.Fatalf("operator edit: %v", err)
	}
	err = Undo(dir, *rev)
	if !IsTargetChanged(err) {
		t.Fatalf("err = %v, want IsTargetChanged", err)
	}
}
