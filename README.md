# dreamer

`dreamer` is a single-binary Go CLI that discovers AI-assistant chat transcripts
on disk, redacts secrets, runs them through a configurable provider
(Copilot / Claude / Gemini / Kiro / **Codex**), and appends actionable
prevention guardrails to a per-project `todos.md`.

The product is a **prevention engine**, not a code reviewer. Output answers:
*"what rule / test / doc would have stopped the agent making mistake M
against this repo last week?"*

See [doc/spec.md](doc/spec.md) for the v1 system specification and
[doc/spec.v1.1.md](doc/spec.v1.1.md) for the v1.1 delta (adds Codex).

---

## Quick start

```bash
# 1. build
go build -o dreamer .

# 2. bootstrap config
./dreamer config init        # writes <UserConfigDir>/dreamer/config.yaml

# 3. edit the config: add at least one project + ensure provider authed
#    Linux:   ~/.config/dreamer/config.yaml
#    macOS:   ~/Library/Application Support/dreamer/config.yaml
#    Windows: %AppData%\dreamer\config.yaml

# 4. authenticate provider
copilot                      # then /login        (for copilot-sdk)
claude                       # browser OAuth      (for claude-cli)
codex   login                # ChatGPT OAuth      (for codex-cli)
gemini                       # browser OAuth      (for gemini-cli)
# (gemini-sdk: export GEMINI_API_KEY; ACP variants: configure command path)

# 5. analyze
./dreamer analyze --path /abs/path/to/project

# 6. inspect output
cat <UserConfigDir>/dreamer/<project>/todos.md
```

`config init` accepts `-f` / `--force` to overwrite an existing file.

---

## Commands

### `dreamer analyze`

Run one analysis pass against a single project path.

```bash
dreamer analyze --path /abs/project
                [--provider <id>]
                [--force]
                [--dry-run]
                [--permissive]
                [--output-dir <dir>]
                [--config <path>]
                [--since <window>]
```

| Flag           | Effect                                                                                          |
|----------------|-------------------------------------------------------------------------------------------------|
| `--path`       | **Required.** Absolute project directory (symlinks resolved).                                   |
| `--provider`   | Runtime override. Beats per-project config beats global default.                                |
| `--force`      | Skip the incremental cache and re-analyze every discovered chat.                                |
| `--dry-run`    | Phase 1 only (mistake extraction). Skip guardrail synthesis. Prints the mistake list.           |
| `--permissive` | Disable strict lint-rule allow-list; emit unrecognised rule ids tagged `[unverified]`.          |
| `--output-dir` | Override the per-project output directory.                                                      |
| `--config`     | Override the global config path.                                                                |
| `--since`      | Lookback window (e.g. `30m`, `1h`, `1d`, `1w`, `1mo`).                                          |

### `dreamer daemon`

Run periodic analysis for every project in the global config.

```bash
dreamer daemon [--config <path>]
```

- Uses `daemon.frequency_seconds` from config (default 3600s).
- Runs one cycle immediately, then repeats on that interval.
- Auth failure on one project is logged and the daemon continues with the next.

### `dreamer ls-chats`

List discovered chat sources for a working directory.

```bash
dreamer ls-chats [--project-path <dir>]
```

Discovered sources:

| Source                          | Root                                                                          | Scoping                                                          |
|---------------------------------|-------------------------------------------------------------------------------|------------------------------------------------------------------|
| Copilot CLI sessions            | `~/.copilot/session-state/**/*.jsonl`                                          | unscoped                                                         |
| Codex CLI sessions              | `~/.codex/sessions/**`, `~/.codex/archived_sessions/**`                       | `session_meta.payload.cwd` must resolve inside `--project-path`  |
| VS Code Copilot chat            | `%APPDATA%/Code/User/workspaceStorage/*/chatSessions/*.{json,jsonl}`          | sibling `workspace.json`                                         |
| Claude Code                     | `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/**/*.jsonl`                          | `cwd` / `workingDirectory` probes                                |
| Antigravity / Gemini            | `${GEMINI_HOME:-~/.gemini}/antigravity/{conversations,inbox}/**`               | cwd probe (home root); implicit (project-local root)             |
| Gemini CLI                      | `~/.gemini/sessions/**`                                                       | cwd probe                                                        |
| Kiro CLI                        | `~/.kiro/sessions/**` (SQLite)                                                | cwd probe                                                        |
| OpenCode                        | `~/.opencode/**` (SQLite)                                                     | session metadata                                                 |

### `dreamer config init`

Create the default global config at `<UserConfigDir>/dreamer/config.yaml`.

