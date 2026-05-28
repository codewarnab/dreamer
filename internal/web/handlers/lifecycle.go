package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/pipeline"
	"dreamer/internal/state"
	"dreamer/internal/web/apply"
)

// transition enumerates the lifecycle endpoints that share the same path
// shape /api/projects/{name}/findings/{hash}/{transition}.
type transition string

const (
	transitionApply     transition = "apply"
	transitionUndo      transition = "undo"
	transitionDismiss   transition = "dismiss"
	transitionResolve   transition = "resolve"
	transitionUndismiss transition = "undismiss"
	transitionUnresolve transition = "unresolve"
)

// applyRequest is the body the SPA POSTs for the /apply endpoint. The
// fields below are accepted for backward compatibility with older
// clients but are NOT trusted: the server resolves the apply plan from
// state.Findings[hash].ApplySpec, which was recorded by the analyzer
// when the finding was first emitted. A loopback client with the CSRF
// token cannot redirect the write by re-shaping the body.
type applyRequest struct {
	Category   string `json:"category"`
	TargetFile string `json:"target_file"`
	Strategy   string `json:"strategy"`
	Anchor     string `json:"anchor"`
	Snippet    string `json:"snippet"`
}

// parseProjectHashTransition extracts {name}, {hash}, {transition} from
// /api/projects/{name}/findings/{hash}/{transition}. Returns empty
// strings when the path shape is wrong.
func parseProjectHashTransition(urlPath string) (name, hash, transition string) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	// Expect: api, projects, {name}, findings, {hash}, {transition}.
	if len(parts) != 6 || parts[0] != "api" || parts[1] != "projects" || parts[3] != "findings" {
		return "", "", ""
	}
	return parts[2], parts[4], parts[5]
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "internal encoding error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	_, _ = w.Write(encoded)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// publish is a nil-safe wrapper around EventBus.Publish.
func publish(bus *pipeline.EventBus, evt string, payload map[string]any) {
	if bus == nil {
		return
	}
	bus.Publish(pipeline.Event{Type: evt, Payload: payload})
}

// resolveProjectAndState validates method, parses path, looks up the
// project, and loads the per-project state. Returns the project, the
// loaded state, the hash, an unlock function, and a bool indicating
// whether the response has already been written (caller must return on
// false).
//
// On success the per-project state lock is held; the caller MUST defer
// unlock() after the ok check. On early failure the lock is not held
// and unlock is nil.
func resolveProjectAndState(w http.ResponseWriter, r *http.Request, deps Deps, want transition) (proj config.ProjectConfig, st *state.State, name, hash string, unlock func(), ok bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name, hash, transitionStr := parseProjectHashTransition(r.URL.Path)
	if name == "" || hash == "" || transition(transitionStr) != want {
		http.NotFound(w, r)
		return
	}
	unlock = deps.StateLock.Lock(name)
	appConfig := deps.Config()
	if appConfig == nil {
		unlock()
		writeJSONError(w, http.StatusInternalServerError, "config unavailable")
		return proj, nil, name, hash, nil, false
	}
	proj, found := findProject(appConfig, name)
	if !found {
		unlock()
		http.NotFound(w, r)
		return proj, nil, name, hash, nil, false
	}
	st, err := state.Load(appConfig.Daemon.OutputRoot, name)
	if err != nil {
		unlock()
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return proj, nil, name, hash, nil, false
	}
	if st == nil {
		unlock()
		writeJSONError(w, http.StatusInternalServerError, "state unavailable")
		return proj, nil, name, hash, nil, false
	}
	return proj, st, name, strings.ToLower(hash), unlock, true
}

