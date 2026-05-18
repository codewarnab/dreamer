# dreamer v1.2 — System Specification

Delta on top of `doc/spec.md` (v1) and `doc/spec.v1.1.md` (v1.1). When v1.2
conflicts with v1.1, v1.2 wins. Sections not mentioned here are unchanged.

Status legend: **[locked]** = design adopted and slated for implementation in
v1.2. **[provisional]** = design adopted but pending real-world validation.
**[deferred]** = design captured for a future release; ship a TODO marker
only.

Source plans folded into this spec:
- `doc/perf-plan.md` — analyze-pipeline performance work (items #1, #2, #3,
  #5, #6).
- `doc/plan-acp-sandbox-hardening.md` — symlink-aware containment + ACP
  liveness check.
- `doc/plan-state-tracking.md` — atomic state writes, per-category run
  timestamps, provider health fields.

---

## 1. Summary

v1.2 is a hardening + performance release. No new providers, no new chat
sources. The headline changes:

| Area              | Change                                                                            |
|-------------------|-----------------------------------------------------------------------------------|
| Pipeline perf     | Preflight short-circuit for empty transcripts; `cacheUnchanged` accepts empty-vs-empty after a prior run. |
| Pipeline perf     | Default lookback `since=24h` (was lifetime); `lifetime` escape hatch.             |
| Pipeline perf     | Transcript chunking by byte budget, provider-boundary aware (§7.12).              |
| Pipeline perf     | Phase 1+2 collapse: one multi-category prompt per chunk + one phase-2 call (§7.2). **[locked]** |
| Pipeline perf     | Sequential-chunk summary chaining; parallel chunks run independently (§7.13).     |
| Pipeline perf     | Opt-in parallel chunk execution behind config + CLI flag, default sequential.     |
| ACP sandbox       | Symlink-aware path containment in `DecidePermission`.                             |
| ACP runtime       | Liveness check on `session.Run` — fail fast when child process has exited.        |
| State tracking    | Atomic state writes via temp file + `os.Rename`.                                  |
| State tracking    | `LastRunPerCategory map[string]time.Time` on `State`.                             |
| State tracking    | `LastSuccessUTC` + `LastError` on `ProviderUsage`; 500-char error truncation.     |

Every change is additive at the schema level. Existing `state.json` files
load without migration. No new CLI subcommands.

---

## 2. CLI surface — extended

Three new flags on `analyze` and `daemon`:

| Flag                  | Effect                                                                                              |
|-----------------------|-----------------------------------------------------------------------------------------------------|
| `--parallel`          | Force `analyzer.execution.mode=parallel` for this invocation. Provider must implement `ParallelCapable` or the pipeline falls back to sequential with a logged warning. |
| `--max-concurrency=N` | Cap parallel session count. `0` = `len(chunks)`. Clamped down to `len(chunks)`. Ignored when mode is sequential. |
| `--max-chunk-bytes=N` | Override `analyzer.chunking.max_chunk_bytes` for this invocation. `0` disables chunking (single chunk regardless of size). |

`--since` flag on `analyze` changes default from `""` (lifetime) to `"24h"`.
New accepted value: `lifetime` (case-insensitive) — disables filtering,
restoring v1 behavior. Empty string still means "use the default".

Precedence: CLI flag > per-project config > global config > built-in
default.

---

## 3. Configuration system

### 3.1 Resolution order — unchanged

### 3.2 Global config schema — extended

Two new analyzer blocks: `analyzer.execution` and `analyzer.chunking`.
Existing `analyzer.rule_timeout_seconds` and `analyzer.rules.<category>.enabled`
are unchanged.

```yaml
analyzer:
  rule_timeout_seconds: 90
  rules:
    lint-rule:
      enabled: true
  execution:
    mode: sequential         # "sequential" (default) | "parallel"
    max_concurrency: 0       # 0 = len(chunks); else min(N, len(chunks))
  chunking:
    max_chunk_bytes: 480000             # ≈ 160k tokens at 3 bytes/token. 0 disables chunking.
    provider_boundary_headroom: 0.20    # start a new chunk when adding the next provider
                                        # would leave less than this fraction of
                                        # max_chunk_bytes free. 0 = always pack until full.
```

Go types (each new exported type carries a docstring):

```go
type AnalyzerConfig struct {
    RuleTimeoutSeconds int                   `yaml:"rule_timeout_seconds,omitempty"`
    Rules              map[string]RuleConfig `yaml:"rules,omitempty"`
    Execution          ExecutionConfig       `yaml:"execution,omitempty"`
    Chunking           ChunkingConfig        `yaml:"chunking,omitempty"`
}

// ExecutionConfig controls how the orchestrator dispatches the per-chunk
// provider calls. Default (zero value) is sequential — see §7.7.
type ExecutionConfig struct {
    // Mode is "sequential" (default) or "parallel". Unknown values fall
    // back to sequential with a logged warning.
    Mode string `yaml:"mode,omitempty"`
    // MaxConcurrency caps the parallel session count. 0 means
    // "len(chunks)"; values larger than len(chunks) are clamped down.
    // Ignored when Mode != "parallel".
    MaxConcurrency int `yaml:"max_concurrency,omitempty"`
}

// ChunkingConfig controls how the redacted transcript is split before
// phase-1 prompts are built. See §7.12. Budget is measured against
// transcript bytes only — grounding context and rule prompt are added on
// top and assumed to fit in the model's remaining context window.
type ChunkingConfig struct {
    // MaxChunkBytes is the upper bound on transcript bytes per chunk.
    // Zero disables chunking (single chunk regardless of transcript size).
    // Default 480000 (~160k tokens at 3 bytes/token).
    MaxChunkBytes int `yaml:"max_chunk_bytes,omitempty"`
    // ProviderBoundaryHeadroom is the minimum free fraction of
    // MaxChunkBytes that must remain after packing a provider before the
    // chunker will pack the next provider into the same chunk. Range
    // [0.0, 1.0). Default 0.20.
    ProviderBoundaryHeadroom float64 `yaml:"provider_boundary_headroom,omitempty"`
}
```

Project `since` field now defaults to `"24h"` when empty or whitespace.
`applyDefaults` fills it during config load. Setting `since: "lifetime"` in
project config preserves v1 lifetime behavior.

### 3.3 Per-project config — extended

`since` accepts `lifetime` (case-insensitive). All other fields unchanged.

### 3.4 Per-category rule config — unchanged

### 3.5 Config notices (new)

`Config` gains a runtime-only `Notices ConfigNotices` field (yaml/json
ignored) for "we changed this for you" signals. v1.2 emits one notice type:

```go
type Config struct {
    // ...existing fields...
    Notices ConfigNotices `yaml:"-" json:"-"`
}

// ConfigNotices collects soft signals discovered during config load. None
// of these are fatal; callers log them at info level once per process.
type ConfigNotices struct {
    // DefaultedSince lists project names whose `since` field was filled
    // in with the v1.2 default (24h). Empty when every project set
    // `since` explicitly.
    DefaultedSince []string
}
```

`cmd/daemon.go` and `cmd/analyze.go` log a single line per defaulted project
at startup:

```
project "my-repo" using default since=24h; set `since: lifetime` to restore prior behavior
```

---

## 4. Provider system

### 4.1 Strategy — unchanged

### 4.2 Selection rule — unchanged

### 4.3 Interfaces — extended

New optional capability interface alongside `Provider`:

```go
// ParallelCapable is implemented by providers verified to produce correct
// output when multiple sessions are opened against them concurrently.
// Providers that do not implement this interface — or implement it
// returning false — are treated as sequential-only and the pipeline silently
// falls back to mode=sequential with a logged warning.
//
// Verification means: at minimum, two concurrent NewSession calls succeed
// without sharing stdio pipes, account-level rate-limit budgets are not
// hit on a typical multi-chunk run, and outputs of concurrent Runs do not
// interleave or corrupt each other.
type ParallelCapable interface {
    SupportsParallelSessions() bool
}
```

Day-1 declarations:

| Provider id    | Implements `ParallelCapable` | Returns |
|----------------|------------------------------|---------|
| `copilot-sdk`  | yes                          | `true`  |
| `claude-cli`   | no                           | (treated false) |
| `codex-cli`    | no                           | (treated false) |
| All `*-acp`    | no                           | (treated false) |
| `gemini-sdk`   | no                           | (treated false) |
| `gemini-cli`   | no                           | (treated false) |
| `kiro-acp`     | no                           | (treated false) |

A future PR flips individual providers on after verification. No user-facing
breakage when a provider lacks support: the pipeline logs and runs
sequentially.

### 4.4 ACP adapter — extended

`acpcore.transport` now exposes a liveness signal. `readLoop`
(`acpcore.go:407`) flips `transport.closed = true` when the scanner returns
EOF or the child process is reaped. `session.Run` checks the flag before
writing to stdin and returns a typed sentinel:

```go
// ErrTransportClosed is returned by session.Run when the underlying ACP
// child process has exited before the call started. The caller should
// treat this as terminal for the current pipeline.Run — no restart is
// attempted. The next daemon tick will spawn a fresh provider.
var ErrTransportClosed = errors.New("acp: transport closed")
```

Behavior:

- `Run` returns within milliseconds (not the full rule timeout) when the
  child has already exited.
- No reconnect logic. No retry. Pipeline aborts the current project for the
  current cycle.
- The error joins with `analyzer.ErrUnavailable` so existing error-class
  detection at the orchestrator layer keeps working.

### 4.5 Read-only permission handler — hardened

The path containment check in `internal/analyzer/permission.go` now resolves
symlinks before comparing against the project root. v1 used
`filepath.Abs` + `filepath.Clean` only — a string-level check that misses
symlinks inside the project pointing outside it (e.g. a `notes` symlink
pointing at `~/.ssh/id_rsa`).

**Containment rules (replace v1 §4.5 rules verbatim):**

1. **Root normalization (once at provider startup).** `NormalizeRootPath`
   resolves the project root with `filepath.EvalSymlinks` and caches the
   result. A root that itself is a symlink (e.g. `/var → /private/var` on
   macOS) compares against its resolved form, so contents do not appear
   "outside" the root.
2. **Candidate resolution (per permission request).** `normalizeCandidatePath`:
   - If the candidate exists on disk: resolve directly with
     `filepath.EvalSymlinks`.
   - If the candidate does not exist: walk up to the deepest existing
     ancestor, resolve that ancestor, then re-join the unresolved tail.
     This catches symlinks anywhere in the path prefix.
   - Reject (deny) when the deepest existing ancestor is itself outside the
     project root, or when no ancestor up to the root exists.
3. **Resolver errors → deny.** Any error from `EvalSymlinks` other than
   "not exists" returns a deny decision with a reason mentioning
   resolution. Never approve on resolver error — a security-relevant
   boundary fails closed.

`pathWithinRoot` is unchanged in signature; it now compares
already-resolved paths. Windows case-folding stays intact because
`EvalSymlinks` already normalizes case on Windows and handles reparse
points.

Performance: each permission check stats a handful of inodes. Permission
requests per run are O(tens), not O(thousands). Acceptable.

Other branches (URL, Shell, MCPTool, CustomTool) are unchanged.

---

## 5. Discovery system — unchanged

---

## 6. Redaction — unchanged

---

## 7. Analysis pipeline

### 7.1 Shape — unchanged

### 7.2 Two phases — collapsed [locked]

v1.1 sent **one prompt per enabled `RulePack` per phase**: ~6 phase-1 calls
followed by up to ~6 phase-2 calls. v1.2 collapses this:

- **Phase 1 (per chunk).** One multi-category prompt per transcript chunk
  (§7.12). The prompt lists every enabled rule category once and asks the
  model to return `{category: [...mistakes]}`. K chunks → K phase-1 calls.
- **Phase 2 (always one call).** A single phase-2 prompt synthesizes
  guardrails from the **union** of phase-1 mistakes across all chunks. The
  transcript itself is **not** re-shipped in phase 2 — only grounding
  context + mistakes JSON. Output: `{category: [...findings]}`.

Total provider calls per project run: `K + 1` (down from up to `2N` where
N = enabled packs, typically 6). One chunk: 2 calls. Three chunks: 4
calls.

`RulePack` survives as a configuration unit — it still defines per-category
prompts, JSON schemas, thresholds, and per-category enable toggles. The
orchestrator merges enabled packs into a single multi-category template at
prompt-build time. Per-rule timeouts still apply (the orchestrator
enforces `rule_timeout_seconds` against the whole chunk call, not per
category, since one call serves all categories).

Phase 2 grounding shape:

```json
{
  "phase": "guardrail_synthesis",
  "toolchain_summary": "...",
  "codebase_context": "...",
  "phase1_mistakes": {
    "<category>": [ {...mistake...}, ... ],
    ...
  }
}
```

Mistakes order in `phase1_mistakes`: by chunk index, then by category
declaration order, then by within-call model order. Deterministic across
sequential and parallel runs.

Risk: phase-2 prompt size scales with `Σ |mistakes|`. If mistakes union
exceeds the model's input budget, phase 2 errors out and the orchestrator
records the failure. v1.2 mitigates by capping `phase1_mistakes` to the
top-K per category (`K=20`) before serializing; truncation logs a
warning. This is per-category, not global.

**Phase-2 grounding shape — files-only.** The `codebase_context` field
in the phase-2 prompt is restricted to **file-path listing only**: no
symbol extraction (function names, struct names, exports, etc.), no
per-file summaries. The phase-2 task is guardrail synthesis from
*mistakes*, not re-grounding from code — symbols would inflate the
prompt without informing the output. The file list is hard-capped at
**500 entries**; when the toolchain detector returns more, paths are
truncated to the top-500 by the detector's existing ranking (recent
modifications first) and a `phase2 file-list truncated original=<N>`
warning is appended to `result.Warnings`. Phase 1's grounding is
unaffected and continues to use the v1.1 grounding shape.

