// Package capture persists every LLM request/response exchange so that a
// failed or suboptimal call can be inspected and replayed later. Each
// pipeline run writes one run directory:
//
//	<outputRoot>/<projectName>/runs/<runID>/
//	├── meta.json     session-reconstruction metadata (provider, model, …)
//	└── calls.jsonl   one Record per LLM call, append-only
//
// Records are written after the response arrives but before any parse
// result is consumed, so a malformed model response is still captured in
// full — the exact data-loss gap this package closes.
package capture

import (
	"time"
)

const (
	// MetaFileName is the per-run metadata file name.
	MetaFileName = "meta.json"
	// CallsFileName is the append-only JSONL file of call records.
	CallsFileName = "calls.jsonl"

	// PhasePhase1 marks a phase-1 (mistake extraction) call.
	PhasePhase1 = "phase1"
	// PhasePhase2 marks a phase-2 (finding synthesis) call.
	PhasePhase2 = "phase2"

	// StatusOK means the provider responded and the response parsed cleanly.
	StatusOK = "ok"
	// StatusParseFailed means the response arrived but decoding failed.
	StatusParseFailed = "parse_failed"
	// StatusError means the transport/session itself failed.
	StatusError = "error"

	// KindAnalysis is a normal analysis run.
	KindAnalysis = "analysis"
	// KindReplay is a re-parse or re-send of a captured call.
	KindReplay = "replay"

	// ReplayModeReparse decodes the stored response again without an LLM call.
	ReplayModeReparse = "reparse"
	// ReplayModeResend sends the stored prompt to a provider again.
	ReplayModeResend = "resend"
)

// Record is one captured LLM exchange inside a run.
type Record struct {
	// Index is the 0-based sequence within the run; assigned by Writer.Append.
	Index int `json:"index"`
	// Phase is "phase1" or "phase2".
	Phase string `json:"phase"`
	// ChunkIndex is the 0-based transcript chunk for phase-1 calls; -1 for
	// phase-2 calls.
	ChunkIndex int `json:"chunk_index"`
	// ChunkCount is the total number of chunks in the run.
	ChunkCount int `json:"chunk_count"`
	// Prompt is the exact prompt text passed to Session.Run.
	Prompt string `json:"prompt"`
	// Response is the raw assistant text as returned by the provider
	// (secret-redacted by the caller). Empty when the call errored.
	Response string `json:"response,omitempty"`
	// Status is one of the Status* constants.
	Status string `json:"status"`
	// Error carries the truncated run/parse error message when Status is
	// not StatusOK.
	Error string `json:"error,omitempty"`
	// DurationMS is the wall time of the provider call only.
	DurationMS int64 `json:"duration_ms"`
	// Timestamp is when the call completed (UTC).
	Timestamp time.Time `json:"timestamp"`
}

// RunMeta describes the session context shared by every record in a run.
// It carries everything needed to rebuild an equivalent provider session
// during replay: working directory, model, sandbox mode, and system message.
type RunMeta struct {
	RunID         string    `json:"run_id"`
	ProjectName   string    `json:"project_name"`
	ProjectPath   string    `json:"project_path"`
	ProviderID    string    `json:"provider_id"`
	Model         string    `json:"model"`
	Sandbox       string    `json:"sandbox"`
	SystemMessage string    `json:"system_message"`
	StartedAt     time.Time `json:"started_at"`
	Since         string    `json:"since,omitempty"`

	// Kind distinguishes analysis runs from replays. Empty means analysis.
	Kind string `json:"kind,omitempty"`
	// Replay fields are set only when Kind == KindReplay.
	ReplayMode    string `json:"replay_mode,omitempty"`
	ParentRunID   string `json:"parent_run_id,omitempty"`
	ParentCallIdx int    `json:"parent_call_index,omitempty"` // -1 = unset
}
