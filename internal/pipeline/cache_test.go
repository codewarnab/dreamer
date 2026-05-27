package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/chat"
	"dreamer/internal/state"
)

func priorRunTime() time.Time {
	return time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
}

func TestCacheUnchangedReturnsTrueForMatchingHashesAndRepo(t *testing.T) {
	current := &state.State{
		LastRunUTC:  priorRunTime(),
		RepoHeadSHA: "abc123",
		ChatHashes: map[string]string{
			"a.jsonl": "hash-a",
			"b.jsonl": "hash-b",
		},
	}

	cacheKeys := map[string]string{
		"a.jsonl": "hash-a",
		"b.jsonl": "hash-b",
	}

	if !cacheUnchanged(current, cacheKeys, "abc123") {
		t.Fatalf("cacheUnchanged returned false for matching cache keys")
	}
}

func TestCacheUnchangedReturnsFalseForHashDrift(t *testing.T) {
	current := &state.State{
		LastRunUTC:  priorRunTime(),
		RepoHeadSHA: "abc123",
		ChatHashes:  map[string]string{"a.jsonl": "old-hash"},
	}

	if cacheUnchanged(current, map[string]string{"a.jsonl": "new-hash"}, "abc123") {
		t.Fatalf("cacheUnchanged returned true when source hash changed")
	}
}

func TestCacheUnchangedReturnsFalseForRepoDrift(t *testing.T) {
	current := &state.State{
		LastRunUTC:  priorRunTime(),
		RepoHeadSHA: "abc123",
		ChatHashes:  map[string]string{"a.jsonl": "hash-a"},
	}

	if cacheUnchanged(current, map[string]string{"a.jsonl": "hash-a"}, "def456") {
		t.Fatalf("cacheUnchanged returned true when repo SHA changed")
	}
}

func TestCacheUnchangedReturnsFalseWhenLastRunUTCZero(t *testing.T) {
	// A freshly-loaded default state (zero LastRunUTC) must never be
	// treated as a cache hit, even if both maps are empty.
	current := &state.State{ChatHashes: map[string]string{}}
	if cacheUnchanged(current, map[string]string{}, "") {
		t.Fatalf("zero LastRunUTC must defeat the cache check")
	}
}

func TestCacheUnchangedTrueForEmptyVsEmptyAfterPriorRun(t *testing.T) {
	// §7.10/§7.11 acceptance: an empty source folder we have observed
	// before is a legitimate cache hit. Without this, preflight skip
	// re-walks discovery on every subsequent daemon tick.
	current := &state.State{
		LastRunUTC:  priorRunTime(),
		RepoHeadSHA: "abc123",
		ChatHashes:  map[string]string{},
	}
	if !cacheUnchanged(current, map[string]string{}, "abc123") {
		t.Fatalf("empty-vs-empty after a prior run must hit the cache")
	}
}

// B1: a chat file that fails to hash this run must keep its prior cache key
// rather than being silently dropped from state.ChatHashes on assignment.
func TestComputeCacheKeysPreservesPriorOnHashFailure(t *testing.T) {
	dir := t.TempDir()
	okPath := filepath.Join(dir, "ok.jsonl")
	if err := os.WriteFile(okPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed ok: %v", err)
	}
	badPath := filepath.Join(dir, "fail-as-dir")
	if err := os.Mkdir(badPath, 0o755); err != nil {
		t.Fatalf("seed bad: %v", err)
	}

	prior := map[string]string{
		badPath: "prior-key",
		okPath:  "old-ok",
	}
	sources := []chat.Source{
		{Path: okPath, Tool: chat.SourceTypeCodexSessionJSONL},
		{Path: badPath, Tool: chat.SourceTypeCodexSessionJSONL},
	}

	out, stats := computeCacheKeys(sources, prior, "head1", nil)

	if stats.HashFailedKept != 1 {
		t.Fatalf("HashFailedKept = %d, want 1", stats.HashFailedKept)
	}
	if stats.HashFailedDropped != 0 {
		t.Fatalf("HashFailedDropped = %d, want 0", stats.HashFailedDropped)
	}
	if out[badPath] != "prior-key" {
		t.Fatalf("hash-failed source lost prior key: got %q, want %q", out[badPath], "prior-key")
	}
	if out[okPath] == "" {
		t.Fatalf("ok source missing cache key after computeCacheKeys")
	}
}