### 7.3 Phase-2 gate — unchanged

### 7.4 Codebase grounding — unchanged

### 7.5 Validation — unchanged

### 7.6 Token budget — superseded by §7.12

v1.1's lifetime-transcript-into-one-prompt model is replaced by the
byte-budgeted chunking system in §7.12. The phase-1 token budget is now
implicitly `MaxChunkBytes` per call (transcript only — grounding and
rule prompt are added on top). See §7.12 for the packing algorithm and
oversize-provider handling.

### 7.7 Execution mode — new

The orchestrator dispatches **phase-1 per-chunk** provider calls under one
of two modes. Phase 2 is always a single serial call after phase 1 joins
(§7.2).

**Sequential (default).** One session, chunks run in chunk-index order.
When `K > 1` chunks exist, sequential mode chains a brief rolling summary
from chunk *i* into chunk *i+1*'s prompt (§7.13). Sequential is the
*reversible* default — users can opt parallel in per-provider once we have
evidence the provider tolerates it.

**Parallel (opt-in).** Multiple sessions, one per worker, bounded by
`MaxConcurrency`. Cuts phase-1 wall time on a K-chunk run from
~`Σ(per-chunk)` to ~`max(per-chunk)`. No summary chaining in parallel mode
(§7.13) — chunks run independently.