```bash
dreamer config init [-f|--force]
```

### `dreamer startup` (Windows-only)

Manage a per-user Task Scheduler task that starts the daemon at logon.

```bash
dreamer startup install [--config <path>]
dreamer startup status
dreamer startup uninstall
```

Install from a built `dreamer.exe`, not `go run`, so the task points at a
stable executable.

---

## Providers

Per spec §4.1 + v1.1, dreamer supports a fast primary + ACP fallback per platform:

| Platform | Primary                       | Fallback        | Status      |
|----------|-------------------------------|-----------------|-------------|
| Copilot  | `copilot-sdk`                 | `copilot-acp`   | shipped     |
| Claude   | `claude-cli`                  | `claude-acp`    | shipped     |
| Gemini   | `gemini-sdk` → `gemini-cli`   | `gemini-acp`    | sdk/cli stub, acp shipped |
| Kiro     | `kiro-acp`                    | n/a             | shipped     |
| **Codex** (v1.1) | **`codex-cli`**       | **`codex-acp`** | cli shipped, acp via operator bridge |

Configure each provider under `providers.<id>` in the global config. See
the inline comments produced by `dreamer config init` for valid options
per field. Provider not registered or its CLI missing → exit 1 with an
exact remediation command on stderr.

---

## Output

- Per-project todos: `<UserConfigDir>/dreamer/<project>/todos.md`
- Per-project state: `<UserConfigDir>/dreamer/<project>/state.json`
- Logs:               `<UserConfigDir>/dreamer/dreamer.log`

Each `dreamer analyze` invocation appends a `## Run <RFC3339>` section
containing one bullet per new finding, grouped by category
(`lint-rule`, `test`, `ci-check`, `doc`, `config`, `refactor-boundary`),
with code-block guardrail snippets and `Evidence:` paths. Discovery /
validation skips land in a `## Warnings (Run <ts>)` section.

Findings dedup by `sha256(category | normalized_mistake |
guardrail.tool | guardrail.rule)`. Re-running with no upstream changes
short-circuits to `no changes (cache hit)` and zero provider calls.

---

## Architecture notes

- Read-only sandbox: file reads + URL fetches + read-only shell/MCP allowed,
  writes/edits/deletes denied. Enforced both via the per-session system
  prompt and a generic `analyzer.DecidePermission` handler that adapts
  to each provider's permission protocol.
- Two-phase pipeline: phase 1 extracts recurring mistakes from the
  transcript; phase 2 synthesizes the smallest preventative guardrail
  per category. Phase 2 is skipped when phase 1 produces zero mistakes.
- Rule packs ship embedded under `internal/analyzer/rules/*.yaml`; each
  category defines its own `response_schema`. Projects can override
  per-category toggles via `<project>/.dreamer/rules/<category>.yaml`.
- Redaction (`internal/analyzer/redaction.go`) runs before any text
  reaches the provider, including the ACP transport. Built-in patterns
  cover AWS / GitHub / GitLab / JWT / PEM / Slack / generic env-shaped
  secrets. User-supplied regexes are tagged `[REDACTED:custom]`.

See [doc/spec.md](doc/spec.md) for the canonical contract.

---

## v1.5: setup wizard + web UI

v1.5 (spec: [doc/spec.v1.5.md](doc/spec.v1.5.md)) adds an interactive setup
wizard, an embedded loopback web UI, and per-finding apply/undo.

### Quick start (v1.5)

```bash
dreamer setup            # interactive wizard; writes config.yaml
dreamer daemon           # periodic analysis + embedded web server
dreamer web --open       # open the dashboard in a browser
```

Once `dreamer daemon` is running, the dashboard is at
<http://127.0.0.1:7777>. The UI is loopback-only by design — v1.5 ships
without auth; v1.6 will introduce token auth for remote binds. Apply /
dismiss / resolve actions persist to `state.json`; applied findings record
a reversal (pre-image bytes + pre/post SHA-256) so undo is safe and
refuses (409 Conflict) if the target file has drifted.

### Overlay configuration

- `<UserConfigDir>/dreamer/config.yaml` — operator-owned base config (written by `dreamer setup`).
- `<UserConfigDir>/dreamer/ui-overrides.yaml` — UI-owned overlay merged on top.

The daemon watches both files via fsnotify and hot-reloads on write.
`PUT /api/settings` writes only to the overlay; `config.yaml` is never
rewritten by the daemon. Merge rules: scalars and map keys — overlay
wins; lists (`projects`, `redaction.patterns`) — overlay replaces when
non-empty. See `doc/spec.v1.5.md` for the full schema.
