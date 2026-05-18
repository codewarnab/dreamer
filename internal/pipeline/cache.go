package pipeline

import (
	"path/filepath"
	"sort"
	"strings"

	"dreamer/internal/analyzer"
	"dreamer/internal/chat"
	"dreamer/internal/logging"
	"dreamer/internal/state"
)

// cacheKeyStats counts the per-run outcomes from computeCacheKeys.
type cacheKeyStats struct {
	Cached       int
	Changed      int
	Fresh        int
	HashFailures int
}

// computeCacheKeys builds the per-source cache-key map for this run. When a
// source is present but its file hash fails, the prior key is preserved so
// state.ChatHashes is not clobbered on assignment (B1). Sources absent from
// `sources` are intentionally dropped, pruning stale entries (B26).
func computeCacheKeys(sources []chat.ChatSource, prior map[string]string, repoHeadSHA string, logger *logging.Logger) (map[string]string, cacheKeyStats) {
	out := make(map[string]string, len(sources))
	stats := cacheKeyStats{}
	for _, src := range sources {
		fileHash, err := state.HashFile(src.Path)
		if err != nil {
			stats.HashFailures++
			if logger != nil {
				logger.Warn("hash chat source failed", logging.Any("path", src.Path), logging.Any("err", err))
			}
			if priorKey, ok := prior[src.Path]; ok {
				out[src.Path] = priorKey
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
