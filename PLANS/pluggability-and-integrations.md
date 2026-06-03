# Pluggability & Integrations Research

> Status: **Research / Discussion** — no implementation yet.
> Written after a full codebase audit of `internal/mcpserver/`, `internal/pipeline/`, `internal/analyzer/`, `internal/chat/`, `internal/state/`, `internal/output/`, and `internal/config/`.

---

## 1. What the MCP server actually is (clearing up the framing)

Before discussing what to change, it helps to be precise about what "the MCP server" is today, because it is **not** the right target for GitHub/Jira integration.

```
pipeline spawns LLM CLI (claude / gemini / codex / ...)
        │
        │   --mcp-config points the CLI at:
        ▼
  `dreamer mcp-server --output <tmp>.jsonl`   ← dreamer IS the MCP server
        │
        │   LLM calls the `record_finding` tool
        ▼
  FindingRecorder appends JSONL → pipeline reads findings back
```

Dreamer exposes itself as an MCP server (`internal/mcpserver/server.go`) with a **single tool**: `record_finding`. This is an internal transport mechanism: it is how the LLM model hands validated findings back to the dreamer pipeline during Phase 2 analysis. It is not where findings are stored or reported. Replacing it with "GitHub MCP" or "Jira MCP" is aiming at the wrong layer.

Where findings actually land is the `runOutputAndPersist` function in `internal/pipeline/pipeline.go`, which calls:

```go
output.GenerateTodos(...)    // writes todos.md
state.Save(...)              // writes state.json
state.UpdateHistoryToday(...)// writes history.json
```

These three calls are the **real sink** — and today they are hard-coded package-level function calls with no abstraction layer.

---

## 2. Honest pluggability audit

| Subsystem | Score | Mechanism today |
|---|---|---|
| LLM / analysis providers | ✅ Excellent | `analyzer.RegisterProvider` + `RegisterProviderMeta` init-registry, ~16 impls |
| Chat source readers | ✅ Excellent | `chat.registerProvider` init-registry, ~10 impls |
| Provider config defaults | ✅ Good | `config.RegisterProviderDefaults` registry |
| Config / overlay | ✅ Good | 3-layer YAML merge (`config.yaml` → `ui-overrides.yaml` → per-project) |
| MCP tool set | ⚠️ Weak | Single hard-coded `record_finding` tool; `ToolNames` field is already plural (anticipates growth) |
| Phase 2 transport | ⚠️ Weak | Closed `MCP \| CLI` sum-type in `Phase2Config`; adding a third transport requires surgery |
| **Findings sink / reporting** | ❌ None | `GenerateTodos` + `state.Save` called directly — no interface, no registry |
| **Issue-tracker integration** | ❌ None | Greenfield — does not exist anywhere in the codebase |

The good news: the codebase already has **two near-identical, clean, idiomatic pluggability patterns** (`analyzer.Provider` and `chat.SourceProvider`) that work well and can be copied verbatim for any new pluggable subsystem.

---

## 3. The primary gap: a pluggable `FindingSink`

### 3.1 The pattern to copy

Both the analyzer-provider registry and the chat-source registry follow the same shape:

1. A typed `interface` with a small required surface.
2. An optional extension `interface` for advanced capability (e.g. `BatchSizer` in `chat/provider.go`).
3. A `Factory func(cfg) (Interface, error)` type.
4. A package-global `map[ID]Factory` guarded by `sync.RWMutex`.
5. `Register(id, factory)` called from each implementation's `init()`.
6. `New(id, cfg)` / `Lookup(id)` / `Registered()` accessors.
7. `Meta` struct with `DisplayName`, `Order`, capability flags.

This pattern is the template. Any new pluggable subsystem should follow it exactly.

### 3.2 Proposed `FindingSink` interface (new package `internal/sink/`)

```go
package sink

import (
    "context"
    "dreamer/internal/analyzer"
)

// Sink receives the findings produced by a completed run.
// Implementations register via init() exactly like analyzer.Provider.
type Sink interface {
    ID() string
    // Publish is called once per run after analysis completes.
    // Implementations own their idempotency — use finding.Hash as the
    // stable key (it's a SHA-256 over category|mistake|tool|rule).
    Publish(ctx context.Context, run RunInfo, findings []analyzer.Finding) (PublishResult, error)
}

// TwoWaySink is an optional extension for sinks that can report
// external state back into dreamer's lifecycle (e.g. a closed GitHub
// issue → dreamer marks the finding resolved).
// Callers type-assert for it; non-two-way sinks are unaffected.
type TwoWaySink interface {
    Sink
    // Reconcile pulls external status updates and returns lifecycle
    // transitions keyed by finding hash.
    Reconcile(ctx context.Context, hashes []string) ([]StatusUpdate, error)
}

type RunInfo struct {
    ProjectName string
    RunID       string
    RunAt       time.Time
    TodosPath   string // path to generated todos.md, may be empty for GH-only mode
}

type PublishResult struct {
    Published int
    Skipped   int // already existed (idempotent re-runs)
    Errors    []error
}

type StatusUpdate struct {
    Hash   string
    Status string // "resolved", "dismissed", etc. — matches state.FindingStatus*
}
```

