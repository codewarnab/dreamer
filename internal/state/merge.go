package state

// MergePipelineResult re-reads the latest on-disk state for a project
// inside the caller-supplied lock, then merges pipeline-owned fields from
// pipelineState into the freshly-loaded state, and saves the result.
//
// # Why a merge instead of a plain Save
//
// The pipeline holds its working copy of State from the beginning of the
// analysis run, which can span minutes to hours.  During that window, web
// lifecycle handlers (Apply, Undo, Dismiss, …) may legally mutate the
// on-disk state.json via their own Load→Mutate→Save cycle, each guarded by
// the same ProjectLock.  A direct state.Save from the pipeline would
// overwrite those mutations, silently dropping dismissals, applied reversals,
// and undo records — the "lost-update" race described in the ProjectLock
// doc comment.
//
// The fix is to narrow the pipeline's exclusive lock window to the very end
// of the run: re-read what is on disk, overwrite only the fields the
// pipeline owns, leave web-owned fields untouched, then write once.
//
// # Field ownership split
//
//	Pipeline-owned (overwritten from pipelineState):
//	  LastRunUTC, RepoHeadSHA, ChatHashes, FindingHashes,
//	  LastRunPerCategory, ProviderUsage, UsageStats, CachedPhase1
//
//	Web-owned (preserved from the freshly-loaded on-disk state):
//	  Findings[*].Status, Findings[*].AppliedAt,
//	  Findings[*].AppliedReversal, Findings[*].DismissedAt,
//	  Findings[*].ResolvedAt
//
//	Additive (pipeline adds new entries, existing entries are kept):
//	  Findings[*].ApplySpec — the pipeline writes specs for new findings;
//	  it never removes them for findings a web handler has already touched.
//	  FindingHashes — merged (union), not replaced, so hashes added by the
//	  pipeline are appended without losing any that already existed on disk.
//
// # Lock protocol
//
// The caller must NOT hold the ProjectLock when calling MergePipelineResult.
// This function acquires and releases the lock internally so the lock window
// is the smallest possible (Load + merge + Save only).
func MergePipelineResult(outputRoot, projectName string, pipelineState *State, lock *ProjectLock) error {
	unlock := lock.Lock(projectName)
	defer unlock()

	// Re-read whatever is on disk right now.  This is the state that includes
	// any web-handler mutations that happened during the analysis run.
	onDisk, err := Load(outputRoot, projectName)
	if err != nil {
		// If the file does not exist yet (first run), Load returns a default
		// state — that is fine, we just merge into the zero base.
		return err
	}

	// --- Pipeline-owned fields: copy from pipelineState ---
	onDisk.LastRunUTC = pipelineState.LastRunUTC
	onDisk.RepoHeadSHA = pipelineState.RepoHeadSHA
	onDisk.ChatHashes = pipelineState.ChatHashes
	onDisk.LastRunPerCategory = pipelineState.LastRunPerCategory
	onDisk.ProviderUsage = pipelineState.ProviderUsage
	onDisk.UsageStats = pipelineState.UsageStats
	onDisk.CachedPhase1 = pipelineState.CachedPhase1

	// FindingHashes: union — the pipeline adds new content-hashes for newly
	// discovered findings; any hashes that exist only on-disk (e.g. added by
	// a concurrent run or a manual repair) are preserved.
	onDisk.FindingHashes = mergeStringSliceUnion(onDisk.FindingHashes, pipelineState.FindingHashes)

	// Findings map: additive ApplySpec merge.
	//
	// For each finding the pipeline knows about:
	//   • If the on-disk entry already exists — preserve all web-owned fields
	//     (Status, AppliedAt, AppliedReversal, DismissedAt, ResolvedAt) and
	//     only update ApplySpec if the on-disk entry does not already have
	//     one (avoids overwriting a spec that a future web feature may have
	//     enriched).
	//   • If the on-disk entry is absent — write the full pipeline entry
	//     (Status will be empty / open, which is correct for a new finding).
	if onDisk.Findings == nil {
		onDisk.Findings = make(map[string]FindingState)
	}
	for hash, pipelineEntry := range pipelineState.Findings {
		existing, exists := onDisk.Findings[hash]
		if !exists {
			// Brand-new finding — write the pipeline's entry wholesale.
			onDisk.Findings[hash] = pipelineEntry
		} else if existing.ApplySpec == nil && pipelineEntry.ApplySpec != nil {
			// Existing entry has no spec yet — fill it in without touching
			// any web-owned lifecycle fields.
			existing.ApplySpec = pipelineEntry.ApplySpec
			onDisk.Findings[hash] = existing
		}
		// Otherwise: on-disk entry already has a spec; leave it and all
		// lifecycle fields intact.
	}

	return Save(outputRoot, projectName, onDisk)
}

// mergeStringSliceUnion returns the union of two string slices as a new
// slice with no duplicates.  Order is: all elements of base first, then any
// elements from extra that were not already in base.
func mergeStringSliceUnion(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	result := make([]string, 0, len(base)+len(extra))
	for _, s := range base {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	for _, s := range extra {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}
