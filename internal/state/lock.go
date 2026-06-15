package state

import "sync"

// ProjectLock serializes state Load→Mutate→Save cycles per project name.
//
// # Why this exists
//
// state.json is the single mutable document shared between two concurrent
// writers that live in the same process:
//
//  1. The pipeline (pipeline.Run) — loads state at the start of an analysis
//     run that can take minutes to hours, computes new findings/hashes, then
//     saves at the end.
//  2. Web lifecycle handlers (Apply, Undo, Dismiss, Resolve, …) — load,
//     mutate a single finding entry, and save immediately.
//
// Without a shared lock the two subsystems race on the classic lost-update
// pattern:
//
//	Pipeline goroutine                 Web handler goroutine
//	──────────────────────────────     ──────────────────────────────────────
//	state.Load()  → snapshot S₀
//	  [LLM call — minutes pass]
//	                                   Lock("proj-x")
//	                                   state.Load() → S₀
//	                                   finding["abc"].Status = "dismissed"
//	                                   state.Save() → writes S₁
//	                                   Unlock()
//	state.Save() → writes S₂ (S₀+results)
//	                                   ← S₁ is gone; the dismiss is lost
//
// The loss is silent: state.Save uses an atomic rename so there is no torn
// write, just a clean clobber of the web handler's mutation.
//
// # Design
//
// ProjectLock lives in the state package (not in web/handlers) so that both
// the pipeline package and the web handler package can import it without
// creating an import cycle.  A single *ProjectLock instance is created at
// daemon startup and threaded through pipeline.Options and handlers.Deps.
//
// The pipeline does NOT hold the lock for the duration of analysis — that
// would block all web mutations for the entire run duration.  Instead, each
// save site (savePrunedState / persistFailureState) acquires the lock only
// for the narrow re-load → merge → save window at the very end.
//
// The web handlers continue to hold the lock across their own Load→Mutate→Save
// window (unchanged) because they are short-lived HTTP requests.
//
// # Field ownership
//
// To merge correctly the pipeline must know which fields it owns and which
// fields the web handlers own.  The split is:
//
//   - Pipeline-owned: LastRunUTC, RepoHeadSHA, ChatHashes, FindingHashes,
//     FindingApplySpecs (embedded in Findings entries), LastRunPerCategory,
//     ProviderUsage, UsageStats, CachedPhase1.
//
//   - Web-owned: Findings[*].Status, Findings[*].AppliedAt,
//     Findings[*].AppliedReversal, Findings[*].DismissedAt,
//     Findings[*].ResolvedAt.
//
// The merge strategy is: re-read the latest on-disk state inside the lock,
// then overwrite only pipeline-owned fields, leaving web-owned fields
// (the Findings lifecycle map) intact.  ApplySpecs are additive: the
// pipeline writes new specs for new findings; it never removes existing ones.
type ProjectLock struct {
	mu    sync.Mutex
	locks map[string]*projectEntry
}

// projectEntry wraps a per-project mutex.
type projectEntry struct {
	mu sync.Mutex
}

// NewProjectLock creates a ProjectLock ready for use.
func NewProjectLock() *ProjectLock {
	return &ProjectLock{locks: make(map[string]*projectEntry)}
}

// Lock acquires the per-project mutex and returns a single-shot release
// function.  Always defer the returned closure immediately after calling Lock.
//
// The outer pl.mu is held only for the map check-and-insert, so there is no
// TOCTOU window and the per-project lock is never held while pl.mu is held.
func (pl *ProjectLock) Lock(projectName string) func() {
	pl.mu.Lock()
	entry, ok := pl.locks[projectName]
	if !ok {
		entry = &projectEntry{}
		pl.locks[projectName] = entry
	}
	pl.mu.Unlock()

	entry.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(entry.mu.Unlock)
	}
}