The optional `TwoWaySink` follows the exact `BatchSizer` optional-extension pattern already used in `internal/chat/provider.go`.

### 3.3 The injection seam

In `internal/pipeline/pipeline.go`, `runOutputAndPersist`, after `output.GenerateTodos`:

```go
// Fan out to registered, enabled sinks (non-fatal — a failing GitHub
// sink must not prevent local todos.md from being written).
for _, s := range sink.Enabled(cfg.Sinks) {
    if _, err := s.Publish(ctx, runInfo, analysis.result.Findings); err != nil {
        warnings = append(warnings, fmt.Sprintf("sink %s: %v", s.ID(), err))
        logger.Warn("sink publish failed", "sink", s.ID(), "err", err)
    }
}
```

### 3.4 Make `todos.md` one of those sinks

The boldest but cleanest move: treat `builtin-todos` as just another registered sink rather than a special case. Then "GitHub only" is pure config:

```yaml
sinks:
  builtin-todos:
    enabled: false
  github:
    enabled: true
    repo: owner/repo-name
    token_env: GITHUB_TOKEN
    label: dreamer-finding
```

No code fork. No conditional logic in the pipeline. Just config.

---

## 4. Config changes needed

Follow the `Providers map[string]ProviderBlock` precedent on `config.App`:

```go
// config.App addition
Sinks map[string]SinkBlock `yaml:"sinks,omitempty" json:"sinks,omitempty"`

// New type
type SinkBlock struct {
    Enabled     *bool             `yaml:"enabled,omitempty"`
    Repo        string            `yaml:"repo,omitempty"`        // github: "owner/repo"
    TokenEnv    string            `yaml:"token_env,omitempty"`   // env var holding the token
    BaseURL     string            `yaml:"base_url,omitempty"`    // jira: instance URL
    ProjectKey  string            `yaml:"project_key,omitempty"` // jira
    Label       string            `yaml:"label,omitempty"`       // github issue label
    Extra       map[string]string `yaml:"extra,omitempty"`       // escape hatch for custom sinks
}
```

Add `mergeSinks` to `internal/config/overlay.go` mirroring the existing `mergeOverlay` provider-map handling. No viper needed — this is already plain `yaml.v3` everywhere.

---

## 5. GitHub integration design

### 5.1 One-way (dreamer → GitHub Issues)

Effort: **easy-to-moderate** (~1–2 days including tests).

- Finding hash → GitHub issue title tag, e.g. `[dreamer:<short-hash>] <mistake summary>`.
- On `Publish`: search for an open issue with the tag; skip if found (idempotent). Create if new.
- Label all dreamer issues with the configured label for easy filtering.
- Store the issue URL in `PublishResult` for traceability.
- No MCP changes needed.

### 5.2 Two-way (bidirectional sync)

Effort: **moderate-to-hard**.

The complication: dreamer's lifecycle state (`applied / dismissed / resolved` in `state.json`, keyed by SHA-256 finding hash) becomes a second source of truth that can conflict with GitHub issue state (open / closed / labeled).

Design notes:
- **Stable mapping key**: the finding hash is already the right idempotency key. Store `{hash → issue_number}` in a new `state.SinkState` field (or a sidecar JSON file per sink).
- **Reconciliation loop**: `TwoWaySink.Reconcile` is called by the daemon on a configurable cadence (separate from the analysis frequency). It polls GitHub for issues with the dreamer label, maps closed issues back to finding hashes, and returns `StatusUpdate` slices that the pipeline applies to `state.Findings`.
- **Conflict resolution**: "external wins" (GitHub closed = dreamer resolved) is the simplest policy. More nuanced policies (dreamer applied = close issue, reopen if un-applied) can follow later.
- **Webhook alternative**: instead of polling, expose a `/webhook/github` HTTP endpoint in `internal/web/` that receives GitHub issue events. Lower polling cost, but adds an auth surface. Can be deferred.

### 5.3 What "GitHub only" looks like in practice

```yaml
sinks:
  builtin-todos:
    enabled: false        # no todos.md written locally
  github:
    enabled: true
    repo: my-org/my-repo
    token_env: GITHUB_TOKEN
    label: dreamer
```