func TestComputeCacheKeysDropsHashFailureWithNoPrior(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "fresh-but-broken")
	if err := os.Mkdir(badPath, 0o755); err != nil {
		t.Fatalf("seed bad: %v", err)
	}
	sources := []chat.Source{{Path: badPath, Tool: chat.SourceTypeCodexSessionJSONL}}

	out, stats := computeCacheKeys(sources, nil, "head1", nil)

	if stats.HashFailedDropped != 1 || stats.HashFailedKept != 0 {
		t.Fatalf("stats=%+v, want HashFailedDropped=1 HashFailedKept=0", stats)
	}
	if _, ok := out[badPath]; ok {
		t.Fatalf("hash-failed source with no prior must be dropped, got key %q", out[badPath])
	}
}

// B26: chat files that are no longer discovered must be pruned from cache keys
// so state.ChatHashes does not grow indefinitely with stale entries.
func TestComputeCacheKeysPrunesAbsentSources(t *testing.T) {
	dir := t.TempDir()
	okPath := filepath.Join(dir, "ok.jsonl")
	if err := os.WriteFile(okPath, []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed ok: %v", err)
	}

	absentPath := filepath.Join(dir, "absent.jsonl")
	prior := map[string]string{absentPath: "stale-key", okPath: "old"}
	sources := []chat.Source{{Path: okPath, Tool: chat.SourceTypeCodexSessionJSONL}}

	out, _ := computeCacheKeys(sources, prior, "head1", nil)
	if _, ok := out[absentPath]; ok {
		t.Fatalf("absent source should be pruned, got key %q", out[absentPath])
	}
}

// B6: FindingHashes must not grow without bound. Beyond MaxFindingHashes
// the oldest entries are dropped (insertion-order rolling window).
func TestMergeHashListsCapsAtRollingWindow(t *testing.T) {
	base := make([]string, MaxFindingHashes)
	for i := range base {
		base[i] = "base-hash-" + intToStr(i)
	}
	addition := []string{"new-hash-a", "new-hash-b"}

	out := mergeHashLists(base, addition)
	if len(out) != MaxFindingHashes {
		t.Fatalf("len(out) = %d, want %d", len(out), MaxFindingHashes)
	}
	if out[len(out)-1] != "new-hash-b" {
		t.Fatalf("newest addition not at tail: got %q", out[len(out)-1])
	}
	if out[0] == base[0] {
		t.Fatalf("oldest base hash %q must have been dropped from the window", base[0])
	}
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	var out []byte
	for i > 0 {
		out = append([]byte{byte('0' + i%10)}, out...)
		i /= 10
	}
	return string(out)
}

// B28: keys for categories no longer in the loaded rule pack set must be
// dropped on each Save so the map does not collect cruft over time.
func TestPruneLastRunPerCategoryDropsUnknownKeys(t *testing.T) {
	m := map[string]time.Time{
		"test":         time.Now(),
		"lint-rule":    time.Now(),
		"retired-rule": time.Now(),
	}
	packs := []analyzer.RulePack{
		{Category: analyzer.RuleCategory("test")},
		{Category: analyzer.RuleCategory("lint-rule")},
	}
	pruneLastRunPerCategory(m, packs)
	if _, ok := m["retired-rule"]; ok {
		t.Fatalf("retired-rule must be pruned, got %v", m)
	}
	if _, ok := m["test"]; !ok {
		t.Fatalf("test must survive prune")
	}
	if _, ok := m["lint-rule"]; !ok {
		t.Fatalf("lint-rule must survive prune")
	}
}

