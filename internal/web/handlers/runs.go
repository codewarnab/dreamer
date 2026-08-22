package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dreamer/internal/capture"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"dreamer/internal/replay"
)

// runIDRe matches capture run IDs (8 lowercase hex chars). Strict on
// purpose: the ID is joined into a filesystem path, so anything outside
// this shape is rejected before it can traverse.
var runIDRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

// parseProjectRunPath extracts {name}, {runID}, {tail...} from
// /api/projects/{name}/runs[/{runID}[/sub]]. Returns ok=false when the path
// shape does not match. runID is "" for the collection form.
func parseProjectRunPath(urlPath string) (name, runID string, tail []string, ok bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	// Expect: api, projects, {name}, runs[, {runID}[, sub...]].
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "projects" || parts[3] != "runs" {
		return "", "", nil, false
	}
	if len(parts) == 4 {
		return parts[2], "", nil, true
	}
	return parts[2], parts[4], parts[5:], true
}

// projectRunsDeps bundles per-request resolution shared by the three
// capture endpoints.
type projectRunsContext struct {
	projectName string
	outputRoot  string
	runsRoot    string
	projectPath string
}

func resolveRunsContext(deps Deps, name string) (*projectRunsContext, error) {
	proj := findProjectByName(deps.Config(), name)
	if proj == nil {
		return nil, errRunsProjectUnknown
	}
	outputRoot := deps.Config().Daemon.OutputRoot
	if outputRoot == "" {
		return nil, errRunsNoOutputRoot
	}
	return &projectRunsContext{
		projectName: name,
		outputRoot:  outputRoot,
		runsRoot:    capture.RunsRoot(outputRoot, name),
		projectPath: proj.Path,
	}, nil
}

type runsError string

func (e runsError) Error() string { return string(e) }

const (
	errRunsProjectUnknown = runsError("project not found")
	errRunsNoOutputRoot   = runsError("output root is not configured")
)

// ProjectRuns handles GET /api/projects/{name}/runs — the list of captured
// analysis and replay runs for one project, newest first.
func ProjectRuns(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		name, runID, tail, ok := parseProjectRunPath(r.URL.Path)
		if !ok || runID != "" || len(tail) != 0 {
			writeJSONError(w, http.StatusNotFound, "unknown runs route")
			return
		}
		ctx, err := resolveRunsContext(deps, name)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		summaries, err := capture.ListRuns(ctx.runsRoot)
		if err != nil {
			deps.Logger.Warn("list capture runs failed", logging.Any("err", err))
			writeJSONError(w, http.StatusInternalServerError, "list runs failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": summaries})
	}
}

// callSummaryView is a Record without the heavy prompt/response bodies.
type callSummaryView struct {
	Index         int       `json:"index"`
	Phase         string    `json:"phase"`
	ChunkIndex    int       `json:"chunk_index"`
	ChunkCount    int       `json:"chunk_count"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	DurationMS    int64     `json:"duration_ms"`
	Timestamp     time.Time `json:"timestamp"`
	PromptBytes   int       `json:"prompt_bytes"`
	ResponseBytes int       `json:"response_bytes"`
}

// RunDetail handles GET /api/projects/{name}/runs/{runID}.
// Default response: run metadata plus every call without prompt/response
// bodies. Add ?call=N to fetch that one call with its full bodies — the
// replay UI uses it to show what would be re-sent.
func RunDetail(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		name, runID, tail, ok := parseProjectRunPath(r.URL.Path)
		if !ok || len(tail) != 0 {
			writeJSONError(w, http.StatusNotFound, "unknown runs route")
			return
		}
		if !runIDRe.MatchString(runID) {
			writeJSONError(w, http.StatusBadRequest, "invalid run id")
			return
		}
		ctx, err := resolveRunsContext(deps, name)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		meta, records, err := capture.LoadRun(ctx.runsRoot, runID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, capture.ErrRunNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, "load run failed")
			return
		}

		callIdx := -1
		if raw := r.URL.Query().Get("call"); raw != "" {
			parsed, convErr := strconv.Atoi(raw)
			if convErr != nil || parsed < 0 || parsed >= len(records) {
				writeJSONError(w, http.StatusBadRequest, "invalid call index")
				return
			}
			callIdx = parsed
		}
		if callIdx >= 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"meta":  meta,
				"call":  records[callIdx],
				"index": callIdx,
			})
			return
		}

		summaries := make([]callSummaryView, len(records))
		for i, rec := range records {
			summaries[i] = callSummaryView{
				Index: rec.Index, Phase: rec.Phase,
				ChunkIndex: rec.ChunkIndex, ChunkCount: rec.ChunkCount,
				Status: rec.Status, Error: rec.Error,
				DurationMS: rec.DurationMS, Timestamp: rec.Timestamp,
				PromptBytes: len(rec.Prompt), ResponseBytes: len(rec.Response),
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"meta": meta, "calls": summaries})
	}
}

// replayRequest is the POST body for the replay endpoint.
type replayRequest struct {
	Mode      string `json:"mode"` // "reparse" | "resend"; default reparse
	CallIndex int    `json:"call_index"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
}