When `K == 1` (single chunk, the common case post-chunking) both modes
issue the same `1 + 1 = 2` calls. Mode only matters when `K ≥ 2`.

Why sessions, not goroutines on one session: `Session.Run` is backed by CLI
providers (claude-cli, codex-cli, ACP children) that pipe stdin/stdout to a
single child process. Two goroutines on one session interleave bytes and
deadlock or corrupt output. Parallel = multiple sessions, full stop.

**RunConfig (new orchestrator input):**

```go
// RunConfig tells the orchestrator how to dispatch per-chunk phase-1
// calls for one pipeline.Run invocation. SessionFactory must be safe to
// call concurrently when Mode == ModeParallel. The orchestrator owns the
// SessionPool it builds from this config and closes it on return.
type RunConfig struct {
    SessionFactory func(context.Context) (Session, error)
    Mode           ExecutionMode // ModeSequential | ModeParallel
    MaxConcurrency int           // ignored when Mode != ModeParallel
}
```

**Orchestrator shape.** `runPhase1` dispatches on `rc.Mode`. Phase logic
splits into `runPhase1Sequential` and `runPhase1Parallel`. Phase 2 runs in
`runPhase2`, always single-call, always serial.

```go
func runPhase1(ctx context.Context, rc RunConfig, chunks []Chunk, ...) ([]ChunkResult, error) {
    if rc.Mode == ModeParallel && len(chunks) > 1 {
        return runPhase1Parallel(ctx, rc, chunks, ...)
    }
    return runPhase1Sequential(ctx, rc, chunks, ...)
}
```

**Sequential dispatch shape (`runPhase1Sequential`):**

1. Acquire one session from the pool.
2. For each `chunk[i]` in index order:
   - Build prompt: grounding context + multi-category instruction +
     (if `i > 0` and `K > 1`) chunk `i-1`'s summary + chunk transcript.
   - `session.Run` → parse `{category: [mistakes]}` + optional `summary`.
   - On rate limit: abort, return `ErrRateLimited`.
   - On other error: append warning, continue with next chunk.
3. Return `[]ChunkResult` in chunk-index order.

**Parallel dispatch shape (`runPhase1Parallel`):**

1. `n := min(rc.MaxConcurrency or len(chunks), len(chunks))`.
2. `g, gctx := errgroup.WithContext(ctx)`; semaphore `make(chan struct{}, n)`.
3. Each worker: acquire semaphore → acquire session from pool →
   build prompt (no summary preamble — chunks are independent) →
   `session.Run` → parse → write result into pre-sized `results[chunkIdx]`
   slot. No mutex needed because each goroutine owns its own index.
4. `ErrRateLimited` in any worker cancels `gctx`. Siblings observe
   `context.Canceled` from their pending `session.Run` and unwind. Returned
   error to the caller is the original `ErrRateLimited`.
5. Non-rate-limit errors in a worker append a warning and continue.
6. After `g.Wait()`: return `results[]` in chunk-index order.

**Phase 2 (`runPhase2`):**

1. Acquire one session from the pool (sequential — single call).
2. Build prompt: grounding context + serialized union of phase-1
   mistakes (top-K per category per §7.2) + multi-category guardrail
   instruction.
3. `session.Run` → parse `{category: [findings]}`.
4. Return findings.

**Determinism guarantees:**

- `result.Mistakes` order: by chunk index, then by category declaration
  order, then by within-call model order.
- `result.Findings` order: by category declaration order, then by
  within-call model order. Independent of phase-1 mode.
- `result.Warnings`: interleaves across chunks in parallel mode (logging
  is real-time). Tests assert *set* equality on warnings after sorting.
- `state.json` content: byte-identical to sequential — it stores hashes
  and counts, no ordering.

### 7.8 Session pool — new

New file `internal/analyzer/sessionpool.go`. Used by both modes (sequential
routes through a pool-of-one, by design — eliminates `if mode == sequential`
branches at every session site).

```go
// sequentialPoolCap is the SessionPool cap used in sequential mode.
const sequentialPoolCap = 1

// SessionPool owns at most `cap` concurrently-live Sessions for one
// pipeline.Run. Callers Acquire a session, do one prompt, then Release it
// (passing ok=false discards a session known to be broken).
//
// Goroutine-safe. Acquire honors ctx cancellation. Close shuts down every
// session the pool created, even those currently checked out.
//
// Invariant: at all times, len(checked-out) + len(free) ≤ cap.
type SessionPool struct {
    factory func(context.Context) (Session, error)
    cap     int
    mu      sync.Mutex
    free    []Session
    all     []Session
}

func NewSessionPool(cap int, factory func(context.Context) (Session, error)) *SessionPool

// Acquire returns a usable session, blocking until one is free or until
// ctx is cancelled.
//
//   Precondition:  ctx is not yet cancelled.
//   Postcondition: returned Session is usable until passed to Release.
//   Invariant:     active sessions never exceed `cap`.
func (p *SessionPool) Acquire(ctx context.Context) (Session, error)

// Release returns a session to the pool. If ok=false the pool closes
// and discards the session; the freed slot is available for a fresh
// factory call on the next Acquire.
func (p *SessionPool) Release(s Session, ok bool)

// Close shuts down every session created by the pool (free or checked
// out). Safe to call exactly once; subsequent calls are no-ops.
func (p *SessionPool) Close() error
```

