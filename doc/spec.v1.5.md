# dreamer v1.5 — System Specification

Delta on top of `doc/spec.md` (v1), `doc/spec.v1.1.md`, `doc/spec.v1.2.md`.
When v1.5 conflicts with prior versions, v1.5 wins. Sections not mentioned
here are unchanged.

Status legend: **[locked]** = design adopted and slated for implementation.
**[provisional]** = adopted but pending real-world validation.
**[deferred]** = captured for a future release; ship a TODO marker only.

Visual / design language for the web UI is governed by `DESIGN.md` (Verge
system). Tokens, components, and aesthetic rules in DESIGN.md are
authoritative for every screen this spec introduces.

---

## 1. Summary

v1.5 is a UX / surface-area release. The CLI gains a first-run interactive
setup wizard; the daemon gains an embedded web dashboard that visualizes,
manages, and (within a safe subset) applies the guardrails the analyzer
proposes.

| Area | Change |
|---|---|
| CLI | New `dreamer setup` subcommand — interactive TUI wizard with optional `--advanced` branch. Writes `config.yaml`. |
| Default provider | `DefaultProviderID` flips from `"copilot-sdk"` (v1) to `"openclaude-cli"` (model `mimo-v2.5-pro`). Affects `applyDefaults` only — existing configs with explicit `default_provider:` keep their value. |
| Daemon | New embedded HTTP server bound to `127.0.0.1:<web.port>` (default `7777`). Lifecycle attached to `daemon`. |
| Config | New top-level `web:` block. New runtime overlay `ui-overrides.yaml` merged on top of `config.yaml` at load. Daemon fsnotify-watches both. |
| State | New per-finding state (`Status`, `AppliedAt`, `AppliedReversal`, `DismissedAt`, `ResolvedAt`). New `history.json` daily rollup for the dashboard 30-day sparkline. |
| Phase-2 schema | All rule packs gain an optional `apply: {target_file, strategy, anchor, snippet}` object. UI uses it to render diff + apply. Eligible categories: `doc`, `lint-rule`, `ci-check`, `config`. |
| Output | `todos.md` continues to be authoritative — UI reads + reconciles findings against `state.json`. UI never rewrites `todos.md`. |
| Vision exception | v1 vision §"proposes, does not apply" is partially reversed: the UI may directly write the per-category target files listed above, behind an explicit Apply click + reversible undo. Categories outside the eligible subset remain proposal-only. |

Every change is additive at the schema level. Existing `state.json`,
`config.yaml`, and `todos.md` load and run unchanged.

---

## 2. CLI surface — extended

### 2.1 `dreamer setup` (new)

Interactive TUI wizard. Writes (or overwrites with confirm) the global
`config.yaml` at the path returned by `config.GlobalConfigPath()`.

**Default flow (no flags):**

```
$ dreamer setup
┌─ dreamer setup ────────────────────────────────────────┐
│ 1/4 Provider                                           │
│   Default provider for analysis. (current: openclaude-cli)│
│   > openclaude-cli  OpenClaude CLI (recommended)       │
│     copilot-sdk     GitHub Copilot SDK                 │
│     copilot-acp     Copilot via ACP                    │
│     claude-cli      Anthropic Claude (stream-json)     │
│     gemini-cli      Google Gemini CLI                  │
│     codex-cli       OpenAI Codex CLI                   │
│     ... 6 more (use ↑↓, Enter to select)               │
└────────────────────────────────────────────────────────┘
```

**Steps (default):**

