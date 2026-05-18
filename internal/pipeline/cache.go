package pipeline

import (
	"path/filepath"
	"sort"
	"strings"

	"dreamer/internal/analyzer"
	"dreamer/internal/state"
)

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