The user's findings appear as GitHub issues instead of in `todos.md`. The web dashboard (`/api/projects/{name}/findings`) would need to read from the GitHub sink rather than from `todos.md` — this is the trickiest part of "GitHub only" mode, since the web UI currently parses `todos.md` to serve the findings list. Options:
- Keep `todos.md` as a local cache always (but don't commit it / .gitignore it).
- Add a `SinkReader` interface so the web handler can query sinks directly.
- Use `todos.md` as the canonical local view and sync bidirectionally.

This is the main architectural decision to research before implementing.

---

## 6. MCP-layer improvements (lower priority)

These are independent of the sink work and lower impact for adoption.

### 6.1 Make the MCP tool set a registry

Today `internal/mcpserver/server.go` registers exactly one tool with a hard-coded `mcp.AddTool` call. The `ToolNames []string` in `ClientLaunchSpec` is already plural (the comment says "future tools compose without changing the API"). The natural next step:

```go
// mcptool package (or inside mcpserver)
type ToolRegistration struct {
    Name        string
    Description string
    Handler     func(ctx, req, input) (*mcp.CallToolResult, any, error)
}

var registeredTools []ToolRegistration

func RegisterTool(t ToolRegistration) { ... } // called from init()
```

Then `NewServer` iterates `registeredTools`. Adding `record_summary`, `query_findings`, etc. becomes a one-file change with no surgery on `NewServer`.

### 6.2 Open up `Phase2Config` beyond `MCP | CLI`

The current closed sum-type in `internal/analyzer/provider.go` blocks adding a third Phase 2 transport without modifying the struct. Not urgent — `MCP` covers all major CLI providers and `CLI` is the Bash-tool fallback. Worth noting for the future.

---

## 7. Other high-leverage pluggability improvements for adoption

Ranked by adoption impact:

### 7.1 User-extensible rule packs (high value)

Rules are embedded YAML and `internal/categories` is a **closed constant set**. `mcpserver/validation.go`'s `ValidCategories` is derived from those constants. This means users cannot add custom rule categories without forking.

Unlock: allow `.dreamer/rules/*.yaml` to define extra rule packs and categories. The main coupling to break is `ValidCategories` validation — it needs to accept registered categories rather than a static set. The `categories.All()` function is already an indirection; extending it to include user-loaded categories is the lever.

### 7.2 Apply/undo strategy registry (moderate value)

`internal/web/apply` implements a closed set of strategies (`append-section`, `replace-section`, etc.). A registry here lets teams support custom file formats without forking.

### 7.3 Outbound event hooks / webhooks (moderate value)

`internal/pipeline/events.go`'s `EventBus` is in-process only (SSE to the web UI). A thin "on run complete / on finding applied" outbound webhook path — which could itself be a degenerate one-way `Sink` — covers Slack, Discord, CI triggers, etc. without a full integration per destination.

### 7.4 Redaction patterns (already good)

`Redaction.Patterns` in config is already user-configurable. Just needs better documentation as an extension point.

---

## 8. Architecture decision log (open questions for research)

These are the questions to answer before writing any code:

1. **Web dashboard in "GitHub only" mode** — what is the canonical source of truth the `/api/projects/{name}/findings` handler reads from? Options: always keep `todos.md` as a local cache, add a `SinkReader` interface, or drop the dashboard for GitHub-only setups.

2. **Mapping persistence for two-way sync** — where does `{finding_hash → issue_number}` live? Options: a new field on `state.State`, a sidecar `sink-state.json` per project, or an in-memory cache rebuilt on startup from the GitHub API.

3. **Sink failure semantics** — if the GitHub sink fails (rate limit, network down), does the run still succeed? The current proposal says yes (non-fatal, logged warning). Is this always correct, or should certain sinks be configured as "blocking"?

4. **`todos.md` as a built-in sink vs. special case** — treating it as a registered sink is the cleanest design but adds complexity to the existing code path (which has no abstraction). Is the migration cost worth it, or should `todos.md` stay hardcoded and sinks be additive?

5. **Category extensibility coupling** — `mcpserver/validation.go` validates `FindingInput.Category` against `categories.All()`. Opening categories to user-defined values means the model's guardrail category in Phase 2 needs to know about user categories too (it's in the Phase 2 prompt). How does user category metadata flow into the LLM prompt?

6. **Fork vs. plugin binary** — Go doesn't have a built-in plugin system that works cross-platform cleanly (the `plugin` package is Linux-only). The intended extension model for custom sinks is: fork + blank-import the new sink package. Is that the right boundary, or should there be a subprocess/RPC-based plugin interface for truly external plugins?

---

## 9. Summary: what to build, in what order

If the goal is **maximum adoption with minimum disruption**:

| Phase | What | Effort | Value |
|---|---|---|---|
| 1 | `FindingSink` interface + registry in `internal/sink/` | Small | Unlocks everything below |
| 1 | Wire sink fan-out into `runOutputAndPersist` | Small | The injection point |
| 1 | `builtin-todos` sink (wraps current `output.GenerateTodos`) | Trivial | Backward compat |
| 2 | `github` one-way sink (create issues, idempotent) | Moderate | Highest adoption ask |
| 2 | Config `sinks:` block on `config.App` + overlay merge | Small | User-facing config |
| 3 | Web dashboard `SinkReader` interface for GH-only mode | Moderate | Needed for GH-only |
| 3 | `TwoWaySink` reconciliation loop in daemon | Hard | Full bidirectional |
| 4 | MCP tool registry (multi-tool extensibility) | Small | Future-proofing |
| 4 | User-extensible rule packs | Moderate | Power users |
| 4 | Outbound webhook / event sink | Small | Slack/CI notify |

Phase 1 is ~1 day of low-risk refactoring that makes all subsequent phases independent parallel tracks. Phase 2 GitHub one-way is the most-requested feature and unblocks adoption. Phases 3–4 are research-first (see open questions above).
