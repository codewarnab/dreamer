# Dreamer Data Flow

This document traces how data moves through `dreamer` end-to-end — from user intent (CLI command or OS scheduler trigger) through discovery, analysis, output, and persistence, and back to the web UI. It is the authoritative reference for understanding inputs, outputs, and transformation boundaries for every major subsystem.

---

## 1. Overview

`dreamer` has three entry points that all converge on the same core pipeline:

| Entry Point | Path |
| :--- | :--- |
| **CLI `analyze`** | User → `cmd/analyze.go` → `pipeline.Run` |
| **Daemon loop** | Timer/signal/stop-file → `cmd/daemon.go` → `jobqueue.Queue` → worker → `pipeline.Run` |
| **Background jobs** | OS scheduler → `dreamer jobs run` → `backgroundjobs.Executor.Run` → provider subprocess |

The common currency is:

```
Chat sources → redacted transcript → LLM provider → Mistakes → Findings → todos.md + state.json
```

---

## 2. Configuration Loading

Configuration is assembled before any pipeline work starts. It uses a two-layer overlay model.

```
<UserConfigDir>/dreamer/config.yaml      (operator-owned base)
        +
<UserConfigDir>/dreamer/ui-overrides.yaml  (web UI writes here only)
        │
        ▼
config.App  (merged in-memory struct, atomic.Pointer for hot-reload)
        +
<projectPath>/.dreamer.yaml              (per-project overrides: provider, since, rules)
        │
        ▼
discoveryResult.providerBlock  (final resolved provider config for this run)
```

**Merge rules:**
- Scalar fields: overlay replaces base when non-zero.
- Map fields: deep merge — overlay keys win over base keys.
- List fields: overlay completely replaces the base list (no append).

**Hot-reload path (daemon only):** `fsnotify` watches both config files. On a write event, the daemon reloads both files, merges them into a fresh `config.App`, and does an atomic CAS swap on `live *atomic.Pointer[config.App]`. A `config.reloaded` event is published on the `EventBus` so SSE clients update.

**Key config fields consumed by pipeline:**
- `daemon.output_root` → base directory for all output files
- `daemon.frequency_seconds` / `daemon.max_concurrent_jobs` → daemon scheduling
- `analyzer.execution.mode` / `analyzer.chunking.max_chunk_bytes` → transcript packing
- `analyzer.rule_timeout_seconds` → per-rule LLM timeout
- `providers.*` → LLM provider SDK config (model, binary path, API keys)
- `projects[].path`, `projects[].since`, `projects[].provider` → per-project overrides

---

## 3. Chat Source Discovery

**Package:** `internal/chat/`

Discovery maps a project path to a list of `chat.Source` records — one per chat session file or database row.

```
OS environment vars                    ~/.config, $APPDATA, $XDG_DATA_HOME
        │
        ▼
chat.DefaultDiscoveryEnvironment()     (DiscoveryEnvironment struct)
        │
        ▼  parallel errgroup (one goroutine per registered provider)
┌────────────────────────────────────────────────────────┐
│ Provider.Discover(env, projectPath) — per provider:    │
│  claude-code   → JSONL files in ClaudeConfigDir        │
│  copilot       → JSONL files under CopilotHome         │
│  codex         → JSONL session files                   │
│  gemini-cli    → JSONL session files                   │
│  vscode        → VSCode chat session files             │
│  opencode      → SQLite DB rows  (<dbPath>#<sessionID>)│
│  kiro-cli      → SQLite DB rows  (<dbPath>#<sessionID>)│
│  codebuff      → JSON session files                    │
│  antigravity   → IDE & CLI session files (index & scan)│
└────────────────────────────────────────────────────────┘
        │
        ▼
[]chat.Source (sorted: newest ModifiedTime first)
        │
        ▼  lookback filter (--since / project.since window)
[]chat.Source  (time-filtered, scoped to projectPath)
```

**`chat.Source` fields:** `Path`, `Tool` (SourceType), `ModifiedTime`, `ParentID` (subagent sessions)