// Apply handles POST /api/projects/{name}/findings/{hash}/apply.
//
// The SPA sends the rule pack's apply object in the body. We validate
// category eligibility, run apply.Apply (containment + size + SHA
// capture), persist the resulting FindingState, and publish
// finding.applied on the bus.
func Apply(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proj, st, name, hash, unlock, ok := resolveProjectAndState(w, r, deps, transitionApply)
		if !ok {
			return
		}
		defer unlock()
		appConfig := deps.Config()
		// Trusted apply plan lives in state; body fields are ignored
		// so a loopback caller cannot redirect the write to any path.
		prior, hasPrior := st.Findings[hash]
		if !hasPrior || prior.ApplySpec == nil {
			writeJSONError(w, http.StatusNotFound, "no apply spec recorded for this finding; re-run analysis to capture it")
			return
		}
		spec := prior.ApplySpec
		if !apply.EligibleCategories[spec.Category] {
			writeJSONError(w, http.StatusBadRequest, "category not eligible for apply")
			return
		}
		// Refuse re-apply: would clobber the AppliedReversal preimage,
		// stranding undo. Caller must undo first then re-apply.
		if prior.Status == state.FindingStatusApplied && prior.AppliedReversal != nil {
			writeJSONError(w, http.StatusConflict, "finding already applied; undo first before re-applying")
			return
		}
		rev, err := apply.Apply(apply.Request{
			ProjectRoot: proj.Path,
			TargetFile:  spec.TargetFile,
			Strategy:    spec.Strategy,
			Anchor:      spec.Anchor,
			Snippet:     spec.Snippet,
		})
		if err != nil {
			switch {
			case apply.IsContainment(err):
				writeJSONError(w, http.StatusBadRequest, "target outside project root")
			case apply.IsTargetTooLarge(err):
				writeJSONError(w, http.StatusRequestEntityTooLarge, "target file too large for safe apply (4 MiB cap)")
			case errors.Is(err, apply.ErrAnchorMissing):
				writeJSONError(w, http.StatusNotFound, "anchor not found in target file")
			default:
				writeJSONError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		// Merge into any existing entry so a dismissed/resolved record's
		// timestamps survive the transition into applied.
		findingState := st.Findings[hash]
		findingState.Status = state.FindingStatusApplied
		findingState.AppliedAt = time.Now().UTC()
		findingState.AppliedReversal = rev
		findingState.ProjectName = name
		st.Findings[hash] = findingState
		if err := state.Save(appConfig.Daemon.OutputRoot, name, st); err != nil {
			// The file write already succeeded via apply.Apply above.
			// Log at error level so operators can detect the partial-success
			// window where the target file is modified but no reversal is
			// persisted — undo will be impossible until state is repaired.
			publish(deps.Events, "apply.state_save_failed", map[string]any{
				"project": name,
				"hash":    hash,
				"target":  rev.Path,
				"error":   err.Error(),
			})
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if deps.StateCache != nil {
			deps.StateCache.Invalidate(name)
		}
		publish(deps.Events, pipeline.EventFindingApplied, map[string]any{
			"project":  name,
			"hash":     hash,
			"target":   rev.Path,
			"strategy": rev.Strategy,
		})
		writeJSON(w, http.StatusOK, findingState)
	}
}

// Undo handles POST /api/projects/{name}/findings/{hash}/undo. Restores
// the pre-image bytes captured at apply time. Refuses with 409 when the
// target file's SHA-256 has drifted (operator edited it post-apply).
func Undo(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proj, st, name, hash, unlock, ok := resolveProjectAndState(w, r, deps, transitionUndo)
		if !ok {
			return
		}
		defer unlock()
		appConfig := deps.Config()
		findingState, exists := st.Findings[hash]
		if !exists || findingState.Status != state.FindingStatusApplied || findingState.AppliedReversal == nil {
			writeJSONError(w, http.StatusNotFound, "no applied reversal for hash")
			return
		}
		if err := apply.Undo(proj.Path, *findingState.AppliedReversal); err != nil {
			switch {
			case apply.IsTargetChanged(err):
				writeJSONError(w, http.StatusConflict, "target file has changed since apply; refusing undo")
			case apply.IsContainment(err):
				writeJSONError(w, http.StatusBadRequest, "target outside project root")
			default:
				writeJSONError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		// Clear the applied state but keep the entry (and its ApplySpec)
		// so a future re-apply is possible without re-running analysis.
		findingState.Status = ""
		findingState.AppliedAt = time.Time{}
		findingState.AppliedReversal = nil
		st.Findings[hash] = findingState
		if err := state.Save(appConfig.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if deps.StateCache != nil {
			deps.StateCache.Invalidate(name)
		}
		publish(deps.Events, pipeline.EventFindingUndone, map[string]any{
			"project": name,
			"hash":    hash,
		})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// Dismiss handles POST /api/projects/{name}/findings/{hash}/dismiss.
func Dismiss(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, st, name, hash, unlock, ok := resolveProjectAndState(w, r, deps, transitionDismiss)
		if !ok {
			return
		}
		defer unlock()
		appConfig := deps.Config()
		// Preserve prior fields (AppliedAt + AppliedReversal in particular)
		// so a later undismiss can fall back to the applied state and a
		// captured reversal remains valid.
		findingState := st.Findings[hash]
		findingState.Status = state.FindingStatusDismissed
		findingState.DismissedAt = time.Now().UTC()
		findingState.ProjectName = name
		st.Findings[hash] = findingState
		if err := state.Save(appConfig.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if deps.StateCache != nil {
			deps.StateCache.Invalidate(name)
		}
		publish(deps.Events, pipeline.EventFindingDismissed, map[string]any{
			"project": name,
			"hash":    hash,
		})
		writeJSON(w, http.StatusOK, findingState)
	}
}

// Resolve handles POST /api/projects/{name}/findings/{hash}/resolve.
func Resolve(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, st, name, hash, unlock, ok := resolveProjectAndState(w, r, deps, transitionResolve)
		if !ok {
			return
		}
		defer unlock()
		appConfig := deps.Config()
		// Preserve prior AppliedAt + AppliedReversal so a later unresolve
		// returns the finding to its applied state with the reversal intact.
		findingState := st.Findings[hash]
		findingState.Status = state.FindingStatusResolved
		findingState.ResolvedAt = time.Now().UTC()
		findingState.ProjectName = name
		st.Findings[hash] = findingState
		if err := state.Save(appConfig.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if deps.StateCache != nil {
			deps.StateCache.Invalidate(name)
		}
		publish(deps.Events, pipeline.EventFindingResolved, map[string]any{
			"project": name,
			"hash":    hash,
		})
		writeJSON(w, http.StatusOK, findingState)
	}
}

// Undismiss handles POST /api/projects/{name}/findings/{hash}/undismiss.
// Drops a dismissed lifecycle entry so the finding returns to the open
// pool. No-op (still 200) when the entry is absent or not dismissed.
func Undismiss(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, st, name, hash, unlock, ok := resolveProjectAndState(w, r, deps, transitionUndismiss)
		if !ok {
			return
		}
		defer unlock()
		appConfig := deps.Config()
		if findingState, exists := st.Findings[hash]; exists && findingState.Status == state.FindingStatusDismissed {
			// If a prior Apply captured a reversal that we preserved
			// through dismiss, fall back to the applied state instead of
			// dropping the entry — otherwise the reversal becomes
			// unreachable (state.Findings[hash] is the only path Undo
			// reads from). Drop only when there was no underlying apply.
			if findingState.AppliedReversal != nil && !findingState.AppliedAt.IsZero() {
				findingState.Status = state.FindingStatusApplied
				findingState.DismissedAt = time.Time{}
				st.Findings[hash] = findingState
			} else {
				delete(st.Findings, hash)
			}
		}
		if err := state.Save(appConfig.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if deps.StateCache != nil {
			deps.StateCache.Invalidate(name)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// Unresolve handles POST /api/projects/{name}/findings/{hash}/unresolve.
// Symmetric to Undismiss for resolved entries.
func Unresolve(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, st, name, hash, unlock, ok := resolveProjectAndState(w, r, deps, transitionUnresolve)
		if !ok {
			return
		}
		defer unlock()
		appConfig := deps.Config()
		if findingState, exists := st.Findings[hash]; exists && findingState.Status == state.FindingStatusResolved {
			// Same fallback as Undismiss: keep an applied reversal reachable.
			if findingState.AppliedReversal != nil && !findingState.AppliedAt.IsZero() {
				findingState.Status = state.FindingStatusApplied
				findingState.ResolvedAt = time.Time{}
				st.Findings[hash] = findingState
			} else {
				delete(st.Findings, hash)
			}
		}
		if err := state.Save(appConfig.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if deps.StateCache != nil {
			deps.StateCache.Invalidate(name)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
