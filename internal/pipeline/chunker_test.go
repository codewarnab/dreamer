package pipeline

import (
	"strconv"
	"strings"
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
)

// blockOf builds a ProviderBlock with `n` identical messages of `msgBytes` each.
// Header is "tool: <tool>\n\n" (~12 bytes).
func blockOf(tool string, msgBytes, n int) ProviderBlock {
	header := "tool: " + tool + "\n\n"
	msg := strings.Repeat("x", msgBytes-1) + "\n"
	messages := make([]string, n)
	for i := range messages {
		messages[i] = msg
	}
	return ProviderBlock{
		Tool:     tool,
		Sources:  []string{tool + ".jsonl"},
		Header:   header,
		Messages: messages,
	}
}

func TestPackChunksDisabledProducesSingleChunk(t *testing.T) {
	blocks := []ProviderBlock{blockOf("claude", 100, 50), blockOf("codex", 100, 50)}
	cfg := config.ChunkingConfig{MaxChunkBytes: 0}
	chunks, warns := PackChunks(blocks, cfg, "lifetime")
	if got, want := len(chunks), 1; got != want {
		t.Fatalf("len(chunks) = %d, want %d", got, want)
	}
	if len(warns) != 0 {
		t.Fatalf("expected no warnings, got %v", warns)
	}
	if chunks[0].Split {
		t.Fatalf("disabled mode chunks must not be marked Split")
	}
	if got := chunks[0].SourceLabels; len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Fatalf("SourceLabels = %v, want [claude codex]", got)
	}
}

func TestPackChunksPacksUnderHeadroom(t *testing.T) {
	// budget=100000, headroom=0.20 -> fillCap=80000.
	blocks := []ProviderBlock{
		{Tool: "claude", Header: "", Messages: []string{strings.Repeat("a", 70000)}},
		{Tool: "codex", Header: "", Messages: []string{strings.Repeat("c", 8000)}},
		{Tool: "copilot", Header: "", Messages: []string{strings.Repeat("p", 12000)}},
		{Tool: "vscode", Header: "", Messages: []string{strings.Repeat("v", 2000)}},
	}
	cfg := config.ChunkingConfig{MaxChunkBytes: 100000, ProviderBoundaryHeadroom: 0.20}

	chunks, warns := PackChunks(blocks, cfg, "24h")
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if got, want := len(chunks), 2; got != want {
		t.Fatalf("len(chunks) = %d, want %d (chunks=%+v)", got, want, summarizeChunks(chunks))
	}
	if labels := chunks[0].SourceLabels; len(labels) != 2 || labels[0] != "claude" || labels[1] != "codex" {
		t.Fatalf("chunk0 labels = %v, want [claude codex]", labels)
	}
	if labels := chunks[1].SourceLabels; len(labels) != 2 || labels[0] != "copilot" || labels[1] != "vscode" {
		t.Fatalf("chunk1 labels = %v, want [copilot vscode]", labels)
	}
}

func TestPackChunksByteBudget3Blocks(t *testing.T) {
	// p1 alone, p2+p3 packed: tests the seal-and-restart path.
	blocks := []ProviderBlock{
		{Tool: "p1", Messages: []string{strings.Repeat("a", 120000)}},
		{Tool: "p2", Messages: []string{strings.Repeat("b", 80000)}},
		{Tool: "p3", Messages: []string{strings.Repeat("c", 50000)}},
	}
	cfg := config.ChunkingConfig{MaxChunkBytes: 130000, ProviderBoundaryHeadroom: 0}
	chunks, warns := PackChunks(blocks, cfg, "24h")
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if got, want := len(chunks), 2; got != want {
		t.Fatalf("len(chunks) = %d, want %d (chunks=%+v)", got, want, summarizeChunks(chunks))
	}
	if labels := chunks[0].SourceLabels; len(labels) != 1 || labels[0] != "p1" {
		t.Fatalf("chunk0 labels = %v, want [p1]", labels)
	}
	if labels := chunks[1].SourceLabels; len(labels) != 2 || labels[0] != "p2" || labels[1] != "p3" {
		t.Fatalf("chunk1 labels = %v, want [p2 p3]", labels)
	}
}