For SQLite-backed providers (opencode, kiro-cli), `Source.Path` is encoded as `<dbFile>#<sessionID>` and split at read time by `SplitSQLiteSourcePath`. For Antigravity CLI, `conversation_summaries.db` indexes `workspace_uris` to map UUIDs directly to disk transcripts (`brain/<uuid>/.system_generated/logs/transcript.jsonl`), with a fallback CWD probe across unindexed transcripts.

---

## 4. Cache Check

**Package:** `internal/pipeline/`, `internal/state/`

Before any LLM calls, the pipeline checks two caches to avoid redundant work.

```
[]chat.Source
        │
        ▼
sourceContentHash(src)                 per-source SHA-256
  ├─ SQLite-backed (opencode, kiro):   provider SourceHash digest of that
  │    session's row content only      (`<db>#<session>` path is split via
  │                                    SplitSQLiteSourcePath; never opened)
  └─ all other sources:                state.HashFile(source.Path)
        +
state.RepoHeadSHA(projectPath)         git HEAD commit hash
        │
        ▼
state.ChatCacheKey(path, hash, headSHA)  length-prefixed composite key
        │
        ▼
Compare with state.ChatHashes           (map[sourcePath]cacheKey from state.json)
        │
   ┌────┴───────────────────────────┐
   │ all keys match AND             │
   │ headSHA unchanged?             │
   └────────────────┬───────────────┘
        │ YES: cache hit              │ NO: cache miss
        ▼                             ▼
  publishRunDone (0 findings)   continue to transcript prep
  return early
```

SQLite-backed providers store every session as a row inside one shared database file, so `Source.Path` encodes `<dbFile>#<sessionID>`. Hashing that literal path would always fail and any write to the shared file would invalidate every session at once. Instead, providers implement `chat.SourceHasher`: opencode fingerprints message/part aggregates plus `session.time_updated` (`readers.OpenCodeReader.SessionFingerprint`), and kiro-cli fingerprints the row's value length plus `updated_at` (`readers.KiroReader.ConversationFingerprint`). A change to one session therefore re-analyzes only that session.

**Discovery cache (daemon only):** `pipeline.DiscoveryCache` is a coarser mtime-based cache. Before even hashing files, the daemon checks whether any source mtime has changed since the last run. A cache hit skips the full content-hash loop entirely.

---

## 5. Transcript Preparation

**Package:** `internal/pipeline/`, `internal/analyzer/`

Chat messages are decoded, redacted, and packed into chunks for the LLM.

```
[]chat.Source
        │
        ▼
chat readers (per SourceType):
  JSONL reader     → []reader.Message  (role, content, timestamp)
  SQLite reader    → []reader.Message  (via opencode/kiro SQLite adapters)
  JSON reader      → []reader.Message  (codebuff)
        │
        ▼  subagent filtering (includeSubagentTranscripts config flag)
        │
        ▼
analyzer.Redactor (regex + list-based secret scrubber)
  • strips API keys, tokens, passwords from message content
  • counts redaction hits (logged + persisted to UsageStats)
        │
        ▼
[]pipeline.ProviderBlock   (one block per source, with byte count)
        │
        ▼
pipeline.PackChunks(blocks, ChunkingConfig, since)
  • respects max_chunk_bytes limit
  • inserts hard Split markers when a single source exceeds the limit
  • annotates each chunk with its time window
        │
        ▼
[]analyzer.Chunk  (each chunk = subset of ProviderBlocks + metadata)
```

**Rule pack assembly** runs in parallel with transcript prep:

```
config.App.Rules (global built-ins)
        +
<projectPath>/.dreamer/rules/<category>.md   (project overrides)
        +
category enable/disable toggles
        │
        ▼
[]analyzer.RulePack  (one pack per enabled rule category)
```

---

## 6. Analysis — Two-Phase LLM Pipeline

**Package:** `internal/analyzer/`

The orchestrator runs two sequential LLM phases against each chunk. Phase 1 and Phase 2 can be sequential or parallel (parallel requires provider support).

### Phase 1 — Mistake Extraction