Lifecycle: one pool per `pipeline.Run`, shared across phase 1 and phase 2,
`defer pool.Close()` in pipeline.Run.

### 7.9 Lookback default — new

`internal/pipeline/lookback.go`:

```go
// DefaultSince is the default lookback window applied when a project's
// `since` field is empty. Selected to bound first-run input volume on
// long-lived projects (lifetime ingestion can ship hundreds of MB).
const DefaultSince = "24h"
```

`parseLookbackWindow` accepts `lifetime` (case-insensitive) and returns
`(0, false, nil)` — same shape as the empty-string case in v1, so existing
call sites continue to bypass filtering.

`applyDefaults` in `internal/config/loader.go` fills empty
`Projects[i].Since` with `DefaultSince` and appends the project name to
`cfg.Notices.DefaultedSince`.

### 7.10 Preflight short-circuit — new

After `buildRedactedTranscript` and before `toolchain.Detect`, the pipeline
checks `messageCount == 0` and exits early without invoking the provider:

```go
if messageCount == 0 {
    logger.Info("preflight skip",
        logging.Any("reason", "no readable chat messages"),
        logging.Any("sources_discovered", len(sources)))

    currentState.LastRunUTC = time.Now().UTC()
    currentState.RepoHeadSHA = repoHeadSHA
    currentState.ChatHashes  = cacheKeys
    if err := state.Save(outputRoot, projectName, currentState); err != nil {
        // Non-fatal: the no-op Result we return is correct regardless.
        // A failed save only costs us next run's cache-hit optimization.
        logger.Warn("preflight state save failed", logging.Any("err", err))
    }

    return Result{
        ProviderID:      providerID,
        SourcesAnalyzed: 0,
        MessagesRead:    0,
        Warnings:        1,
        TodosPath:       todosOutputPath(outputRoot, projectName),
        NoMistakes:      true,
    }, nil
}
```

Why post-transcript (not post-discovery): `buildRedactedTranscript` may
yield zero readable messages even when sources exist (all empty/corrupt
JSONL, sanitizer dropped everything). One gate covers both blank-folder and
blank-transcript cases.

Why save state on the empty path: without it, `cacheUnchanged` (§7.11)
stays false forever — every subsequent run re-walks discovery and produces
the same no-op. Saving the empty `ChatHashes` map + `repoHeadSHA` gives
§7.11 something to match against next run.

Acceptance: `time dreamer analyze --project /empty/dir` finishes in
sub-second wall time, down from ~7 minutes.

### 7.11 `cacheUnchanged` tightening — new

`internal/pipeline/cache.go:13` previously short-circuited on
`len(currentState.ChatHashes) == 0`, which made blank folders re-walk
forever. v1.2 rewrites the check around the real invariant:

```go
func cacheUnchanged(currentState *state.State, cacheKeys map[string]string, repoHeadSHA string) bool {
    if currentState == nil {
        return false
    }
    // "Never run before" cannot be a cache hit, even on a blank folder.
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
```

Three behavior changes:

1. Drop the `len(currentState.ChatHashes) == 0` early-bail.
2. Add a `LastRunUTC.IsZero()` guard so a half-initialized state file does
   not count as "I've seen this before".
3. `len(cacheKeys) != len(currentState.ChatHashes)` already handles
   empty-vs-empty correctly (`0 == 0`), so the for-loop is a no-op on
   empty maps. `{} matches {}` becomes a clean true.

Case analysis (only row 2 flips):

| Cn | Cp | Hn?=Hp | LastRunUTC | Want hit? | v1 result | v1.2 result |
|----|----|--------|------------|-----------|-----------|-------------|
| 0  | 0  | yes    | zero       | no        | no        | no  ✓       |
| 0  | 0  | yes    | non-zero   | **yes**   | **no** ✗  | **yes** ✓   |
| 0  | 0  | no     | non-zero   | no        | no        | no  ✓       |
| 0  | N  | yes    | non-zero   | no        | no        | no  ✓       |
| N  | 0  | yes    | non-zero   | no        | no        | no  ✓       |
| N  | N  | yes    | non-zero   | match-dep | matches   | matches ✓   |

§7.10 and §7.11 only pay off together: §7.10 saves the empty state, §7.11
makes that saved state a real cache hit on the next run.

### 7.12 Transcript chunking — new

After `buildRedactedTranscript` returns the per-source builder slices,
v1.2 packs them into one or more **chunks** for phase 1. Each chunk is
the unit of one phase-1 provider call (§7.2).

**Chunk struct (new):**

```go
// Chunk is one phase-1 prompt's transcript payload. Sources in
// SourceLabels appear in Transcript in the same order. Bytes is the
// transcript-only byte length used by the chunker — grounding context
// and rule prompt are added on top by the orchestrator.
type Chunk struct {
    Index        int       // 0-based, dense, deterministic
    SourceLabels []string  // e.g. ["claude", "codex", "copilot"]
    Transcript   string    // concatenated per-source blocks
    Bytes        int       // len(Transcript)
    Split        bool      // true if this chunk holds part of a single
                           // provider that was hard-split (§7.12, oversize case)
}
```

**Packing algorithm (`internal/pipeline/chunker.go`):**

1. Group redacted transcript into **provider blocks** by `source.Tool`,
   one block per tool. Within a block, sources are concatenated in
   discovery order (modified-time desc, path asc).
2. Start with empty `current` chunk. `budget := cfg.MaxChunkBytes`.
3. For each provider block `b`:
   - If `cfg.MaxChunkBytes == 0`: append `b` to `current` regardless of
     size (chunking disabled).
   - Else if `len(b) > budget`: **oversize provider, hard split + warn**
     (§7.12 oversize rule below).
   - Else if `current.Bytes + len(b) <= budget * (1 - HeadroomFraction)`:
     append `b` to `current` (fits with the required headroom).
   - Else if `current.Bytes == 0`: append `b` to `current` (block fits
     within `budget` but exceeds the headroom cap — first-block-wins, no
     point starting an empty chunk).
   - Else: seal `current` (assign next `Index`), emit it, start fresh
     `current` with `b`.
4. After the loop, seal and emit any non-empty `current`.

`HeadroomFraction = cfg.ProviderBoundaryHeadroom`. Default `0.20` →
`current.Bytes + len(b) <= budget * 0.80` to pack into existing chunk.

**Oversize-provider rule.** When a single provider block exceeds
`budget` on its own (e.g. a multi-MB claude session under `since=lifetime`):

1. Hard-split that block by **message boundary** (never mid-message) into
   sub-blocks of `≤ budget` bytes each.
