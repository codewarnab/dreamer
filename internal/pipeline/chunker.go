package pipeline

import (
	"fmt"
	"strings"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
)

// ProviderBlock is one tool's transcript: a header plus one entry per message.
// Messages are atomic units for hard-splitting; the chunker never splits mid-message.
type ProviderBlock struct {
	Tool     string
	Sources  []string
	Header   string
	Messages []string
}

// Bytes returns Header + sum(message bytes).
func (b ProviderBlock) Bytes() int {
	total := len(b.Header)
	for _, m := range b.Messages {
		total += len(m)
	}
	return total
}

// Text concatenates Header and all messages in order.
func (b ProviderBlock) Text() string {
	var sb strings.Builder
	sb.Grow(b.Bytes())
	sb.WriteString(b.Header)
	for _, m := range b.Messages {
		sb.WriteString(m)
	}
	return sb.String()
}

// PackChunks groups provider blocks into chunks bounded by cfg.MaxChunkBytes.
// cfg.MaxChunkBytes == 0 disables chunking (single chunk). Oversize blocks
// hard-split by message boundary; sinceLabel goes into the warning string.
func PackChunks(blocks []ProviderBlock, cfg config.ChunkingConfig, sinceLabel string) ([]analyzer.Chunk, []string) {
	if len(blocks) == 0 {
		return nil, nil
	}

	if cfg.MaxChunkBytes <= 0 {
		return packDisabled(blocks), nil
	}

	headroom := 0.0
	if cfg.ProviderBoundaryHeadroom != nil {
		headroom = *cfg.ProviderBoundaryHeadroom
	}
	if headroom < 0 {
		headroom = 0
	}
	if headroom >= 1 {
		headroom = 0.999
	}
	fillCap := int(float64(cfg.MaxChunkBytes) * (1 - headroom))
	if fillCap <= 0 {
		fillCap = 1
	}

	var (
		chunks   []analyzer.Chunk
		warnings []string
		curBytes int
		curLabel []string
		curText  strings.Builder
	)

	flush := func() {
		if curBytes == 0 {
			return
		}
		chunks = append(chunks, analyzer.Chunk{
			Index:        len(chunks),
			SourceLabels: dedupeLabels(curLabel),
			Transcript:   curText.String(),
			Bytes:        curBytes,
		})
		curBytes = 0
		curLabel = nil
		curText.Reset()
	}

	for _, b := range blocks {
		blockBytes := b.Bytes()
		if blockBytes == 0 {
			continue
		}

		if blockBytes > cfg.MaxChunkBytes {
			flush()
			sub, warn := hardSplit(b, cfg.MaxChunkBytes, len(chunks), sinceLabel)
			chunks = append(chunks, sub...)
			if warn != "" {
				warnings = append(warnings, warn)
			}
			continue
		}

		fits := curBytes+blockBytes <= fillCap
		empty := curBytes == 0
		if !fits && !empty {
			flush()
		}
		curText.WriteString(b.Text())
		curLabel = append(curLabel, b.Tool)
		curBytes += blockBytes
	}
	flush()

	return chunks, warnings
}

// packDisabled is the cfg.MaxChunkBytes==0 path: every block concatenated into chunk[0].
func packDisabled(blocks []ProviderBlock) []analyzer.Chunk {
	var sb strings.Builder
	labels := make([]string, 0, len(blocks))
	total := 0
	for _, b := range blocks {
		sb.WriteString(b.Text())
		labels = append(labels, b.Tool)
		total += b.Bytes()
	}
	return []analyzer.Chunk{{
		Index:        0,
		SourceLabels: dedupeLabels(labels),
		Transcript:   sb.String(),
		Bytes:        total,
	}}
}

// hardSplit packs an oversize provider block into N chunks at message boundaries.
// Each output chunk has Split=true; sinceLabel goes into the returned warning.
func hardSplit(b ProviderBlock, budget int, startIndex int, sinceLabel string) ([]analyzer.Chunk, string) {
	if budget <= 0 {
		return nil, ""
	}
	var chunks []analyzer.Chunk
	var sb strings.Builder
	curBytes := 0
	header := b.Header
	headerBytes := len(header)

	emit := func() {
		// Skip header-only chunks: startChunk pre-writes the header, so a
		// no-content emit produces a wasted provider call.
		if curBytes == 0 || curBytes == headerBytes {
			curBytes = 0
			sb.Reset()
			return
		}
		chunks = append(chunks, analyzer.Chunk{
			Index:        startIndex + len(chunks),
			SourceLabels: []string{b.Tool},
			Transcript:   sb.String(),
			Bytes:        curBytes,
			Split:        true,
		})
		curBytes = 0
		sb.Reset()
	}

	startChunk := func() {
		if headerBytes > 0 {
			sb.WriteString(header)
			curBytes = headerBytes
		}
	}

	startChunk()
	for _, m := range b.Messages {
		mb := len(m)
		if mb == 0 {
			continue
		}
		// Single message bigger than budget: emit it on its own chunk.
		if mb+headerBytes > budget {
			emit()
			sb.WriteString(header)
			sb.WriteString(m)
			curBytes = headerBytes + mb
			emit()
			startChunk()
			continue
		}
		if curBytes+mb > budget {
			emit()
			startChunk()
		}
		sb.WriteString(m)
		curBytes += mb
	}
	emit()

	if strings.TrimSpace(sinceLabel) == "" {
		sinceLabel = config.DefaultSince
	}
	warn := fmt.Sprintf("provider %s hard-split into %d chunks (%d bytes); consider tightening since (current: %s) to reduce input volume", b.Tool, len(chunks), b.Bytes(), sinceLabel)
	return chunks, warn
}

// dedupeLabels preserves first-seen order and removes duplicates.
func dedupeLabels(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
