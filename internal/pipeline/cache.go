package pipeline

import (
	"path/filepath"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/chat"
	"dreamer/internal/logging"
	"dreamer/internal/state"
)

// cacheKeyStats counts the per-run outcomes from computeCacheKeys.
// The five fields are disjoint, so Cached+Changed+Fresh+HashFailedKept+
// HashFailedDropped == len(sources). HashFailedKept entries are also written
// to the returned cache-key map (preserving prior evidence); HashFailedDropped
// entries are not in the map (no prior to keep).
type cacheKeyStats struct {
	Cached            int
	Changed           int
	Fresh             int
	HashFailedKept    int
	HashFailedDropped int
}

// computeCacheKeys builds the per-source cache-key map for this run. When a
// source is present but its file hash fails, the prior key is preserved so
// state.ChatHashes is not clobbered on assignment (B1). Sources absent from
// `sources` are intentionally dropped, pruning stale entries (B26).
func computeCacheKeys(sources []chat.Source, prior map[string]string, repoHeadSHA string, logger *logging.Logger) (map[string]string, cacheKeyStats) {
	out := make(map[string]string, len(sources))
	stats := cacheKeyStats{}
	for _, src := range sources {
		fileHash, err := state.HashFile(src.Path)
		if err != nil {
			if logger != nil {
				logger.Warn("hash chat source failed", logging.Any("path", src.Path), logging.Any("err", err))
			}
			if priorKey, ok := prior[src.Path]; ok {
				out[src.Path] = priorKey
				stats.HashFailedKept++
			} else {
				stats.HashFailedDropped++
			}
			continue
		}
		key := state.ChatCacheKey(src.Path, fileHash, repoHeadSHA)
		out[src.Path] = key
		existing, seen := prior[src.Path]
		switch {
		case !seen:
			stats.Fresh++
		case existing != key:
			stats.Changed++
		default:
			stats.Cached++
		}
	}
	return out, stats
}

// cacheUnchanged: prior successful run covered the exact same source set + repo head.
// Empty-vs-empty after a real prior run is a legitimate hit (lets preflight pay off).
func cacheUnchanged(currentState *state.State, cacheKeys map[string]string, repoHeadSHA string) bool {
	if currentState == nil {
		return false
	}
	if currentState.LastRunUTC.IsZero() {
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

// recordFindingApplySpecs persists each emitted finding's apply plan
// into state.Findings[hash].ApplySpec so the web UI's Apply handler can
// look it up by hash instead of trusting client-supplied fields. Only
// findings with a non-nil Guardrail.Apply contribute a spec; existing
// entries (dismissed/resolved/applied) are preserved with their lifecycle
// state, and ApplySpec is overwritten with the latest analyzer output.
func recordFindingApplySpecs(st *state.State, findings []analyzer.Finding, projectName string) {
	if st == nil {
		return
	}
	if st.Findings == nil {
		st.Findings = map[string]state.FindingState{}
	}
	for _, f := range findings {
		hash := strings.ToLower(strings.TrimSpace(f.Hash))
		if hash == "" || f.Guardrail.Apply == nil {
			continue
		}
		spec := &state.FindingApplySpec{
			Category:   string(f.Category),
			TargetFile: f.Guardrail.Apply.TargetFile,
			Strategy:   f.Guardrail.Apply.Strategy,
			Anchor:     f.Guardrail.Apply.Anchor,
			Snippet:    f.Guardrail.Apply.Snippet,
		}
		findingState := st.Findings[hash]
		findingState.ApplySpec = spec
		if findingState.ProjectName == "" {
			findingState.ProjectName = projectName
		}
		st.Findings[hash] = findingState
	}
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

// MaxFindingHashes caps the rolling de-dup window for state.FindingHashes.
// Beyond this many entries the oldest are dropped (B6). This keeps state.json
// from growing without bound on long-lived projects; the trade-off is that
// findings older than the window can re-surface if the model re-emits them.
const MaxFindingHashes = 5000

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
	if len(merged) > MaxFindingHashes {
		merged = merged[len(merged)-MaxFindingHashes:]
	}
	return merged
}

func stringSliceToSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}

// pruneLastRunPerCategory drops keys that don't belong to any pack in the
// current rule set (B28). Disabled packs still count as "known" so toggling
// a category off temporarily doesn't lose its history.
func pruneLastRunPerCategory(m map[string]time.Time, packs []analyzer.RulePack) {
	if len(m) == 0 {
		return
	}
	known := make(map[string]struct{}, len(packs))
	for _, p := range packs {
		known[string(p.Category)] = struct{}{}
	}
	for k := range m {
		if _, ok := known[k]; !ok {
			delete(m, k)
		}
	}
}

// ProviderUsageMaxAge bounds how long a provider's counters survive after
// its last successful run. The active provider is always kept regardless.
// 30 days lets a user toggle between two providers across a sprint without
// losing the inactive one's history (B29).
const ProviderUsageMaxAge = 30 * 24 * time.Hour

// pruneProviderUsage drops counters for inactive provider ids whose last
// successful run is older than ProviderUsageMaxAge (B29). Per spec, the
// active provider is always retained, and entries that never reported a
// success (zero LastSuccessUTC) are dropped immediately when inactive —
// they're stubs from a failed bootstrap.
func pruneProviderUsage(m map[string]state.ProviderUsage, activeProviderID string) {
	if len(m) == 0 {
		return
	}
	now := time.Now().UTC()
	for id, usage := range m {
		if id == activeProviderID {
			continue
		}
		if usage.LastSuccessUTC.IsZero() {
			delete(m, id)
			continue
		}
		if now.Sub(usage.LastSuccessUTC) > ProviderUsageMaxAge {
			delete(m, id)
		}
	}
}

func todosOutputPath(outputRoot string, projectName string) string {
	root := strings.TrimSpace(outputRoot)
	if root == "" {
		return ""
	}
	return filepath.Join(root, projectName, "todos.md")
}