func TestPackChunksOversizeHardSplit(t *testing.T) {
	// 3 messages ~85000 each, budget=100000 -> 3 Split=true chunks.
	blocks := []ProviderBlock{{
		Tool: "claude",
		Messages: []string{
			strings.Repeat("a", 85000),
			strings.Repeat("b", 85000),
			strings.Repeat("c", 80000),
		},
	}}
	cfg := config.ChunkingConfig{MaxChunkBytes: 100000}
	chunks, warns := PackChunks(blocks, cfg, "lifetime")
	if got, want := len(chunks), 3; got != want {
		t.Fatalf("len(chunks) = %d, want %d", got, want)
	}
	for i, c := range chunks {
		if !c.Split {
			t.Fatalf("chunk[%d].Split = false, want true", i)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("want 1 warning, got %d: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0], "consider tightening since (current: lifetime)") {
		t.Fatalf("warning %q must mention lifetime since", warns[0])
	}
}

// Hard-split with non-empty Header: every emitted chunk must carry content
// beyond the header. No header-only chunks; warning count == 1.
func TestPackChunksOversizeHardSplitWithHeader(t *testing.T) {
	header := "tool: claude\nsources: a.jsonl, b.jsonl\n\n"
	blocks := []ProviderBlock{{
		Tool:   "claude",
		Header: header,
		Messages: []string{
			strings.Repeat("a", 85000),
			strings.Repeat("b", 85000),
			strings.Repeat("c", 80000),
		},
	}}
	cfg := config.ChunkingConfig{MaxChunkBytes: 100000}
	chunks, warns := PackChunks(blocks, cfg, "lifetime")
	if got, want := len(chunks), 3; got != want {
		t.Fatalf("len(chunks) = %d, want %d (summary=%s)", got, want, summarizeChunks(chunks))
	}
	if got, want := len(warns), 1; got != want {
		t.Fatalf("len(warns) = %d, want %d", got, want)
	}
	for i, c := range chunks {
		if !c.Split {
			t.Fatalf("chunk[%d].Split = false, want true", i)
		}
		if c.Bytes <= len(header) {
			t.Fatalf("chunk[%d] is header-only (bytes=%d, header=%d)", i, c.Bytes, len(header))
		}
		if !strings.HasPrefix(c.Transcript, header) {
			t.Fatalf("chunk[%d] missing header prefix", i)
		}
	}
}

func TestPackChunksOversizeHardSplitDefaultsSince(t *testing.T) {
	blocks := []ProviderBlock{{
		Tool:     "claude",
		Messages: []string{strings.Repeat("a", 200000)},
	}}
	cfg := config.ChunkingConfig{MaxChunkBytes: 100000}
	_, warns := PackChunks(blocks, cfg, "")
	if len(warns) != 1 {
		t.Fatalf("want 1 warning, got %d: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0], "(current: 24h)") {
		t.Fatalf("warning %q must default to 24h", warns[0])
	}
}

func TestPackChunksDeterministic(t *testing.T) {
	blocks := []ProviderBlock{
		blockOf("claude", 1000, 50),
		blockOf("codex", 800, 60),
		blockOf("copilot", 1500, 30),
	}
	cfg := config.ChunkingConfig{MaxChunkBytes: 70000, ProviderBoundaryHeadroom: 0.15}

	c1, _ := PackChunks(blocks, cfg, "24h")
	c2, _ := PackChunks(blocks, cfg, "24h")
	if len(c1) != len(c2) {
		t.Fatalf("non-deterministic chunk count: %d vs %d", len(c1), len(c2))
	}
	for i := range c1 {
		if c1[i].Transcript != c2[i].Transcript {
			t.Fatalf("chunk %d transcript differs across runs", i)
		}
		if c1[i].Bytes != c2[i].Bytes {
			t.Fatalf("chunk %d bytes differ across runs", i)
		}
	}
}

func summarizeChunks(chunks []analyzer.Chunk) string {
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString("[")
		sb.WriteString(strings.Join(c.SourceLabels, ","))
		sb.WriteString(" idx=")
		sb.WriteString(strconv.Itoa(c.Index))
		sb.WriteString(" bytes=")
		sb.WriteString(strconv.Itoa(c.Bytes))
		sb.WriteString("] ")
	}
	return sb.String()
}