```
[]analyzer.Chunk  +  []RulePack  +  PhaseRequest
        │
        ▼
PromptBuilder.BuildPhase1Prompt(chunk, rulePack)
        │
        ▼
provider.NewSession(SessionConfig{ReadOnly:true, Sandbox:mode})
  → OS sandbox wrapper (bubblewrap/seatbelt/Win32 Job Object)
        │
        ▼
LLM provider subprocess (claude, copilot, gemini, etc.)
        │
        ▼
raw LLM response text
        │
        ▼
phase1 decoder: extract []Mistake per RuleCategory
  • normalize text, filter by confidence threshold
        │
        ▼
map[RuleCategory][]Mistake  (merged across all chunks)
```

**Phase 1 cache:** If Phase 1 succeeds but Phase 2 subsequently fails, the mistakes are serialized into `state.CachedPhase1` (with a cache key derived from transcript + rule pack hash). On the next run, if the cache key still matches, Phase 1 is skipped and mistakes are replayed directly into Phase 2.

### LLM Call Capture (`internal/capture/`, enabled by default)

Every phase-1 and phase-2 call is persisted **before any parse result is
consumed**, so a malformed response like `invalid phase-1 JSON` keeps its raw
body instead of being dropped with only a warning string.

```
orchestrator.RunConfig.Capture  (analyzer.CallCapture hook)
        │  one CapturedCall per LLM exchange: prompt, raw response,
        │  run error, parse error, elapsed, chunk index/count
        ▼
pipeline.callRecorder  (internal/pipeline/capture.go)
        • maps errors → status: ok | parse_failed | error
        • redacts secret-shaped text from the response
        • truncates fields to analyzer.capture.max_record_kb
        ▼
<outputRoot>/<projectName>/runs/<runID>/
├── meta.json     RunMeta: provider, model, sandbox, system message,
│                 project path, since window, replay lineage
└── calls.jsonl   append-only Record per call (index assigned by Writer)
```

Retention: `analyzer.capture.retain_runs` prunes oldest run dirs after each
run (default 20; -1 keeps everything). Disable entirely with
`analyzer.capture.enabled: false`. Replay reads this directory back:
`internal/replay` re-parses a stored response with the current rule packs
(`reparse`) or resends the stored prompt through a new session with an
optional provider/model override (`resend`), writing resend results as a new
run tagged `kind=replay` with `parent_run_id` + `parent_call_index` lineage.

### Phase 2 — Finding Synthesis

```
map[RuleCategory][]Mistake
        │
        ▼
PromptBuilder.BuildPhase2Prompt(mistakes, rulePack, phaseReq)
        │
        ▼  Phase 2 transport selection (per provider):
┌────────────────────────────────────────────────────────────┐
│ Phase2ModeNone (default/dry-run):                          │
│   inline JSON in prompt response                           │
│                                                            │
│ Phase2ModeCLI (CLI providers: claude-code, codex, etc.):   │
│   dreamer record-finding --output <temp.jsonl>              │
│   provider calls the tool; findings written to temp file   │
│                                                            │
│ Phase2ModeMCP (MCP-capable providers):                     │
│   mcp-server spawned on stdio                               │
│   provider calls record_finding MCP tool                   │
│   findings validated + written to temp JSONL file          │
└────────────────────────────────────────────────────────────┘
        │
        ▼
[]unvalidatedFinding  (from inline JSON or temp JSONL file)
        │
        ▼
orchestrator.validateAndBuildFindings:
  • validate category against enabled RulePack allow-list
  • compute ComputeFindingHash (stable SHA of normalized text)
  • filter against existingHashes (dismissed + already-seen findings)
  • apply Redactor to finding text fields
        │
        ▼
[]analyzer.Finding  (deduplicated, validated, hashed, redacted)
```

**`analyzer.Finding` key fields:** `Category`, `Summary`, `EvidenceExcerpt`, `CodebaseEvidence[]`, `ApplySpec` (optional), `Hash`

---

## 7. Output Generation

**Package:** `internal/output/`

Findings are merged into the project's `todos.md` file.