2. Each sub-block becomes its own chunk with `Split=true`.
3. Emit one warning to logs and append to `Result.Warnings`:
   `provider <tool> hard-split into N chunks (M bytes); consider tightening since (current: <value>) to reduce input volume`.
4. State: `ProviderUsage.<id>.LastError` is **not** set (this is a
   transcript-size signal, not a provider failure).

**Chunking-disabled mode.** `MaxChunkBytes == 0` produces exactly one
chunk containing every provider block in discovery order. Equivalent to
pre-v1.2 single-prompt shape (but still phase-collapsed per §7.2 — so 2
calls per run, not 12).

**Determinism.** Chunk count and content are a pure function of
`(MaxChunkBytes, HeadroomFraction, discovered sources, per-source bytes)`.
No timing, no concurrency. Tests assert `Chunk(input) == Chunk(input)`
across runs.

**Worked example.** `MaxChunkBytes=480000`, `HeadroomFraction=0.20`,
fill cap = `480000 * 0.80 = 384000`. Providers (bytes): claude=200000,
codex=150000, copilot=180000, vscode=40000.

- claude (200000) → fits empty chunk → `current=[claude] @ 200000`
- codex (150000): would reach `350000 > 384000`? No, `350000 ≤ 384000`
  → pack → `current=[claude, codex] @ 350000`
- copilot (180000): would reach `530000 > 384000` → seal chunk 0,
  start chunk 1 with copilot → `current=[copilot] @ 180000`
- vscode (40000): `220000 ≤ 384000` → pack → `current=[copilot, vscode] @ 220000`
- end → seal chunk 1.

Result: 2 chunks. Phase 1: 2 calls. Phase 2: 1 call. Total: **3 calls.**

### 7.13 Chunk-summary chaining — new