1. **Provider** — single-select from registered provider IDs.
   Default = `openclaude-cli` (v1.5 elevates this over v1's
   `copilot-sdk` default; matches the wizard's recommended bundle).
2. **Model** — single-select from the provider's known-good model list
   (sourced from `internal/analyzer/providers/<id>` registration).
   Default = provider's recommended model — for `openclaude-cli` that
   is `mimo-v2.5-pro`. `Other` option accepts free-form.
3. **Daemon frequency** — integer minutes (default `60`).
4. **Output root** — path picker (default empty = `<UserConfigDir>/dreamer`).
   Accepts `~`. Validated: must be absolute or empty; parent must exist.
5. **Startup install** — yes/no. On yes, invokes
   `dreamer startup install` after writing config (skipped on `--no-startup`).

**Advanced branch (`--advanced` flag OR prompt at end of default flow):**

6. **Log level** — single-select (`error`/`warn`/`info`/`debug`,
   default `info`).
7. **Analyzer rule timeout** — integer seconds (default `120`).
8. **Parallel mode** — yes/no (default no). On yes:
   - **Max concurrency** — integer (default `0` = `len(chunks)`).
9. **Max chunk bytes** — integer (default `480000`).
10. **First project** — yes/no. On yes:
    - **Project path** — path picker, must exist and be absolute.
    - **Project name** — string, default = basename of path.
    - **Lookback (`since`)** — single-select
      (`24h`/`7d`/`30d`/`lifetime`/`custom`).

**Flags:**

| Flag | Effect |
|---|---|
| `--advanced` | Branch directly into the advanced steps after step 5. |
| `--force` | Overwrite existing `config.yaml` without confirmation. |
| `--no-startup` | Skip the step-5 `startup install` invocation. |
| `--non-interactive` | Fail fast if any prompt cannot be answered from flags. Reserved — no v1.5 flag-driven inputs yet, so this always errors unless every step has a default. |

**Idempotence.** Re-running `dreamer setup` against an existing config:
each prompt pre-fills with the current value; user can accept or
overwrite. Comments in the existing file are dropped on rewrite (v1.5
limitation; documented in the wizard's final summary screen, with the
suggestion "edit `~/.config/dreamer/config.yaml` directly to preserve
comments").

**Implementation hint.** Bubbletea (`github.com/charmbracelet/bubbletea`)
+ `bubbles/list`, `bubbles/textinput`. No new transitive deps beyond
charmbracelet stack — already a small ecosystem, widely used in
Go-CLI-of-this-shape territory.

**Acceptance.** `dreamer setup` on a clean machine produces a
syntactically valid `config.yaml` that `config.LoadConfig` accepts
without error, and `dreamer daemon --config <path>` boots successfully
against it.

### 2.2 `dreamer web` (new, thin convenience)

```
$ dreamer web [--open]
```

Prints the web URL (`http://127.0.0.1:<port>`). With `--open`, opens the
URL in the OS default browser (`open` / `xdg-open` / `start`). Does NOT
spawn the daemon — fails with a clear message if the port is not
listening, instructing the user to run `dreamer daemon` (or `systemctl
--user start dreamer` if `startup install` ran).

### 2.3 Other commands — unchanged

`analyze`, `daemon`, `ls-chats`, `startup` are unchanged from v1.2.

---

## 3. Configuration system — extended

### 3.1 Resolution order — extended

```
CLI flag  >  ui-overrides.yaml  >  per-project config.yaml  >  global config.yaml  >  built-in default
```

`ui-overrides.yaml` slots between per-project and CLI. Rationale: the web
UI is a global surface (cross-project), and ad-hoc CLI flags should still
win over UI-persisted choices.

### 3.2 Global config schema — extended

New top-level `web:` block. All other v1.2 blocks unchanged.

```yaml
web:
  # Enable the embedded UI. When false, daemon does not bind a port.
  # Default: true.
  enabled: true

  # TCP port the daemon binds. 0 = pick a free ephemeral port and write
  # the chosen value to <output_root>/web.port for `dreamer web` to read.
  # Default: 7777.
  port: 7777

  # Bind host. v1.5 enforces loopback only — values other than
  # "127.0.0.1" or "localhost" cause config validation to fail. Reserved
  # for v1.6 to relax behind a token-auth flag.
  host: "127.0.0.1"

  # Maximum kilobytes of dreamer.log returned by GET /api/logs/tail.
  # Default: 256.
  log_tail_kb: 256
```

Go types:

```go
type Config struct {
    // ...existing fields from v1.2...
    Web WebConfig `yaml:"web,omitempty"`
}

// WebConfig governs the daemon's embedded HTTP server. Enabled is a
// pointer so applyDefaults can distinguish "unset" (flip to true) from
// explicit "enabled: false" (preserve). Same pattern as
// ProviderBlock.UseLoggedInUser in v1.2.
type WebConfig struct {
    Enabled   *bool  `yaml:"enabled,omitempty"`
    Port      int    `yaml:"port,omitempty"`
    Host      string `yaml:"host,omitempty"`
    LogTailKB int    `yaml:"log_tail_kb,omitempty"`
}
```

`applyDefaults`: when `Web.Enabled == nil`, set to `&true`. When
`Web.Port == 0`, set to `7777`. When `Web.Host == ""`, set to
`"127.0.0.1"`. When `Web.LogTailKB == 0`, set to `256`. Explicit
`enabled: false` survives untouched.

**Default provider change.** `internal/config/loader.go:18`
`DefaultProviderID` flips from `"copilot-sdk"` to `"openclaude-cli"`.
`applyDefaults` and `ResolveProviderConfig` consult this constant when
`cfg.DefaultProvider` is empty. Configs that set
`default_provider: copilot-sdk` (or any other value) explicitly are
unaffected — the change only steers the empty-string fallback. The
wizard's recommended bundle for new users is `openclaude-cli` +
`mimo-v2.5-pro` (§2.1 step 2).

`Validate` rejects any `Web.Host` that resolves to a non-loopback address
(`Host` is parsed and compared against `127.0.0.0/8` + `::1` + the literal
strings `"localhost"`, `"127.0.0.1"`, `"::1"`).

### 3.3 `ui-overrides.yaml` overlay — new

A second YAML file at `<UserConfigDir>/dreamer/ui-overrides.yaml`. Same
schema as `config.yaml`. Owned by the web UI.

**Merge rules.**

- Loaded after `config.yaml` via `config.LoadConfigWithOverlay(globalPath,
  overlayPath) (*Config, error)`. Missing overlay file is a silent no-op
  (not an error).
- Scalar fields: overlay value wins when present (non-zero).
- Map fields (`providers`, `analyzer.rules`): overlay merges per-key;
  per-key the overlay value wins.
- List fields (`projects`, `redaction.patterns`): overlay **replaces**
  the entire list when non-empty. Operators who edit `projects` by hand
  in `config.yaml` are warned in the wizard summary that subsequent UI
  edits to the project list will replace, not merge, the list.

**Write rules.**

- UI's `PUT /api/settings` writes ONLY to overlay. `config.yaml` is never
  rewritten by the daemon.
- Overlay is rewritten atomically (temp file + `os.Rename`, mirroring
  v1.2 state save).
- Comments are not preserved across writes; the overlay is treated as
  daemon-owned.

**Reload.**

- The daemon registers `fsnotify` watches on `config.yaml` and
  `ui-overrides.yaml`. On either WRITE event:
  - Re-load via `LoadConfigWithOverlay`.
  - On parse error: keep the in-memory config, log a warning at
    `error` level, do **not** crash. Next successful save retriggers.
  - On parse success: swap atomically into the daemon's config
    `*atomic.Pointer[Config]`. Subsequent analyze loops pick up new
    values on the next tick.
- Settings that are only honored at startup (e.g. `web.port`,
  `web.host`) emit a `restart required for <field>` warning and do not
  hot-reload. The UI surfaces this as a banner.

### 3.4 Per-project config — unchanged

### 3.5 Per-category rule config — unchanged

### 3.6 Config notices — extended

`ConfigNotices` gains:

```go
type ConfigNotices struct {
    DefaultedSince     []string // unchanged from v1.2
    OverlayApplied     bool     // true when ui-overrides.yaml was non-empty
    OverlayParseError  string   // last overlay load error, empty when clean
    RestartRequired    []string // field names that changed but need restart
}
```

The daemon logs `overlay applied path=<path> keys=<list>` on each
successful reload.

---

## 4. Web server — new

### 4.1 Lifecycle

The web server is a goroutine inside `cmd/daemon.go`. Lifecycle:

1. Daemon constructs the server with the current `*Config`.
2. Server binds `web.host:web.port`. On `port: 0`, chooses an ephemeral
   port and writes the chosen value to `<output_root>/web.port` (file
   contents = decimal integer + newline). `dreamer web` reads this file.
3. Server runs `http.Server.Serve` on a `net.Listener`.
4. Daemon's signal handler calls `server.Shutdown(ctx)` on
   SIGTERM/SIGINT with a 5-second grace.
5. Bind failure (e.g. port in use): daemon logs an `error` and continues
   without the UI. `analyze` cycles unaffected.

### 4.2 Routing

```
GET  /                          → SPA shell (HTMX-driven)
GET  /static/*                  → embedded assets (CSS, JS, fonts) via go:embed
GET  /api/dashboard             → aggregate stats JSON
GET  /api/projects              → list of configured projects + per-project rollup
GET  /api/projects/{name}       → project detail (overview tab data)
GET  /api/projects/{name}/findings    → findings list (filter: status, category)
GET  /api/projects/{name}/findings/{hash} → finding detail (incl. diff preview)
POST /api/projects/{name}/findings/{hash}/apply    → apply solution
POST /api/projects/{name}/findings/{hash}/undo     → revert last apply
POST /api/projects/{name}/findings/{hash}/dismiss  → mark dismissed
POST /api/projects/{name}/findings/{hash}/resolve  → mark resolved (manual)
POST /api/projects/{name}/run                      → enqueue immediate analyze
GET  /api/projects/{name}/chats                    → chats tab data
GET  /api/projects/{name}/history                  → past runs (history.json slice)
GET  /api/providers                                → provider health table
GET  /api/settings                                 → current effective config (merged)
PUT  /api/settings                                 → write overlay
GET  /api/logs/tail                                → last N KB of dreamer.log
GET  /api/events                                   → SSE stream of daemon events
GET  /api/fs/exists?path=<abs>                     → 200 {"exists":bool, "is_dir":bool} (path picker validator)
POST /api/daemon/restart                           → graceful self-exit (relies on systemd/Task Scheduler to respawn)
```

**Content negotiation.** Each `/api/*` handler returns JSON
(`Content-Type: application/json`) by default. When the request includes
`HX-Request: true` (HTMX), the handler returns an HTML partial rendered
via Go `html/template`. The router shares the same data layer between
both representations to avoid duplication.

**Auth.** v1.5 ships no auth. The bind-host check in §3.2 enforces
loopback. v1.6 will add token auth when non-loopback binds are allowed.

**CSRF.** State-changing routes (POST/PUT) require either:
- A custom header `X-Dreamer-CSRF: <token>` matching the token rendered
  into the SPA shell at boot, OR
- `Origin: http://127.0.0.1:<port>` (loopback origin check).

Both must hold. The SPA carries the token in a meta tag and injects it
via HTMX's `htmx:configRequest` event.

**SSE event channel.** `/api/events` streams:

```
event: run.start
data: {"project":"dreamer","at":"2026-05-20T12:00:00Z"}

event: run.done
data: {"project":"dreamer","at":"...","findings_new":3,"chunks":2,"calls":3}

event: finding.applied
data: {"project":"dreamer","hash":"a1b2...","target":"/abs/CLAUDE.md"}

event: config.reloaded
data: {"overlay":true,"restart_required":["web.port"]}
```

The dashboard subscribes via the HTMX SSE extension and updates the
"last run" card + activity panels live.

### 4.3 Frontend stack

- **Templates.** Go `html/template`, embedded via `go:embed` under
  `internal/web/templates/`.
- **Interactivity.** HTMX (`htmx.org` v2.x) + Alpine.js (`alpinejs` v3.x)
  for local component state (modal toggles, tabs). Both shipped as
  bundled assets in `internal/web/static/vendor/` to avoid runtime CDN
  dependency.
- **CSS.** Hand-written, scoped to `internal/web/static/css/`. Tokens
  match `DESIGN.md` (Verge: Canvas Black `#131313`, Jelly Mint
  `#3cffd0`, Verge Ultraviolet `#5200ff`, Manuka headlines, PolySans
  Mono labels). Fonts: open-source substitutes documented in DESIGN.md
  §3 — **Anton** (Manuka stand-in, +0.10 line-height adjustment per
  DESIGN.md §3 note), **Space Grotesk** (PolySans stand-in), **JetBrains
  Mono** (PolySans Mono stand-in). All fonts shipped under
  `internal/web/static/fonts/` via `go:embed`; no CDN, no Google Fonts
  request.
- **Build step.** None. CSS is hand-authored, JS vendored. `go build`
  embeds everything via `//go:embed all:templates all:static`.

---

## 5. Information architecture — new

### 5.1 Sidebar

Two groups, in order:

```
— PROJECTS —
  <project-1>           [active = mint pill]
  <project-2>
  + add project          → opens "Add Project" modal
— SYSTEM —
  Dashboard
  Settings
  Logs
  Providers
```

Top right: status pill ("daemon · running"/"daemon · idle") + Run Now
button (POST `/api/projects/{name}/run` for the active project, or all
on the Dashboard view).

### 5.2 Pages

**Dashboard (system-level, aggregate).**

- 5 top stat cards: Chats analyzed / Findings open / Last run / Tokens
  per week / Fail rate.
- Second row left: 30-day findings sparkline + per-category breakdown
  (lint-rule, doc, test, config, ci-check, refactor-boundary counts).
- Second row right: Top mistake providers (per-tool ranking) + Avg
  per-project run duration.
- Live activity strip (SSE-driven): last 5 events from `/api/events`.

**Project page (tabs: Overview / Findings / Chats / History).**

- **Overview.** Same 5 cards as Dashboard but scoped to project. Mini
  "next run in <Xm>" pill.
- **Findings.** Table: kicker (category, confidence), summary,
  status, last seen. Status filter chips (Open / Applied / Dismissed
  / Resolved). Click row → detail modal (§6).
- **Chats.** List of discovered chat sources for the project. Columns:
  tool, path, last modified, message count, included? (yes/no per
  `since`). Filterable by tool.
- **History.** Reverse-chronological list of past runs from
  `state.json` + `history.json`. Each row: timestamp, mode (sequential
  /parallel), chunks, calls, new findings, duration. Click → detail
  pane with per-category counts.

**Settings page.**

Sections (each writes to overlay):
- Provider (default, per-id model + flags)
- Daemon (frequency_seconds, output_root — readonly note: restart
  required)
- Analyzer (rule_timeout_seconds, execution mode, max_concurrency,
  max_chunk_bytes, per-category enable toggles)
- Logging (level, file)
- Redaction (patterns — textarea, one per line)
- Web (port, host, log_tail_kb — readonly note: restart required)

**Logs page.** Tail of `dreamer.log` (last `LogTailKB` KB), auto-scroll
toggle, level filter chips (`error`/`warn`/`info`/`debug`).

**Providers page.** Table of every registered provider: ID, model,
runs, last success, last error, failures, timeouts, healthy badge.
Pulls from `state.ProviderUsage`.

### 5.3 Empty / error states

- No projects configured → Dashboard renders a centered card prompting
  `Add your first project` with a button that opens the modal.
- Daemon hasn't run yet → cards show `—`, sparkline shows
  `no data yet`, footer reads `next run in <Xm>` from
  `daemon.frequency_seconds`.
- Overlay parse error → red banner at top of every page:
  `Settings overlay has a parse error: <last_error>. Daemon is using
  pre-error values.`

---

## 6. Findings model — extended

### 6.1 New per-finding state

`state.State.Findings map[string]FindingState` (keyed by finding hash,
matching the `<!-- dreamer:finding:<hex> -->` markers already in
`todos.md`).

```go
// FindingState tracks the lifecycle of a single finding. Open findings
// are absent from the map (zero value semantics). Once the user
// transitions a finding out of open, an entry exists here for the
// remainder of its life — including resurrection by a later analysis
// run.
type FindingState struct {
    // Status: "applied" | "dismissed" | "resolved".
    Status string `json:"status"`

    // AppliedAt is set when Status="applied". Cleared on Undo.
    AppliedAt time.Time `json:"applied_at,omitempty"`

    // AppliedReversal captures the information needed to undo an apply.
    // See §6.3. Nil when Status != "applied" or when undo has already
    // been consumed.
    AppliedReversal *FindingReversal `json:"applied_reversal,omitempty"`

    // DismissedAt / ResolvedAt analogous to AppliedAt for their states.
    DismissedAt time.Time `json:"dismissed_at,omitempty"`
    ResolvedAt  time.Time `json:"resolved_at,omitempty"`

    // ProjectName is the project this finding belongs to. Stored here
    // so a single state.json (in v1, scoped per project) plus a global
    // index would still be correct — but in v1.5 state remains
    // per-project, and ProjectName equals the directory name. Kept for
    // future cross-project rollups.
    ProjectName string `json:"project_name,omitempty"`
}
```

`state.State` gains:

```go
type State struct {
    // ...existing v1.2 fields...
    Findings map[string]FindingState `json:"findings,omitempty"`
}
```

Backwards compatibility: missing field deserializes to nil map; state
load normalizes to empty map.

### 6.2 Transition rules

| From | To | Trigger | Effect |
|---|---|---|---|
| open | applied | `POST /apply` | Write target file; record reversal; set `AppliedAt` |
| open | dismissed | `POST /dismiss` | Set `DismissedAt` |
| open | resolved | `POST /resolve` | Set `ResolvedAt` |
| applied | open | `POST /undo` | Revert target file; clear `AppliedReversal`; remove map entry |
| dismissed | open | `POST /undismiss` | Remove map entry |
| resolved | open | `POST /unresolve` | Remove map entry |
| any | (auto) | Next analyze run | If finding hash no longer appears in fresh output AND state was `open`, finding is dropped from in-memory list. State entries (applied/dismissed/resolved) persist. |

The pipeline already dedupes by `(category, description)` hash. v1.5
extends the dedupe filter: findings whose hash is in
`state.Findings` with `Status != ""` are filtered out before phase-2
synthesis if `Status == "dismissed"` (we already decided we don't want
them). For `applied` / `resolved`, the finding may legitimately
re-surface if the apply didn't stick — the UI shows them with a
"recurred after apply" badge and the row sorts to the top.

### 6.3 Apply reversal

```go
type FindingReversal struct {
    // Path is the absolute path to the file that was modified.
    Path string `json:"path"`

    // Strategy mirrors the apply strategy used (§6.4) — informational.
    Strategy string `json:"strategy"`

    // PreImageSHA256 is the SHA-256 of the file's contents before the
    // apply. Undo refuses if the current file SHA does not match the
    // post-image SHA (the file changed after dreamer wrote it; we
    // can't safely revert).
    PreImageSHA256  string `json:"pre_sha"`
    PostImageSHA256 string `json:"post_sha"`

    // PreImage is the literal file contents before apply. Hard cap
    // 4 MiB; targets larger than that are rejected at apply time with
    // 413 (see §6.5) so PreImage is always sufficient to undo.
    PreImage string `json:"pre_image,omitempty"`
}
```

Undo flow:

1. Read current target file → compute SHA.
2. If SHA != `PostImageSHA256` → return 409 Conflict
   `{"error": "target file has changed since apply; refusing undo"}`.
3. Write `PreImage` atomically (temp + rename).
4. Remove `FindingState` entry.

### 6.4 Apply schema on phase-2 output

All six rule pack YAMLs gain an optional `apply` object on each
`guardrail`:

```yaml
# Example excerpt added to doc.yaml's guardrail_prompt_template
{"findings": [
  {
    "mistake": "...",
    "guardrail": {
      "kind": "doc",
      "tool": "<filename>",
      "rule": "<heading>",
      "config_snippet": "<paragraph>",
      "apply": {
        "target_file": "<repo-relative path>",
        "strategy": "append-section | insert-after | replace-section | append-file | replace-file",
        "anchor": "<header text, line number, or pattern — strategy-dependent>",
        "snippet": "<exact text to write/insert>"
      }
    },
    ...
  }
]}
```

**Eligibility gate.** The `apply` object is only honored by the UI for
categories in the eligible set:

| Category | Eligible? | Default target hint to phase-2 prompt |
|---|---|---|
| `doc` | yes | `CLAUDE.md`/`AGENTS.md`/`README.md`/`docs/**` |
| `lint-rule` | yes | toolchain-detected lint config (`.golangci.yml`, `.eslintrc.*`, `pyproject.toml`, etc.) |
| `ci-check` | yes | `.github/workflows/*.yml`, `.gitlab-ci.yml` |
| `config` | yes | toolchain-detected config files |
| `test` | no | UI shows snippet but no Apply button |
| `refactor-boundary` | no | UI shows snippet but no Apply button |

For ineligible categories or findings missing `apply`, the UI hides the
Apply button and shows only the snippet block with a "manual" badge.

**Path containment.** Before any write, the server resolves
`apply.target_file` against the project root (the same root used by the
read-only analyzer permission handler, including v1.2's symlink-aware
containment). Targets outside the project root are rejected with 400.

**Strategy semantics:**

- `append-section`: append `\n\n## <anchor>\n\n<snippet>\n` at file
  end. If a section with `anchor` already exists, becomes
  `replace-section`.
- `insert-after`: locate the literal line `anchor` (exact match
  trimmed), insert `\n<snippet>\n` immediately after. 404 if anchor
  not found.
- `replace-section`: replace the markdown section starting at `##
  <anchor>` (through next `##` or EOF) with `<snippet>`.
- `append-file`: append `\n<snippet>\n` at EOF unconditionally.
- `replace-file`: overwrite entire file with `<snippet>`. Reserved for
  config files where the snippet IS the full content.

### 6.5 Apply implementation

`internal/web/apply/` package. `Apply(target, strategy, anchor,
snippet)` returns `(FindingReversal, error)`. Atomic via temp file +
rename. SHA-256 pre/post recorded.

**Size cap.** Targets whose pre-image exceeds 4 MiB are rejected with
HTTP 413 `{"error":"target file too large for safe apply (4 MiB cap)"}`.
The cap exists so `PreImage` in the reversal record is always
sufficient to undo — no diff/patch library is needed in v1.5. Operators
with legitimately large config files can edit them manually.

**`append-section` auto-promotion.** If the file already contains a
`## <anchor>` heading, the strategy silently promotes to
`replace-section`. The reversal's `Strategy` field records the
promoted value (`"replace-section"`), not the requested one, so undo
restores the prior section verbatim.

No third-party diff/patch library is required for v1.5.

---

## 7. Dashboard data plumbing — new

### 7.1 `history.json` daily rollup

New file at `<output_root>/<project>/history.json`. Updated by the
pipeline at the end of every successful run.

```go
type History struct {
    Version int           `json:"version"`
    Days    []DaySummary  `json:"days"` // sorted ascending, max 90 entries
}

type DaySummary struct {
    Date           string `json:"date"`            // "2026-05-20"
    Runs           int    `json:"runs"`
    FindingsNew    int    `json:"findings_new"`
    FindingsTotal  int    `json:"findings_total"`
    Tokens         int64  `json:"tokens"`
    AvgRunMillis   int64  `json:"avg_run_millis"`
    PerCategory    map[string]int `json:"per_category"`
}
```

Pipeline updates the entry whose `Date` equals today's UTC date,
creating it if absent. Entries older than 90 days are pruned on each
write. Atomic save via temp + rename.

### 7.2 Aggregate dashboard endpoint

`GET /api/dashboard` returns the union of every project's
`state.json` + `history.json`:

```json
{
  "stats": {
    "chats_analyzed_total": 128,
    "chats_discovered_total": 142,
    "findings_open": 37,
    "findings_applied": 23,
    "findings_dismissed": 9,
    "findings_resolved": 0,
    "last_run_utc": "2026-05-20T11:48:00Z",
    "next_run_utc": "2026-05-20T12:48:00Z",
    "tokens_week": 1432000,
    "failure_rate_pct": 3,
    "avg_run_seconds": 42,
    "providers_healthy": "2/2"
  },
  "sparkline_30d": [/* DaySummary[] */],
  "per_category": {"lint-rule": 14, "doc": 9, ...},
  "top_mistake_providers": [{"tool": "cursor", "count": 18}, ...],
  "live_activity": [/* last 5 events */]
}
```

Computed server-side per request. No caching in v1.5; cost is bounded
(O(projects × bytes(state.json + history.json))).

### 7.3 Avg per-project run time

`AvgRunSeconds` in `/api/dashboard` is the rolling 7-day average of
`history.json:days[].avg_run_millis` across all projects, weighted by
`runs`.

Per-project `Overview` tab shows the same metric scoped to that
project's `history.json`.

---

## 8. Settings page — UI semantics

### 8.1 Section ordering

1. Provider — default + per-id model picker.
2. Daemon — frequency, output root (readonly + tooltip "restart
   required").
3. Analyzer — timeout, execution mode, max_concurrency, max_chunk_bytes,
   per-category toggles (6 switches).
4. Logging — level, file path (readonly + tooltip).
5. Redaction — patterns textarea.
6. Web — port, host, log_tail_kb (all readonly + tooltip).

### 8.2 Save semantics

- Every section has its own `Save` button. Saving a section issues
  `PUT /api/settings` with a partial body containing **only the fields
  the section owns**. The handler reads the current overlay, merges the
  partial body into it (overlay-merge rules per §3.3), and writes the
  result. Fields not present in the request body are left untouched.
  Clearing a field (reverting to default) requires the explicit
  sentinel `{"<field>": null}` in the request body.
- After save, the daemon's fsnotify watcher fires; reload happens via
  the same code path as a hand edit. SSE emits `config.reloaded`. If
  any field in `restart_required` changed, a banner appears with
  `[Restart daemon]` button → `POST /api/daemon/restart` (graceful
  self-exit; systemd / Task Scheduler respawns).

### 8.3 Per-project edits

Settings → Projects subsection lists configured projects with inline
edit (path, since, name) + remove button. Add Project modal posts to
the same endpoint. Path field has a validator that hits
`GET /api/fs/exists?path=<>` to confirm the directory resolves.

---

## 9. Web — security posture

- Loopback-only bind (§3.2 enforced at config validate time).
- No auth (loopback assumption).
- CSRF via custom-header token + Origin check on all state-changing
  routes (§4.2).
- Apply containment: target file must resolve under the project root
  (§6.4) using the v1.2 symlink-aware resolver (§4.5). Targets outside
  are 400 rejected.
- Snippet content is **never executed** by the daemon — it is plain
  text written verbatim into the target file. There is no shell-out,
  no script eval, no template substitution at apply time.
- `Content-Security-Policy: default-src 'self'; style-src 'self'
  'unsafe-inline'; script-src 'self'; font-src 'self'`. Vendored
  htmx/alpine + locally-served fonts satisfy this without `unsafe-eval`.
- Server reads `state.json` and `todos.md` directly. No code paths
  read user-supplied paths into file responses except the explicit
  whitelist in §4.2.

---

## 10. Logging — extended

New events:

| Event | Fields |
|---|---|
| `web start` | `host`, `port`, `bind_addr` |
| `web stop` | `reason`, `uptime_s` |
| `overlay reloaded` | `path`, `defaulted_count`, `restart_required` |
| `finding applied` | `project`, `hash`, `target`, `strategy` |
| `finding undone` | `project`, `hash`, `target`, `reason` |
| `finding dismissed` | `project`, `hash` |
| `finding resolved` | `project`, `hash` |
| `history rolled` | `project`, `date`, `findings_new`, `pruned_days` |
| `apply rejected` | `project`, `hash`, `reason` (containment / strategy error) |

---

## 11. Directory & file layout — additive

```
cmd/
  setup.go                       # new — TUI wizard cobra command
  web.go                         # new — `dreamer web` thin command
  daemon.go                      # extended — spawn web server goroutine
internal/
  config/
    overlay.go                   # new — LoadConfigWithOverlay, merge rules
    loader.go                    # extended — applyDefaults for Web, Notices fields
  state/
    tracker.go                   # extended — Findings map, FindingState, FindingReversal
    history.go                   # new — History/DaySummary, atomic save, 90-day prune
  web/
    server.go                    # new — http.Server, routing, lifecycle, CSRF, SSE hub
    handlers/
      dashboard.go               # new — GET /api/dashboard
      projects.go                # new — projects + project detail
      findings.go                # new — list/detail/apply/undo/dismiss/resolve
      settings.go                # new — GET/PUT settings (overlay write)
      logs.go                    # new — log tail
      providers.go               # new — provider health
      events.go                  # new — SSE hub
      run.go                     # new — POST /run
    apply/
      apply.go                   # new — strategy implementations, reversal capture
      apply_test.go              # new
    templates/                   # new — html/template, go:embed
      layout.html
      dashboard.html
      project_overview.html
      project_findings.html
      project_chats.html
      project_history.html
      settings.html
      logs.html
      providers.html
      modals/*.html
    static/
      css/dreamer.css            # new — Verge-token implementation
      js/app.js                  # new — Alpine bootstrapping, HTMX config, CSRF wiring
      vendor/
        htmx.min.js              # vendored
        alpine.min.js            # vendored
      fonts/                     # vendored substitutes (Anton, Space Grotesk, JetBrains Mono)
  pipeline/
    pipeline.go                  # extended — write history.json at end of run,
                                 # filter dismissed findings before phase-2 input,
                                 # mark recurred-after-apply for UI badge
  analyzer/
    rules/
      *.yaml                     # extended — guardrail schema gains optional `apply` object
    orchestrator.go              # extended — parse `apply` field into Finding struct
ui/
  (none in repo — frontend lives under internal/web/)
```

No new top-level packages outside `internal/web/`. `setup.go` lives in
`cmd/` alongside the other Cobra commands.

---

## 12. End-to-end run sequence — updated

Replaces v1.2 §17. New / changed steps marked `[v1.5]`.

**At daemon startup:**

1. **Load config.** `[v1.5]` `LoadConfigWithOverlay(globalPath,
   overlayPath)`. Populate `Config.Notices.OverlayApplied` and
   `OverlayParseError` accordingly.
2. **`[v1.5]` Spawn web server goroutine** if `web.enabled` and the
   bind succeeds. Log `web start`. On bind failure, log `error` and
   continue without UI.
3. **`[v1.5]` Spawn fsnotify watcher** for `config.yaml` and
   `ui-overrides.yaml`. On WRITE: reload, atomic swap, emit SSE
   `config.reloaded`.
4. **Begin analyze ticker** at `daemon.frequency_seconds`.

**At each analyze tick** (per project, otherwise unchanged from v1.2):

5–22. Steps 1–21 from v1.2 §17 unchanged, EXCEPT:

- **After step 8 (Redact), before step 9 (Preflight skip):** `[v1.5]`
  Filter findings already in `state.Findings` with status
  `"dismissed"`. The pipeline still consumes the new transcript, but
  the orchestrator's dedupe set is pre-loaded with dismissed hashes so
  phase-2 won't re-emit them.
- **After step 19 (Render):** `[v1.5]` Update
  `<output_root>/<project>/history.json` with today's `DaySummary`
  (runs++, findings_new += N, etc.).
- **Emit SSE `run.done`** after step 21.

**At UI-triggered Run Now (`POST /run`):**

23. **`[v1.5]`** Push an immediate `pipeline.Run` for the named
    project on a worker goroutine. Returns 202 Accepted with run ID.
    SSE channel reports `run.start` and `run.done`.

**At UI-triggered Apply (`POST /apply`):**

24. **`[v1.5]`** Lookup finding by hash in current project's findings
    cache (rebuilt from `todos.md` + state on every settings reload,
    or on demand if stale).
25. **`[v1.5]`** Validate `apply.target_file` resolves inside project
    root (symlink-aware per §4.5 v1.2 rules).
26. **`[v1.5]`** Read target file → compute pre-SHA, capture preimage.
27. **`[v1.5]`** Apply strategy. Write atomically (temp + rename).
    Compute post-SHA.
28. **`[v1.5]`** Update `state.Findings[hash] = FindingState{Status:
    "applied", ...}`. Save state (atomic per v1.2 §12.2).
29. **`[v1.5]`** Emit SSE `finding.applied`. Return 200 with the new
    FindingState.

---

## 13. Acceptance criteria — additive

A v1.5 build is acceptable when, in addition to v1 §20.1–9, v1.1
§20.10–13, and v1.2 §20.14–34:

35. **Setup wizard happy path.** On a clean machine,
    `dreamer setup` produces a `config.yaml` accepted by
    `config.LoadConfig`. `dreamer daemon --config <path>` boots
    against it.
36. **Setup wizard advanced branch.** `dreamer setup --advanced`
    walks steps 1–10, persists logging level + analyzer
    timeout + parallel mode + first project. The written config
    reflects every chosen value.
37. **Setup wizard idempotence.** Re-running `dreamer setup` against
    an existing config pre-fills every prompt with the current value
    and overwrites only fields the user changed.
38. **Web server lifecycle.** With `web.enabled: true`, the daemon
    binds `127.0.0.1:7777` within 200 ms of startup and serves
    `GET /` returning a 200 with the SPA shell.
39. **Web server bind failure isolates.** With `web.port` already in
    use, the daemon logs `error` and continues running analyze cycles
    without the UI; `dreamer web` returns the documented "daemon UI
    not running" message.
40. **Loopback enforcement.** `web.host: 0.0.0.0` in config fails
    `Validate` with a clear error mentioning loopback-only.
41. **CSRF.** A `POST /api/projects/x/findings/y/apply` from
    `Origin: http://evil.example` returns 403.
42. **Overlay merge.** A `ui-overrides.yaml` with
    `default_provider: claude-cli` overrides a `config.yaml` that
    sets `default_provider: copilot-sdk`; the daemon picks
    `claude-cli` for new analyze cycles.
43. **Overlay hot-reload.** Writing a new `ui-overrides.yaml` while
    the daemon is running triggers a `config.reloaded` SSE event
    within 500 ms and the new value takes effect on the next analyze
    tick.
44. **Overlay parse error tolerated.** Writing invalid YAML to
    `ui-overrides.yaml` does not crash the daemon; the pre-error
    in-memory config remains active and `OverlayParseError` is set.
45. **Apply happy path.** `POST /apply` on a `doc` finding with
    `apply.target_file=CLAUDE.md` writes the snippet, sets
    `state.Findings[hash].Status="applied"`, captures
    `AppliedReversal`, returns 200.
46. **Apply containment.** `POST /apply` on a finding whose
    `apply.target_file` resolves outside the project root returns
    400 and writes nothing.
47. **Apply ineligibility.** `POST /apply` on a `test` or
    `refactor-boundary` finding returns 400 with reason
    `category not eligible for apply`.
48. **Undo happy path.** `POST /undo` immediately after `apply`
    restores the file to its pre-image SHA-256 and removes the
    `FindingState` entry.
49. **Undo refuses external edits.** A user-edit to the target file
    between apply and undo makes undo return 409 with the
    documented message.
50. **Dismiss filters future runs.** A finding marked `dismissed`
    does not reappear in `GET /api/projects/{name}/findings` after
    the next analyze tick — even when the underlying mistake pattern
    is still in the transcript.
51. **Resolve is purely cosmetic.** A finding marked `resolved`
    that still matches in the next analyze tick re-appears with a
    `recurred after resolve` badge in the UI and sorts to the top.
52. **Dashboard sparkline.** With 7 days of run history, the
    sparkline on `/` shows 7 data points, and the `avg_run_seconds`
    field reflects the rolling 7-day weighted mean.
53. **History pruning.** `history.json:days` is capped at 90
    entries; the 91st write prunes the oldest entry.
54. **SSE delivery.** A browser connected to `/api/events` receives
    a `run.done` event within 1 second of a `POST /run` cycle
    completing.
55. **`dreamer web --open`.** On a system with `xdg-open` available
    and the daemon running, the command spawns the browser to the
    correct URL and exits 0.
56. **Apply size cap.** `POST /apply` against a target file > 4 MiB
    returns 413 and writes nothing; `state.Findings` is not mutated.
57. **`append-section` auto-promotion.** Applying `append-section` to
    a file already containing the same anchor heading rewrites the
    existing section and records `Strategy="replace-section"` in the
    reversal; subsequent undo restores the original section text
    verbatim.
58. **Partial settings save.** `PUT /api/settings` with body
    `{"logging":{"level":"debug"}}` updates only `logging.level` in
    the overlay; other overlay keys are preserved byte-for-byte.
59. **`enabled: false` survives applyDefaults.** A `config.yaml`
    containing `web: {enabled: false}` boots the daemon with the
    web server disabled (no bind, no listener).
60. **Default provider fallback.** A `config.yaml` omitting
    `default_provider:` resolves to `openclaude-cli` at runtime;
    `dreamer analyze --path /repo` invokes `openclaude-cli` with
    model `mimo-v2.5-pro` unless `--provider` is passed.

---

## 14. Glossary — additive

- **`dreamer setup`** — the new interactive TUI wizard for first-run
  configuration; writes `config.yaml`.
- **Web UI / dashboard** — the HTMX+Alpine browser interface served
  by the daemon at `127.0.0.1:<web.port>`.
- **Overlay** — `ui-overrides.yaml` at
  `<UserConfigDir>/dreamer/ui-overrides.yaml`. Daemon-owned. Merged on
  top of `config.yaml` per §3.3. Written exclusively by the web UI.
- **Apply** — UI action that takes a finding's
  `guardrail.apply` object, writes the snippet to the target file,
  records reversal, transitions the finding to `applied`. Eligible
  categories: `doc`, `lint-rule`, `ci-check`, `config`.
- **Undo** — reverse of Apply. Refuses if the target file changed
  after apply (SHA mismatch).
- **Dismiss / Resolve** — UI-driven finding state transitions.
  Dismiss = "I don't want to see this again"; Resolve = "I fixed
  this manually, mark it done". Dismiss filters future runs;
  Resolve does not.
- **Recurred badge** — UI marker on `applied` or `resolved`
  findings whose hash re-appeared in a later analyze run.
- **`FindingState`** — per-finding lifecycle struct in `state.json`.
- **`FindingReversal`** — captured preimage + SHA-256 hashes
  required to undo an apply.
- **`history.json`** — per-project daily rollup of run metrics.
  Capped at 90 entries.
- **SSE event channel** — `GET /api/events` server-sent-events
  stream for live UI updates.
- **Restart required** — settings (e.g. `web.port`, `web.host`,
  `daemon.frequency_seconds`) that cannot hot-reload and surface a
  banner + `Restart daemon` button on save.

---

## 15. Open issues

### I6 — Comment preservation in `setup` rewrite [provisional]

**Symptom.** Re-running `dreamer setup` overwrites `config.yaml`,
stripping operator-added comments.

**v1.5 stance.** Accepted limitation. Mitigated by:
1. The wizard's final summary screen warns the user.
2. `ui-overrides.yaml` (daemon-managed) never touches `config.yaml`
   for normal UI edits — so post-setup, comments survive indefinitely.

**v1.6 candidate.** Switch to `yaml.v3` `Node` API for round-trip
preservation, if user demand emerges.

**Owner.** Wizard implementer.

### I7 — `apply` schema adoption across all rule packs [provisional]

**Symptom.** v1.5 ships the `apply` schema in all six rule pack
prompts, but model quality at producing a clean `target_file` +
`strategy` varies. `lint-rule` and `doc` packs are most reliable;
`config` and `ci-check` may emit non-existent paths.

**v1.5 stance.** UI's existence check rejects invalid paths at apply
time with a 400; user can manually edit `target_file` via the
"Edit Snippet" button before re-clicking Apply (which posts the
edited snippet + target back to the server).

**v1.6 candidate.** Per-pack apply-success telemetry; auto-disable
Apply for packs whose first-attempt success rate falls below a
threshold.

**Owner.** Rule pack maintainer.

### I8 — Cross-project rollup performance [provisional]

**Symptom.** `GET /api/dashboard` reads every project's `state.json`
+ `history.json` per request. With N projects, request latency is
O(N) disk reads.

**v1.5 stance.** Acceptable for typical N ≤ 20. Above 20, the
dashboard may need server-side caching keyed by file mtimes. Not
shipped in v1.5.

**Owner.** N/A — accepted risk.

### I9 — UI write-back race vs. running analyze [provisional]

**Symptom.** A user-clicked Apply could land while pipeline.Run is
mid-flight against the same project, racing on filesystem state
(unlikely to write the same target file, but possible for
`CLAUDE.md` if the analyzer's read sandbox snapshots it before our
write).

**v1.5 stance.** Apply does not block on the pipeline. The analyzer
runs read-only against a stable filesystem from its own perspective;
a target-file mutation between phase-1 and phase-2 only affects the
*next* analyze cycle, never the current one. No locking introduced.

**Owner.** N/A — accepted risk.