```
[]analyzer.Finding
        │
        ▼
output.GenerateTodos(projectName, findings, GenerateOptions)
        │
        ▼
read existing <outputRoot>/<projectName>/todos.md
        │
        ▼
extractExistingFindingHashes(existingContent)
  → map[hash]struct{}  (already-present finding hashes)
        │
        ▼
filterNewFindings(findings, existing)
  → only truly new findings pass through
        │
        ▼
renderRunSection(newFindings, runAt, runID)
  • grouped by category heading (## Mistakes / ## Guardrails / etc.)
  • each finding rendered with: summary, evidence excerpt, codebase refs
  • each finding tagged: <!-- dreamer:finding:<hash> -->
        │
        ▼
mergeContent(existing, header, newSections)
  • idempotent merge: new run section prepended, old sections preserved
        │
        ▼
fsutil.WriteFileAtomic(<outputRoot>/<projectName>/todos.md)

Output: GenerateResult { Path, AddedFindings }
```

---

## 8. State Persistence

**Package:** `internal/state/`

After output generation, the pipeline updates and saves `state.json` for the project.

```
state.State (loaded at run start from <outputRoot>/<projectName>/state.json)
        │
        ▼  mutated during runOutputAndPersist:
  LastRunUTC             ← time.Now().UTC()
  RepoHeadSHA            ← git HEAD at run start
  ChatHashes             ← map[sourcePath → cacheKey]  (new values from this run)
  FindingHashes          ← merged: prior hashes + new finding hashes
  Findings               ← per-hash FindingState map (lifecycle: applied/dismissed/resolved)
  LastRunPerCategory     ← map[category → completionTime]
  ProviderUsage          ← per-provider run counts, tokens, last error
  UsageStats             ← cumulative counters: sources_analyzed, messages_analyzed,
                           mistakes_found, findings_added, redaction_hits
  CachedPhase1           ← cleared on full success; set if Phase 2 failed
        │
        ▼
state.Save → fsutil.WriteFileAtomic(<outputRoot>/<projectName>/state.json)
        │
        ▼
stateCache.Invalidate(projectName)   (in-memory cache bust for web handler reads)
```

**History** is a separate file updated once per run day:

```
state.UpdateHistoryToday → <outputRoot>/<projectName>/history.json
  DaySummaryDelta: Runs+1, FindingsNew, FindingsTotal, RunDurationMillis, PerCategory
  Pruned to 90 days of history.
```

---

## 9. Daemon Execution Loop

**Package:** `cmd/daemon.go`, `internal/jobqueue/`

The daemon wraps the pipeline in a scheduling layer with concurrency control.

```
config.App.Projects[]  (list of registered project paths)
        │
        ▼  every frequency_seconds tick
enqueueMissingJobs:
  for each project NOT already in queue (active job = pending or running):
    queue.Enqueue(project, EnqueueConfig{Path, Provider, Since})
        │
        ▼
jobqueue.Queue  (in-memory + persisted to <outputRoot>/jobs.json)
  • dedup per project: one active job at a time
  • wake channel signals idle workers
        │
        ▼  worker pool (max_concurrent_jobs goroutines)
worker.Dequeue() → pipeline.Run(ctx, Options{...})
        │
    ┌───┴───────────────────────┐
    │ success                   │ failure
    ▼                           ▼
queue.Complete(findings,    queue.Failed(err)
  messages, sources)
```

**Job lifecycle states:** `pending → running → completed | failed | timed_out | cancelled`

The queue persists its job list to `<outputRoot>/jobs.json` after every state transition. On daemon restart, `queue.Recover()` reloads the list and `queue.ReapRunning()` marks any zombie `running` entries as `failed`.

---

## 10. Background Jobs Engine

**Package:** `internal/backgroundjobs/`

Background jobs are distinct from the daemon's project queue — they run user-defined prompts via OS-native schedulers rather than dreamer's own timer loop.

