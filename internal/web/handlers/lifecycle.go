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
	txApply     transition = "apply"
	txUndo      transition = "undo"
	txDismiss   transition = "dismiss"
	txResolve   transition = "resolve"
	txUndismiss transition = "undismiss"
	txUnresolve transition = "unresolve"
)

// applyRequest is the body the SPA POSTs for the /apply endpoint. The
// SPA carries the rule pack's `apply` object client-side from the list
// view and feeds it back here. Category is sent for eligibility gating
// rather than re-derived server-side.
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
func parseProjectHashTransition(urlPath string) (name, hash, tx string) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	// Expect: api, projects, {name}, findings, {hash}, {transition}.
	if len(parts) != 6 || parts[0] != "api" || parts[1] != "projects" || parts[3] != "findings" {
		return "", "", ""
	}
	return parts[2], parts[4], parts[5]
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
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
// loaded state, the hash, and a bool indicating whether the response has
// already been written (caller must return on false).
func resolveProjectAndState(w http.ResponseWriter, r *http.Request, deps Deps, want transition) (proj config.ProjectConfig, st *state.State, name, hash string, ok bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	n, h, tx := parseProjectHashTransition(r.URL.Path)
	if n == "" || h == "" || transition(tx) != want {
		http.NotFound(w, r)
		return
	}
	cfg := deps.Config()
	if cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	p, found := findProject(cfg, n)
	if !found {
		http.NotFound(w, r)
		return
	}
	s, err := state.Load(cfg.Daemon.OutputRoot, n)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s == nil {
		writeJSONError(w, http.StatusInternalServerError, "state unavailable")
		return
	}
	return p, s, n, strings.ToLower(h), true
}

// Apply handles POST /api/projects/{name}/findings/{hash}/apply.
//
// The SPA sends the rule pack's apply object in the body. We validate
// category eligibility, run apply.Apply (containment + size + SHA
// capture), persist the resulting FindingState, and publish
// finding.applied on the bus.
func Apply(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proj, st, name, hash, ok := resolveProjectAndState(w, r, deps, txApply)
		if !ok {
			return
		}
		cfg := deps.Config()
		var req applyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if !apply.EligibleCategories[req.Category] {
			writeJSONError(w, http.StatusBadRequest, "category not eligible for apply")
			return
		}
		rev, err := apply.Apply(apply.ApplyRequest{
			ProjectRoot: proj.Path,
			TargetFile:  req.TargetFile,
			Strategy:    req.Strategy,
			Anchor:      req.Anchor,
			Snippet:     req.Snippet,
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
		fs := state.FindingState{
			Status:          state.FindingStatusApplied,
			AppliedAt:       time.Now().UTC(),
			AppliedReversal: rev,
			ProjectName:     name,
		}
		st.Findings[hash] = fs
		if err := state.Save(cfg.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		publish(deps.Events, pipeline.EventFindingApplied, map[string]any{
			"project":  name,
			"hash":     hash,
			"target":   rev.Path,
			"strategy": rev.Strategy,
		})
		writeJSON(w, http.StatusOK, fs)
	}
}

// Undo handles POST /api/projects/{name}/findings/{hash}/undo. Restores
// the pre-image bytes captured at apply time. Refuses with 409 when the
// target file's SHA-256 has drifted (operator edited it post-apply).
func Undo(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proj, st, name, hash, ok := resolveProjectAndState(w, r, deps, txUndo)
		if !ok {
			return
		}
		cfg := deps.Config()
		fs, exists := st.Findings[hash]
		if !exists || fs.Status != state.FindingStatusApplied || fs.AppliedReversal == nil {
			writeJSONError(w, http.StatusNotFound, "no applied reversal for hash")
			return
		}
		if err := apply.Undo(proj.Path, *fs.AppliedReversal); err != nil {
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
		delete(st.Findings, hash)
		if err := state.Save(cfg.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
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
		_, st, name, hash, ok := resolveProjectAndState(w, r, deps, txDismiss)
		if !ok {
			return
		}
		cfg := deps.Config()
		fs := state.FindingState{
			Status:      state.FindingStatusDismissed,
			DismissedAt: time.Now().UTC(),
			ProjectName: name,
		}
		st.Findings[hash] = fs
		if err := state.Save(cfg.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		publish(deps.Events, pipeline.EventFindingDismiss, map[string]any{
			"project": name,
			"hash":    hash,
		})
		writeJSON(w, http.StatusOK, fs)
	}
}

// Resolve handles POST /api/projects/{name}/findings/{hash}/resolve.
func Resolve(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, st, name, hash, ok := resolveProjectAndState(w, r, deps, txResolve)
		if !ok {
			return
		}
		cfg := deps.Config()
		fs := state.FindingState{
			Status:      state.FindingStatusResolved,
			ResolvedAt:  time.Now().UTC(),
			ProjectName: name,
		}
		st.Findings[hash] = fs
		if err := state.Save(cfg.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		publish(deps.Events, pipeline.EventFindingResolve, map[string]any{
			"project": name,
			"hash":    hash,
		})
		writeJSON(w, http.StatusOK, fs)
	}
}

// Undismiss handles POST /api/projects/{name}/findings/{hash}/undismiss.
// Drops a dismissed lifecycle entry so the finding returns to the open
// pool. No-op (still 200) when the entry is absent or not dismissed.
func Undismiss(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, st, name, hash, ok := resolveProjectAndState(w, r, deps, txUndismiss)
		if !ok {
			return
		}
		cfg := deps.Config()
		if fs, exists := st.Findings[hash]; exists && fs.Status == state.FindingStatusDismissed {
			delete(st.Findings, hash)
		}
		if err := state.Save(cfg.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// Unresolve handles POST /api/projects/{name}/findings/{hash}/unresolve.
// Symmetric to Undismiss for resolved entries.
func Unresolve(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, st, name, hash, ok := resolveProjectAndState(w, r, deps, txUnresolve)
		if !ok {
			return
		}
		cfg := deps.Config()
		if fs, exists := st.Findings[hash]; exists && fs.Status == state.FindingStatusResolved {
			delete(st.Findings, hash)
		}
		if err := state.Save(cfg.Daemon.OutputRoot, name, st); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