When phase 1 runs sequentially across multiple chunks
(`Mode=sequential && K > 1`), each phase-1 response carries a brief
`summary` field that becomes preamble in the next chunk's prompt. The
goal is preserving cross-chunk context (e.g. "earlier the user was
debugging the auth refactor; this chunk continues that thread") without
re-shipping prior transcripts.

**When chaining is enabled:** `K > 1 && Mode == sequential`.

**When chaining is disabled:** `K == 1` (no prior chunk) OR
`Mode == parallel` (chunks fire concurrently, no causal order).

**Prompt addition (sequential mode, chunk i where i > 0):**

```
<rolling_context>
Prior chunks covered:
{chunk[i-1].summary}
</rolling_context>

<transcript_chunk index="{i+1}" of="{K}" sources="{chunk[i].SourceLabels}">
{chunk[i].Transcript}
</transcript_chunk>
```

**Response addition (sequential mode, every chunk except the last):**

The phase-1 JSON schema gains a **required** top-level `summary` string
field (≤ 2000 chars). Truncation rule: orchestrator hard-clamps incoming
strings longer than 2000 chars and logs a warning — but **does not**
synthesize a missing `summary`. If the model omits the field entirely
(or returns non-string), the orchestrator treats the chunk call as a
**schema violation**, appends a `schema violation chunk=<i>` warning,
increments `ProviderUsage.<id>.Failures`, and aborts phase 1 for this
project run (no chain to broken context). The next daemon cycle retries
from scratch.

Why no fallback synthesis: a fallback summary built from
`SourceLabels`/`Bytes` carries no semantic content (no themes, no
mistake hints), which makes the rolling-context block actively
misleading rather than helpful. Better to fail loud than chain on
empty.

**Final-chunk exemption:** chunk `K-1` (the last chunk) does not need a
`summary` — there is no chunk `K` to consume it. The orchestrator
ignores `summary` on the final chunk regardless of presence/absence.

**Why no chaining in parallel mode.** Workers run concurrently; chunk
i+1 starts before chunk i's response is available. Waiting for a summary
serializes the chunks and erases the parallel speedup. Independent
parallel chunks rely on grounding context + their own transcript only —
mistakes that span chunks are recovered at phase 2 from the union of
phase-1 outputs (§7.2).

**Determinism.** With chaining, chunk i+1's prompt depends on chunk i's
model output → strictly speaking non-deterministic across model runs.
Tests run against a fake provider that returns a fixed summary string,
so test-level determinism holds. Real-provider runs may produce
different chunk-2 mistakes than sequential-without-chaining, but
phase-2 output (the user-facing artifact) typically converges because
mistakes are deduped by `(category, description)` at the orchestrator
layer.

**Trade-off documented.** Sequential mode favors cross-chunk continuity
at the cost of strict reproducibility. Parallel mode favors speed and
strict reproducibility at the cost of cross-chunk context. Operators
pick.

---

## 8. Toolchain detection — unchanged

---

## 9. Rule packs — unchanged

---

## 10. Lint-rule allow-list — unchanged

---

## 11. Output — unchanged

---

## 12. State & incremental runs

### 12.1 Schema — extended

`internal/state/tracker.go` `State` and `ProviderUsage` gain three new
fields. All additive — old `state.json` files load fine, missing fields
normalize to zero values.

```go
type State struct {
    Version            int                  `json:"version"`
    LastRunUTC         time.Time            `json:"last_run_utc"`
    RepoHeadSHA        string               `json:"repo_head_sha,omitempty"`
    ChatHashes         map[string]string    `json:"chat_hashes,omitempty"`
    FindingHashes      []string             `json:"finding_hashes,omitempty"`
    ProviderUsage      map[string]ProviderUsage `json:"provider_usage,omitempty"`
    UsageStats         UsageStats           `json:"usage_stats,omitempty"`

    // LastRunPerCategory records the most recent successful completion
    // timestamp per rule category. A rule that times out or returns a
    // provider error does NOT update its entry, so stale timestamps signal
    // a flaky rule. Map keys are RuleCategory string values.
    LastRunPerCategory map[string]time.Time `json:"last_run_per_category,omitempty"`
}

type ProviderUsage struct {
    Runs           int64     `json:"runs"`
    TotalTokens    int64     `json:"total_tokens"`
    Timeouts       int64     `json:"timeouts,omitempty"`
    Failures       int64     `json:"failures,omitempty"`

    // LastSuccessUTC is the timestamp of the most recent provider call
    // that returned without error. Cleared LastError indicates the
    // provider is currently healthy; raw Failures alone cannot distinguish
    // "47 failures last year" from "47 failures last hour".
    LastSuccessUTC time.Time `json:"last_success_utc,omitempty"`

    // LastError is the most recent error message from this provider,
    // truncated to 500 chars via state.TruncateError so multi-KB upstream
    // JSON blobs cannot dominate state.json. Cleared on the next success.
    LastError      string    `json:"last_error,omitempty"`
}
```

`defaultState()` and `normalizeState()` initialize
`LastRunPerCategory` to an empty map when nil.

New helper:

```go
// TruncateError clamps an error string to 500 characters, appending '…'
// when truncation occurs. Short strings pass through unchanged. Used by
// the pipeline before writing ProviderUsage.LastError to bound state.json
// size.
func TruncateError(s string) string
```

### 12.2 Atomic writes — new

`state.Save` no longer calls `os.WriteFile(path, ...)` directly. New shape:

1. `tmp := path + ".tmp"`
2. `os.WriteFile(tmp, data, statePerms)` — wrap and return any error.
3. `os.Rename(tmp, path)` — on failure, best-effort `os.Remove(tmp)` and
   return the wrapped rename error.

`os.Rename` is atomic on POSIX and Windows when source and destination
share a volume, which they always do here. Mid-write crashes
(`Ctrl+C`, daemon SIGTERM) can no longer leave a half-written `state.json`
that fails `json.Unmarshal` next start.

Public API unchanged.

### 12.3 Per-category timestamps — new

The orchestrator result gains `CompletedCategories []string` (categories
that completed without timeout or provider error — zero-finding completion
counts). After successful analysis, `pipeline.Run`:

```go
now := time.Now().UTC()
for _, cat := range analysisResult.CompletedCategories {
    if currentState.LastRunPerCategory == nil {
        currentState.LastRunPerCategory = map[string]time.Time{}
    }
    currentState.LastRunPerCategory[cat] = now
}
```

What counts as "completed":
- Ran to completion without `rule_timeout_seconds` firing.
- Did not return a transport / provider error.
- Returning zero findings still counts — it ran.

A timed-out or errored rule keeps its previous timestamp. Operators see
staleness as a signal.

### 12.4 Provider health fields — new

On every provider-touching path in `internal/pipeline/pipeline.go`,
v1.2 routes both success and failure through helpers so no path forgets
to update `ProviderUsage`:

```go
// recordProviderSuccess updates ProviderUsage on a clean run.
//
//   Runs++, LastSuccessUTC = now, LastError = ""
func recordProviderSuccess(currentState *state.State, providerID string, tokens int64)

// recordProviderFailure updates ProviderUsage on an error path and Saves
// state before the caller returns the error to its caller.
//
//   if errors.Is(err, context.DeadlineExceeded) → Timeouts++
//   else                                        → Failures++
//   LastError = state.TruncateError(err.Error())
//   state.Save(...) (best-effort; logs on failure but does not mask err)
//
// All early `return Result{}, err` paths after `analyzer.NewProvider`
// returns are wrapped through this helper. The helper Saves before
// returning so failure context survives the abort.
func recordProviderFailure(currentState *state.State, outputRoot, projectName, providerID string, err error)
```

Risk: forgetting an error path. Mitigation: every provider-error early
return in `pipeline.go` routes through `recordProviderFailure`. Adding new
error paths requires touching this helper, which keeps the contract
discoverable.

### 12.5 Cache key — unchanged

`sha256(path || file_hash || repo_head_sha)` per chat file, same as v1.

---

## 13. Auth & error handling

### 13.1 Startup checks — unchanged

### 13.2 Per-provider remediation — unchanged

### 13.3 Daemon mode — unchanged

### 13.4 Mid-run errors — extended

New error class: `acpcore.ErrTransportClosed` (§4.4). Treatment:

- `pipeline.Run` returns the error wrapped through `recordProviderFailure`
  (§12.4) so `Failures++` and `LastError` are populated before exit.
- The current project is skipped for the current daemon cycle. Next cycle
  spawns a fresh provider.
- No retry within the same cycle.

Rate-limit handling in parallel mode (§7.7): worker that sees
`ErrRateLimited` cancels the errgroup context; siblings observe
`context.Canceled` and unwind cleanly; `runPhase` returns the original
`ErrRateLimited` to match v1 sequential behavior.

---

## 14. Daemon — unchanged

---

## 15. Logging — extended

New log fields produced by the orchestrator:

| Event              | Fields                                                                  |
|--------------------|-------------------------------------------------------------------------|
| `preflight skip`   | `reason`, `sources_discovered`                                          |
| `chunked transcript` | `chunks`, `total_bytes`, `max_chunk_bytes`, `hard_splits`             |
| `phase done`       | `phase`, `mode` (sequential/parallel), `concurrency`, `wall_ms`, `calls`|
| `parallel fallback`| `provider`, `reason="provider does not support parallel"`               |
| `since defaulted`  | `project`, `since="24h"` (once per project per process)                 |
| `acp transport closed` | `provider`, `project`                                               |
| `provider hard split` | `provider`, `chunks`, `bytes` (oversize-block warning per §7.12)     |

Parallel-mode logs interleave across chunks. Mitigations:
1. Every log call inside a chunk worker carries `chunk=<index>` (and the
   participating `sources=<comma-joined-tool-list>`).
2. End-of-phase summary line emits once per phase: total calls (= K
   phase-1 + 1 phase-2), total wall ms, mode.

Buffered per-chunk flush is **not** used — it hides real-time progress
and breaks the daemon tail-the-log UX.

---

## 16. Directory & file layout — additive

```
internal/
  analyzer/
    sessionpool.go              # new — bounded session pool, used by both modes
    orchestrator.go             # runPhase splits into dispatcher + sequential + parallel
    provider.go                 # ParallelCapable interface added
    permission.go               # symlink-aware containment
    providers/
      acpcore/
        acpcore.go              # liveness flag + ErrTransportClosed
  state/
    tracker.go                  # atomic Save, LastRunPerCategory, ProviderUsage extensions, TruncateError
  pipeline/
    pipeline.go                 # preflight skip, recordProviderSuccess/Failure helpers, chunk dispatch
    cache.go                    # tightened cacheUnchanged
    lookback.go                 # DefaultSince, lifetime escape hatch
    chunker.go                  # new — Chunk type, Pack() byte-budget + provider-boundary algo
  analyzer/
    promptbuilder.go            # new — multi-category phase-1/phase-2 template assembly
  config/
    loader.go                   # applyDefaults fills since, populates Config.Notices
```

No new packages, no new commands.

---

## 17. End-to-end run sequence — updated

Replaces v1 §17. New / changed steps marked `[v1.2]`.

`dreamer analyze --path /repo`:

1. **Load config.** Global + project + CLI flag merge. Resolve provider id,
   output dir, rule pack. **[v1.2]** `applyDefaults` fills missing `since`
   with `24h` and appends affected project names to
   `cfg.Notices.DefaultedSince`.
2. **Log notices.** **[v1.2]** Emit one `since defaulted` line per project
   in `cfg.Notices.DefaultedSince`.
3. **Resolve project.** Absolute path + symlink-resolved.
4. **Discover chats.** §5.1.
5. **Load state.** Read `state.json`. Compute current cache keys. Compute
   `repo_head_sha`.
6. **Short-circuit (full cache hit).** v1 §12 logic, **[v1.2]** now also
   true when both `cacheKeys` and `state.ChatHashes` are empty and
   `LastRunUTC` is non-zero (§7.11).
7. **Read + sanitize chats.** §5.3.
8. **Redact.** §6.
9. **Preflight skip.** **[v1.2]** If `messageCount == 0`: save empty cache
   state and return a no-op `Result` (§7.10). No provider call. Exit `0`.
10. **Detect toolchain.** §8.
11. **Chunk transcript.** **[v1.2]** `chunker.Pack(transcript, cfg.Chunking)`
    returns `[]Chunk` per §7.12. Hard-split warnings, if any, append to
    `result.Warnings`.
12. **Build phase-1 prompts.** **[v1.2]** Multi-category template per
    chunk (§7.2). In sequential mode with `K > 1`, prompts after the
    first include the prior chunk's `summary` (§7.13).
13. **Provider start.** `provider.Start(ctx)`; on failure → §13.
14. **Resolve execution mode.** **[v1.2]** CLI > project > global > default
    (`sequential`). If `parallel` requested but provider lacks
    `ParallelCapable`, fall back to sequential with a logged warning.
    When `K == 1`, mode has no observable effect.
15. **Build session pool.** **[v1.2]** `NewSessionPool(cap, factory)` where
    `cap=1` in sequential, `cap=min(MaxConcurrency, len(chunks))` in
    parallel.
16. **Phase 1.** **[v1.2]** Dispatch via `runPhase1` (sequential or
    parallel per `RunConfig`). One call per chunk. Per-chunk timeout
    enforced (`rule_timeout_seconds`). Successful chunks contribute
    their categories to `result.CompletedCategories`.
17. **Gate.** v1 §7.3 unchanged — skip phase 2 when union of phase-1
    mistakes is empty.
18. **Phase 2.** **[v1.2]** Single serial call. Input: grounding context
    + union of phase-1 mistakes (top-K per category, see §7.2). No
    transcript re-shipped. Same pool, same provider.
19. **Render.** v1 §11 unchanged.
20. **Save state.** **[v1.2]** Atomic temp-file + rename (§12.2). Update
    `LastRunPerCategory` for each entry in `result.CompletedCategories`.
    Update `ProviderUsage.LastSuccessUTC` and clear `LastError` via
    `recordProviderSuccess` (§12.4).
21. **Close pool + provider.** `pool.Close()` then `provider.Close()`.
    Exit `0`.

On provider error at any point after step 13: route through
`recordProviderFailure` (§12.4) before returning. `state.json` reflects the
failure even though no findings were written.

---

## 18. Provider-specific notes

### 18.1–18.2 unchanged from v1.1

### 18.3 `copilot-sdk` parallel support [provisional]

The Copilot Go SDK manages sessions independently per
`NewSession` call. v1.2 declares `copilot-sdk.SupportsParallelSessions()
== true`. Verification before flipping the bit:

1. Two concurrent `NewSession` calls succeed without sharing stdio pipes.
2. Account-level rate-limit budgets are not hit on a typical multi-chunk run.
3. Outputs of concurrent `Run` calls do not interleave or corrupt each
   other.

If telemetry surfaces rate-limit storms or correctness issues, flip
`SupportsParallelSessions()` back to `false` and reissue.

---

## 19. Open issues

### I1 — Copilot SDK session timeout — unchanged

### I2 — Codex JSON event schema drift — unchanged

### I3 — Provider rate-limit storms under parallel mode [provisional]

**Symptom.** A provider that passes the §18.3 verification at low
concurrency may still hit account-level rate limits when multiple
chunks fire near-simultaneously, causing the whole phase to abort on
`ErrRateLimited`. (v1.2 chunking typically produces 1–3 chunks per run,
not v1.1's 6 packs — but a heavy `since=lifetime` run with many hard
splits can still produce N≫3 chunks.)

**v1.2 mitigation.**
1. Default `MaxConcurrency=0` → `len(chunks)`; operators can clamp down
   per project. With phase collapse + chunking (§7.2, §7.12), the
   typical `len(chunks)` is 1–3 — far smaller than v1.1's `6` packs.
2. Parallel mode is opt-in. No provider is parallel-by-default in v1.2
   except `copilot-sdk` (provisional).
3. Telemetry: `ProviderUsage.Failures` and `LastError` (§12.1) now record
   the exact rate-limit error string so operators can correlate.

**Graduation criteria.** A provider stays at
`SupportsParallelSessions() == true` only if no rate-limit error appears
in `state.json:provider_usage.<id>.last_error` across a typical week of
daemon runs.

**Owner.** Implementer of the parallel-execution work.

### I4 — Symlink resolution on flaky network shares [provisional]

**Symptom.** A project rooted on an NFS / SMB share with intermittent
mount failures may now return resolver errors during permission checks
that v1 would have silently approved.

**v1.2 stance.** Working as intended. The §4.5 contract says fail-closed
on resolver error — we'd rather deny the call than read past the sandbox
boundary. The deny reason mentions resolution so operators can debug. No
retry, no fallback.

**Owner.** N/A — accepted risk.

### I5 — ACP child hangs (not exits) [provisional]

**Symptom.** An ACP child that's stuck (deadlocked, waiting on remote)
but not exited still ties up the rule timeout. The §4.4 liveness check
fires only on exit, not on hang.

**v1.2 stance.** Out of scope. Detecting hangs reliably across providers
requires per-provider heartbeat semantics we don't yet have. Per-rule
`timeout_seconds` (default 90s for `copilot-sdk`, 45s elsewhere) bounds
the worst case at one rule's budget, not the whole run.

**Owner.** N/A — tracked for a future ACP runtime improvement.

---

## 20. Acceptance criteria — additive

A v1.2 build is acceptable when, on this repository, in addition to v1
§20.1–9 and v1.1 §20.10–13:

14. **Preflight short-circuit.** `time dreamer analyze --path /empty/dir`
    completes in under 1 second wall time on first run, and the run
    invokes zero provider calls. Second run on the same blank folder hits
    the §7.11 cache and exits without re-walking discovery.
15. **Lifetime escape hatch.** `dreamer analyze --since=lifetime --path
    /repo` ingests every discovered chat regardless of age, restoring v1
    behavior. `since: lifetime` in project config behaves identically.
16. **24h default surfaces a notice.** Daemon startup against a project
    whose config omits `since` logs exactly one `since defaulted` line
    per process per project.
17. **Parallel parity (K=1).** A fixture with a single chunk produces
    byte-identical `result.Mistakes` and `result.Findings` under
    `--parallel` and sequential modes on a deterministic fake provider.
    Real-provider parity is tested only structurally (set equality).
18. **Parallel parity (K>1, no chaining).** A fixture forcing K=3 chunks
    produces `result.Findings` equal after sort under `--parallel` and
    sequential-without-chaining modes (chaining is sequential-mode-only;
    see §7.13). Test driver disables chaining in the fake provider for
    this assertion.
19. **Parallel fallback.** `dreamer analyze --parallel --provider
    claude-cli` logs `parallel fallback` and runs sequentially without
    error.
24. **Phase collapse — call count.** A fixture with 6 enabled categories
    and a single chunk produces exactly **2** provider calls
    (1 phase-1 + 1 phase-2), down from up to 12 in v1.1. Verified via a
    fake provider that counts `Run` invocations.
25. **Chunking by byte budget.** With `max_chunk_bytes=100000` and a
    fixture transcript totaling 250000 bytes across three providers
    (sized 120000 / 80000 / 50000), the chunker emits exactly 2 chunks:
    `[provider-1] | [provider-2 + provider-3]`. Phase 1 issues 2 calls.
    Phase 2 issues 1 call. Total: 3 calls.
26. **Chunking — oversize provider hard split.** A single provider block
    of 250000 bytes against `max_chunk_bytes=100000` splits into 3
    chunks all marked `Split=true`, emits one `provider hard split`
    warning, and does not set `ProviderUsage.LastError`.
27. **Chunking — disabled.** `--max-chunk-bytes=0` produces exactly one
    chunk regardless of transcript size, and the run issues exactly 2
    provider calls.
28. **Summary chaining (sequential, K>1).** With K=3 chunks and
    `mode=sequential`, chunk-2's prompt contains chunk-1's `summary`
    string, chunk-3's prompt contains chunk-2's `summary` string, and
    chunk-1's prompt has no `rolling_context` block. Verified via fake
    provider capturing prompts.
29. **Summary chaining — disabled under parallel.** With K=3 chunks and
    `--parallel`, no chunk's prompt contains a `rolling_context` block.
30. **Summary chaining — disabled under K=1.** With K=1 (regardless of
    mode), the chunk's prompt has no `rolling_context` block and the
    response `summary` field is ignored.
31. **Schema violation aborts phase 1.** Sequential mode, K=3 chunks,
    fake provider omits the `summary` field on chunk 1. Pipeline aborts
    phase 1 before issuing chunk 2's call, appends one `schema
    violation chunk=0` warning, increments
    `ProviderUsage.<id>.Failures`, and returns. Chunks 2 and 3 do not
    fire.
32. **Final-chunk summary exemption.** Sequential mode, K=3 chunks,
    fake provider omits `summary` only on chunk 2 (the final chunk).
    Pipeline completes phase 1 without warning; phase 2 fires normally.
33. **Phase-2 grounding is files-only.** Phase-2 prompt captured from
    fake provider contains a `codebase_context` field whose value is a
    file-path list (one path per line, ≤ 500 lines) with no function or
    type names. A fixture project with > 500 files produces a
    truncation warning.
34. **Hard-split warning suggests since.** With `since=lifetime` and a
    transcript that triggers a hard split, the emitted warning string
    matches `consider tightening since (current: lifetime)`.
19. **Symlink sandbox.** A project containing a symlink pointing outside
    its root denies the read permission request for that symlink and
    for any path under it, with a deny reason mentioning symlink
    resolution.
20. **Liveness fail-fast.** A `*-acp` provider whose child exits between
    rules returns `ErrTransportClosed` from the next `session.Run` within
    100 ms, not after the rule timeout.
21. **Atomic state.** Interrupting `dreamer analyze` with SIGTERM during
    `state.Save` leaves `state.json` either fully old or fully new — never
    half-written. (Verified via fixture test that pre-populates
    `state.json.tmp` with garbage and asserts the rename overwrites
    cleanly.)
22. **Per-category staleness.** After a run where one rule times out,
    `state.json:last_run_per_category` shows updated timestamps for
    every completed rule and unchanged timestamps for the timed-out
    rule.
23. **Provider health.** After a run where the provider returns an
    error, `state.json:provider_usage.<id>.last_error` is populated
    (≤ 500 chars), `failures` (or `timeouts`) is incremented, and
    `last_success_utc` is unchanged. After the next clean run,
    `last_error` is empty and `last_success_utc` advances.

---

## 21. Glossary — additive

- **Parallel mode** — orchestrator execution mode that opens N sessions
  against one provider and runs phase-1 **chunks** concurrently. Off by
  default; enabled via `analyzer.execution.mode=parallel` or `--parallel`.
- **Sequential mode** — default execution mode; one session, chunks run
  in chunk-index order with summary chaining (§7.13) when `K > 1`.
- **`SessionPool`** — bounded factory-backed pool of `analyzer.Session`
  instances scoped to one `pipeline.Run`. Cap of 1 in sequential mode;
  cap of `min(MaxConcurrency, len(chunks))` in parallel mode.
- **`Chunk`** — one phase-1 prompt's transcript payload. Created by
  `chunker.Pack` per §7.12 from a byte budget and the discovered provider
  blocks. Chunk count `K` controls phase-1 call count (`K + 1` total
  calls per run, including phase 2).
- **Phase collapse** — v1.2 reduces phase-1 from one call per `RulePack`
  to one multi-category call per chunk; phase 2 from one call per pack
  to a single union-of-mistakes call. Total calls per run drop from up
  to `2N` (N enabled packs) to `K + 1` (K chunks).
- **Summary chaining** — sequential-only behavior where chunk *i*'s
  phase-1 response `summary` field is included as `<rolling_context>` in
  chunk *i+1*'s prompt. Disabled in parallel mode and when `K == 1`
  (§7.13).
- **`MaxChunkBytes`** — transcript-byte budget per chunk; default
  `480000` (~160k tokens at 3 bytes/token). `0` disables chunking.
- **`ProviderBoundaryHeadroom`** — minimum free fraction of
  `MaxChunkBytes` required after packing a provider before the chunker
  appends the next provider into the same chunk; default `0.20`.
- **Oversize provider** — single provider block exceeding `MaxChunkBytes`
  on its own. Hard-split by message boundary, each sub-block becomes its
  own chunk with `Split=true` and emits a `provider hard split` warning
  (§7.12).
- **`ParallelCapable`** — optional provider interface signaling that
  concurrent sessions against the provider are verified safe.
- **`ErrTransportClosed`** — sentinel returned by ACP `session.Run` when
  the underlying child process has exited. Terminal for the current
  pipeline; no restart.
- **Preflight skip** — pipeline exit path taken when the redacted
  transcript has zero messages. Saves state and returns a no-op `Result`
  without invoking the provider.
- **`DefaultSince`** — `"24h"`; lookback window applied when a project's
  `since` is empty.
- **`lifetime`** — opt-out value for `since` that disables lookback
  filtering, restoring v1 behavior.