```
jobs.json  (<outputRoot>/background-jobs/jobs.json)
  Job { ID, Name, Prompt, ProjectPath, ProviderID, Schedule, Permissions, ... }
        │
        ▼  OS scheduler (Task Scheduler / systemd / launchd / cron)
dreamer jobs run <jobID> --run-token-file <path>
        │
        ▼
backgroundjobs.Executor.Run(ctx, jobID):
  1. Validate run token against <storeDir>/run.token   (auth check)
  2. Load Job from store (acquire store.lock)
  3. Validate permissions (file access mode, writable paths)
  4. Resolve provider config
  5. Build system message (job prompt + project context)
  6. provider.NewSession(SessionConfig{Phase2, Permissions, Sandbox})
  7. Execute job prompt in sandboxed LLM subprocess
  8. Capture stdout/stderr to <storeDir>/runs/<jobID>/<runID>.log
  9. Update Run record (status, duration, output summary, activity log)
 10. Persist Run to runs store
        │
        ▼
backgroundjobs.RunStore  (<storeDir>/runs/)
  Run { ID, JobID, Status, StartedAt, FinishedAt, ProviderID, LogPath,
         SandboxStatus, ActivitySummary, ... }
        │
        ▼
backgroundjobs.AuditWriter  (<storeDir>/audit.jsonl)
  Append-only audit log of every run start/complete/fail event
```

**Store locking:** All `jobs.json` mutations go through `Store.Update()`, which acquires `store.lock` (cross-process file lock + in-process mutex) before read–modify–write, then releases.

**Reconciler:** On daemon startup and on `dreamer jobs reconcile`, the `Reconciler` compares `jobs.json` against the OS scheduler and repairs drift: installs missing tasks, removes orphan tasks, and updates stale schedule hashes.

**Windows scheduling hardening (scheduler_windows.go):**
- Task definition XML is written UTF-8 without an XML declaration (schtasks requires UTF-16LE if an encoding declaration is present); job paths (`\Dreamer\BackgroundJobs\<id>`) are guarded by `ValidateJobID` on every entry point (Install/Update/Remove/Inspect).
- Config/run-token paths are quoted in the task `<Arguments>` so spaces don't split the command line; all interpolated text passes through XML escaping.
- `<ExecutionTimeLimit>` for interval schedules is 80% of the interval (floored at 5m) so a hung run cannot suppress subsequent fires under `MultipleInstancesPolicy=IgnoreNew`.
- `Inspect` reads enabled state from definition XML, then fetches Next/LastRunTime via `/Query /V /FO CSV` parsed by column index — runtime times never appear in definition XML, and column indexes are stable across locales (unlike localized headers).
- `ListOwn` propagates real schtasks errors (access denied etc.) instead of treating them as "folder missing", so orphan detection never silently disables.
- Invalid schedule fields fail loudly at trigger-build time (no silent PT1H fallback); unknown weekdays return errors instead of panicking.
- Advisory warnings for timezone mismatch vs machine-local time and DST-observing zones are produced by `backgroundjobs.ScheduleWarnings` and surfaced through CLI stderr and web API `warnings` arrays on create/edit.

---

## 11. Web UI Data Flows

**Package:** `internal/web/`, `internal/web/handlers/`

The web server is a loopback-only HTTP server embedded in the daemon. It serves the SPA and a REST API. All state reads go through `state.StateCache` (mtime-invalidated, in-memory).

### Read Path (Dashboard, Projects, Findings)

```
Browser GET /api/projects/{name}/findings
        │
        ▼
web handler → stateCache.GetState(outputRoot, projectName)
  cache HIT:  return cached *state.State
  cache MISS: state.Load(outputRoot, projectName) → cache store
        │
        ▼
filter findings by status / category query params
        │
        ▼
JSON response  ([]FindingState + ApplySpec metadata)
```

### Write Path (Settings)

```
Browser PUT /api/settings  { body: partial config override }
        │
        ▼
handler validates fields
        │
        ▼
write to <UserConfigDir>/dreamer/ui-overrides.yaml   (atomic write)
        │
        ▼
fsnotify event → startConfigWatcher goroutine
        │
        ▼
config.LoadConfig + config.ApplyOverlay → new config.App
        │
        ▼
live.Store(newConfig)   (atomic.Pointer CAS swap)
        │
        ▼
events.Publish(config.reloaded)  → SSE broadcast to all clients
```