// RunReplay handles POST /api/projects/{name}/runs/{runID}/replay.
// Reparse is instant; resend performs a live LLM call and returns when it
// finishes (the SPA shows a spinner; loopback-only server keeps this simple).
func RunReplay(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		name, runID, tail, ok := parseProjectRunPath(r.URL.Path)
		if !ok || len(tail) != 1 || tail[0] != "replay" {
			writeJSONError(w, http.StatusNotFound, "unknown runs route")
			return
		}
		if !runIDRe.MatchString(runID) {
			writeJSONError(w, http.StatusBadRequest, "invalid run id")
			return
		}
		rctx, err := resolveRunsContext(deps, name)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}

		var req replayRequest
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
		if readErr != nil {
			writeJSONError(w, http.StatusBadRequest, "read request body failed")
			return
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			if jsonErr := json.Unmarshal(body, &req); jsonErr != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
		}

		packs, packsErr := pipeline.BuildRulePacks(deps.Config(), rctx.projectPath)
		if packsErr != nil {
			writeJSONError(w, http.StatusInternalServerError, "load rule packs failed")
			return
		}

		mode := req.Mode
		if mode == "" {
			mode = capture.ReplayModeReparse
		}
		switch mode {
		case capture.ReplayModeReparse:
			result, repErr := replay.Reparse(rctx.outputRoot, rctx.projectName, runID, req.CallIndex, packs)
			if repErr != nil {
				status := http.StatusInternalServerError
				if errors.Is(repErr, replay.ErrCallNotFound) || errors.Is(repErr, capture.ErrRunNotFound) {
					status = http.StatusNotFound
				}
				writeJSONError(w, status, repErr.Error())
				return
			}
			publish(deps.Events, "replay.done", map[string]any{
				"project": rctx.projectName, "run_id": runID, "mode": mode,
				"parent_call": req.CallIndex,
			})
			writeJSON(w, http.StatusOK, result)
		case capture.ReplayModeResend:
			result, resErr := replay.Resend(r.Context(), replay.Options{
				OutputRoot:       rctx.outputRoot,
				ProjectName:      rctx.projectName,
				RunID:            runID,
				CallIndex:        req.CallIndex,
				ProviderOverride: req.Provider,
				ModelOverride:    req.Model,
				AppConfig:        deps.Config(),
				Packs:            packs,
				Logger:           deps.Logger,
			})
			if resErr != nil {
				status := http.StatusInternalServerError
				if errors.Is(resErr, replay.ErrCallNotFound) || errors.Is(resErr, capture.ErrRunNotFound) {
					status = http.StatusNotFound
				}
				deps.Logger.Warn("replay resend failed", logging.Any("err", resErr))
				writeJSONError(w, status, resErr.Error())
				return
			}
			publish(deps.Events, "replay.done", map[string]any{
				"project": rctx.projectName, "run_id": result.ReplayRunID, "mode": mode,
				"parent_run_id": runID, "parent_call": req.CallIndex,
			})
			writeJSON(w, http.StatusOK, result)
		default:
			writeJSONError(w, http.StatusBadRequest, "mode must be \"reparse\" or \"resend\"")
		}
	}
}