// B29: inactive provider ids drop only when their last-success is older than
// ProviderUsageMaxAge. Recent inactive providers must keep their history so
// a user who toggles between two providers across a sprint doesn't lose it.
func TestPruneProviderUsageRespectsTTL(t *testing.T) {
	now := time.Now().UTC()
	m := map[string]state.ProviderUsage{
		"copilot-sdk":     {Runs: 3, LastSuccessUTC: now},
		"recent-inactive": {Runs: 5, LastSuccessUTC: now.Add(-7 * 24 * time.Hour)},
		"stale-inactive":  {Runs: 7, LastSuccessUTC: now.Add(-90 * 24 * time.Hour)},
		"never-succeeded": {Runs: 0, LastError: "boot failed"},
	}
	pruneProviderUsage(m, "copilot-sdk")
	if got := m["copilot-sdk"].Runs; got != 3 {
		t.Fatalf("active provider lost counters: got %d", got)
	}
	if got := m["recent-inactive"].Runs; got != 5 {
		t.Fatalf("recent-inactive within TTL must survive, got Runs=%d", got)
	}
	if _, ok := m["stale-inactive"]; ok {
		t.Fatalf("stale-inactive past TTL must be pruned, got %v", m)
	}
	if _, ok := m["never-succeeded"]; ok {
		t.Fatalf("never-succeeded inactive stub must be pruned, got %v", m)
	}
}

func TestForceBypassesCacheDecisionInCaller(t *testing.T) {
	current := &state.State{
		LastRunUTC:  priorRunTime(),
		RepoHeadSHA: "abc123",
		ChatHashes:  map[string]string{"a.jsonl": "hash-a"},
	}
	cacheKeys := map[string]string{"a.jsonl": "hash-a"}

	force := true
	shouldSkip := !force && cacheUnchanged(current, cacheKeys, "abc123")
	if shouldSkip {
		t.Fatalf("force should bypass cache hit")
	}
}

func TestRecordFindingApplySpecsPersistsServerTrustedSpec(t *testing.T) {
	st := &state.State{Findings: map[string]state.FindingState{}}
	findings := []analyzer.Finding{
		{
			Hash:     "AABBCCDD",
			Category: "doc",
			Guardrail: analyzer.Guardrail{
				Apply: &analyzer.ApplySpec{
					TargetFile: "CLAUDE.md",
					Strategy:   "append-file",
					Snippet:    "rule",
				},
			},
		},
		// Finding without an Apply object: no spec recorded.
		{
			Hash:     "ee11",
			Category: "perf",
		},
	}
	recordFindingApplySpecs(st, findings, "proj-a")

	got, ok := st.Findings["aabbccdd"]
	if !ok {
		t.Fatalf("hash not lower-cased into Findings map: %+v", st.Findings)
	}
	if got.ApplySpec == nil {
		t.Fatalf("ApplySpec not recorded")
	}
	if got.ApplySpec.TargetFile != "CLAUDE.md" || got.ApplySpec.Strategy != "append-file" || got.ApplySpec.Category != "doc" {
		t.Errorf("ApplySpec mismatch: %+v", got.ApplySpec)
	}
	if got.ProjectName != "proj-a" {
		t.Errorf("ProjectName=%q want proj-a", got.ProjectName)
	}
	if _, ok := st.Findings["ee11"]; ok {
		t.Errorf("finding without Apply object should not create entry")
	}
}

func TestRecordFindingApplySpecsPreservesLifecycleFields(t *testing.T) {
	hash := "aabbccdd"
	prior := state.FindingState{
		Status:      state.FindingStatusDismissed,
		DismissedAt: time.Now().UTC(),
		ProjectName: "proj-a",
	}
	st := &state.State{Findings: map[string]state.FindingState{hash: prior}}
	findings := []analyzer.Finding{{
		Hash:     hash,
		Category: "doc",
		Guardrail: analyzer.Guardrail{Apply: &analyzer.ApplySpec{
			TargetFile: "CLAUDE.md", Strategy: "append-file", Snippet: "rule",
		}},
	}}
	recordFindingApplySpecs(st, findings, "proj-a")
	got := st.Findings[hash]
	if got.Status != state.FindingStatusDismissed {
		t.Errorf("Status clobbered: %q", got.Status)
	}
	if got.DismissedAt.IsZero() {
		t.Errorf("DismissedAt cleared")
	}
	if got.ApplySpec == nil || got.ApplySpec.TargetFile != "CLAUDE.md" {
		t.Errorf("ApplySpec missing: %+v", got.ApplySpec)
	}
}