The Settings write path also carries **per-category prompt overrides**. The rules
panel reads embedded defaults from `GET /api/rule-defaults` (sourced from
`analyzer.LoadDefaultRulePacks()`, never the overlay) to show placeholder/reset
text, and a `PUT /api/settings` body may set `analyzer.rules.<category>.{mistake_prompt_template,
guardrail_prompt_template, phase1_category_description}`. `mergePartial` folds these
into the overlay's existing rule map recursively (siblings like `severity` survive),
and a `null` value deletes the key so the embedded pack default applies again at
`applyRuleToggles`.

### Apply / Undo Path (Finding Remediation)

```
Browser POST /api/projects/{name}/findings/{hash}/apply
        │
        ▼
handler reads FindingApplySpec from state.Findings[hash]
  (server-trusted; request body apply fields are ignored)
        │
        ▼
web/apply.Apply(Request{ProjectRoot, TargetFile, Strategy, Anchor, Snippet})
  containment check: TargetFile must be inside ProjectRoot
  size check: target file must be ≤ 4 MiB
        │
        ▼
apply strategy:
  append-section   → add new heading/bullet block to end of file
  replace-section  → swap existing block under heading
  insert-after     → inject after matching anchor token
  append-file      → append lines to bottom
  replace-file     → overwrite entire file
        │
        ▼
fsutil.WriteFileAtomic(targetFile, postImage)
        │
        ▼
FindingReversal { Path, Strategy, PreImageSHA256, PostImageSHA256, PreImage }
        │
        ▼
state.Findings[hash] = FindingState{Status:"applied", AppliedReversal: reversal}
state.Save → state.json
stateCache.Invalidate(projectName)
        │
        ▼
events.Publish(finding.applied)  → SSE broadcast

Undo path (POST /undo):
  load reversal from state.Findings[hash].AppliedReversal
  verify SHA-256(currentFile) == PostImageSHA256   (drift check → 409 Conflict)
  fsutil.WriteFileAtomic(targetFile, reversal.PreImage)
  state.Findings[hash].Status = ""  (cleared)
  events.Publish(finding.undone)
```

### Server-Sent Events (SSE)

```
Browser GET /api/events  (long-lived connection)
        │
        ▼
web handler → events.Subscribe(bufSize) → chan Event
        │
        ▼  pipeline and handlers publish to EventBus:
  run.start      → project run began
  run.done       → run complete (findings count, sources, messages)
  run.error      → run failed
  finding.applied / undone / dismissed / resolved
  config.reloaded
  job.created / deleted / paused / resumed / updated
  job.run.start / job.run.done
  project.removed
  chat.deleted
        │
        ▼
SSE stream to browser  (slow subscribers drop events; no back-pressure)
```

---

## 12. MCP Server & `record-finding` Tool

**Package:** `internal/mcpserver/`

For CLI-mode providers (claude-code, codex, etc.) that support tool use, Phase 2 findings are delivered out-of-band rather than in the inline LLM response.

```
analyzer: spawn provider subprocess with Phase2Config
        │
Phase2ModeCLI:
  provider subprocess calls:
    dreamer record-finding --output <tempFile.jsonl> [finding JSON flags]
        │
        ▼
  cmd/recordfinding.go → mcpserver.FindingRecorder.Record(input)
  validation → append finding to tempFile.jsonl
        │
Phase2ModeMCP:
  analyzer: spawn  dreamer mcp-server --output <tempFile.jsonl>
        │
  provider subprocess calls MCP tool: record_finding(input)
        │
        ▼
  mcpserver.findingHandler.RecordFinding → FindingRecorder.Record(input)
  validation → append finding to tempFile.jsonl
        │
Both modes converge:
        ▼
  orchestrator reads tempFile.jsonl after provider exits
  → []unvalidatedFinding  → validateAndBuildFindings  → []Finding
  temp file cleaned up by phase2Cleanup() defer
```

**`FindingInput` validation rules** (enforced by `mcpserver.Validate`):
- Category must be a known rule category
- Target file must be inside project root (containment)
- Apply strategy must be a supported enum value
- Snippet must not exceed size cap

---

## 13. OS Sandbox Containment

**Package:** `internal/sandbox/`

Every LLM provider session is wrapped in a platform-specific OS sandbox before the subprocess is spawned. This is a pure system edge — data entering the sandbox is the system message + prompt; data exiting is the raw LLM response text (or findings written to temp files).

```
analyzer.SessionConfig { WorkingDirectory, ReadOnly, Sandbox, Phase2 }
        │
        ▼
sandbox.New(mode, projectPath, writablePaths)
        │
  ┌─────┴─────────────────────────────────────────────────┐
  │ Linux:   bwrap  (read-only bind mount, no network,     │
  │          user/pid namespace isolation, seccomp filter) │
  │ macOS:   sandbox-exec SBPL profile (read-only FS,      │
  │          no socket binds, Mach service blocks)         │
  │ Windows: restricted SID token + Win32 Job Object       │
  │          (deny-write ACL, process tree lifecycle)      │
  └───────────────────────────────────────────────────────┘
        │
        ▼
sandboxed provider subprocess
  INPUT:  system message + prompt (via stdin / named pipe / args)
  OUTPUT: response text (via stdout)  OR  findings temp file (Phase 2 CLI/MCP)
```

---

## 14. File System Layout

All persisted data is rooted at `<outputRoot>` (configured via `daemon.output_root`).

```
<outputRoot>/
├── dreamer.daemon.lock           daemon PID lock
├── dreamer.log                   structured daemon log
├── jobs.json                     jobqueue: in-flight analysis job states
├── web.port                      ephemeral port (--port 0 mode)
│
├── <projectName>/                one directory per registered project
│   ├── state.json                per-project run cache and finding lifecycle
│   ├── history.json              per-day run summaries (90-day rolling)
│   ├── todos.md                  finding output (append-merged each run)
│   └── runs/<runID>/             LLM call capture (see §6)
│       ├── meta.json             session metadata (provider, model, sandbox,
│       │                         system message, replay lineage)
│       └── calls.jsonl           one captured prompt/response per line
│
└── background-jobs/              background job engine store
    ├── store.lock                cross-process lock for jobs.json mutations
    ├── jobs.json                 persisted Job definitions
    ├── audit.jsonl               append-only execution audit log
    ├── run.token                 OS scheduler auth token
    └── runs/
        └── <jobID>/
            ├── <runID>.log       full provider stdout/stderr for this run
            └── <runID>.activity.jsonl  process/network event log
```

---

## 15. Data Flow Summary Diagram

```
CLI flags / OS scheduler trigger
        │
        ▼
config.App  ◄── config.yaml + ui-overrides.yaml + .dreamer.yaml
        │
        ▼
[Validation]  project path must exist and be a directory;
              resolved provider id must be registered (fail fast, exit ≠ 0)
        │
        ▼
[Discovery]  chat.DiscoverChats(env, projectPath)
        │          └─ parallel provider scans → []chat.Source
        │
        ▼
[Cache Check]  HashFile × sources + RepoHeadSHA → compare state.ChatHashes
        │          └─ HIT: return early (no LLM call)
        │
        ▼
[Transcript Prep]  decode messages → redact secrets → PackChunks
        │          └─ []analyzer.Chunk  +  []RulePack
        │
        ▼
[Phase 1]  LLM in sandbox: chunk + rules → raw response → []Mistake
        │          └─ parallel or sequential per chunk
        │
        ▼
[Phase 2]  LLM in sandbox: mistakes → findings (inline / CLI / MCP tool)
        │          └─ validate, hash, dedup, redact → []Finding
        │
        ▼
[Output]  MergeTodos → todos.md  (idempotent, hash-tagged finding blocks)
        │
        ▼
[State]  Save state.json  (ChatHashes, FindingHashes, FindingState lifecycle,
        │                   ProviderUsage, UsageStats, LastRunPerCategory)
        │
        ▼
[History]  UpdateHistoryToday → history.json  (per-day delta)
        │
        ▼
[Events]  EventBus.Publish(run.done)  → SSE → web browser clients
```

