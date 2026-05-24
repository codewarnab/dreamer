<p align="center">
  <img src="docs/assets/dreamer-eye.svg" alt="dreamer" width="550">
</p>

<p align="center">
  <img src="docs/assets/dreamer-eye.svg" alt="dreamer" width="550">
</p>

`dreamer` is a single-binary Go CLI that discovers AI-assistant chat
transcripts on disk, redacts secrets, runs them through a configurable
provider (Copilot / Claude / Gemini / Kiro / Codex / OpenClaude / Codebuff /
OpenCode), and appends actionable prevention guardrails to a per-project
`todos.md`.

The product is a **prevention engine**, not a code reviewer. Output answers:
*"what rule / test / doc would have stopped the agent making mistake M
against this repo last week?"*

Specs: [v1](doc/spec.md) | [v1.1](doc/spec.v1.1.md) (Codex) |
[v1.2](doc/spec.v1.2.md) (perf + hardening) |
[v1.5](doc/spec.v1.5.md) (setup wizard + web UI)

---

## Quick start

```bash
# 1. build
make build                     # release build (~18MB, stripped, -trimpath)

# 2. interactive setup (writes config.yaml)
./dreamer setup

# 3. add a project
./dreamer add /path/to/project

# 4. start the daemon in the background
./dreamer start

# 5. open the web dashboard
./dreamer web --open           # http://127.0.0.1:7777

# 6. check job status
./dreamer status
```

Or run a one-shot analysis without the daemon:

```bash
./dreamer analyze --path /abs/path/to/project
```

---

## Commands

### Core

#### `dreamer analyze`

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
                [--parallel]
                [--jobs <n>]
                [--chunk-size <bytes>]
```

| Flag           | Short | Effect                                                                       |
|----------------|-------|------------------------------------------------------------------------------|
| `--path`       |       | **Required.** Absolute project directory (symlinks resolved).                |
| `--provider`   | `-P`  | Runtime override. Beats per-project config beats global default.             |
| `--force`      | `-f`  | Skip the incremental cache and re-analyze every discovered chat.             |
| `--dry-run`    | `-n`  | Phase 1 only (mistake extraction). Skip guardrail synthesis.                 |
| `--permissive` |       | Disable strict lint-rule allow-list; emit unrecognised rule ids.             |
| `--output-dir` | `-o`  | Override the per-project output directory.                                   |
| `--config`     | `-c`  | Override the global config path.                                             |
| `--since`      | `-s`  | Lookback window (e.g. `30m`, `1h`, `1d`, `1w`, `1mo`, `lifetime`). Default: `24h`. |
| `--parallel`   | `-p`  | Force parallel chunk execution (provider must support it).                   |
| `--jobs`       | `-j`  | Cap parallel session count. `0` = len(chunks).                               |
| `--chunk-size` |       | Override `analyzer.chunking.max_chunk_bytes`. `0` disables chunking.         |

#### `dreamer daemon`

Run periodic analysis for every project in the global config. Also starts
the embedded web server (when `web.enabled` is true).

```bash
dreamer daemon [--config <path>]
               [--parallel] [--jobs <n>] [--chunk-size <bytes>]
```

- Uses `daemon.frequency_seconds` from config (default 3600s).
- Runs one cycle immediately, then repeats on that interval.
- Concurrent workers process projects in parallel (`max_concurrent_jobs`).
- Auth failure on one project is logged and the daemon continues.
- Graceful shutdown via Ctrl+C (SIGTERM on Linux).

#### `dreamer start`

Start the daemon as a detached background process. The daemon writes its
PID to `<output_root>/dreamer.daemon.lock` and logs to
`<output_root>/dreamer.log`.

```bash
dreamer start [--config <path>]
```

#### `dreamer stop`

Stop the running daemon by reading its PID from the lockfile and sending
a termination signal.

```bash
dreamer stop [--config <path>]
```

#### `dreamer status`

Show the analysis job queue status.

```bash
dreamer status [--json] [--all] [--project <name>]
```

| Flag        | Short | Effect                                      |
|-------------|-------|---------------------------------------------|
| `--json`    | `-j`  | Machine-readable JSON output.               |
| `--all`     | `-a`  | Show all history (not just last 24h).       |
| `--project` | `-P`  | Filter to one project.                      |

### Setup & Config

#### `dreamer setup`

Interactive TUI wizard that writes `<UserConfigDir>/dreamer/config.yaml`.
Built with Bubble Tea + Lip Gloss. Supports back-navigation (left/esc).

```bash
dreamer setup [--advanced] [--force] [--no-startup]
```

| Flag           | Effect                                                               |
|----------------|----------------------------------------------------------------------|
| `--advanced`   | Branch into advanced steps (log level, rule timeout, parallel, etc.) |
| `--force`      | Overwrite existing config.yaml.                                      |
| `--no-startup` | Skip the startup-install step.                                       |

**Default flow (5 steps):** Provider -> Model -> Frequency -> Output root -> Startup install

**Advanced branch (5 more steps):** Log level -> Rule timeout -> Parallel mode -> Max concurrency -> Max chunk bytes -> First project

#### `dreamer add [path]`

Append a project to `config.yaml`. Preserves YAML comments via the
yaml.v3 Node API; rejects duplicates by name or path.

```bash
dreamer add [path] [--name <name>] [--since <window>]
```

- `path` defaults to `.` (current directory).
- `--name` defaults to `basename(path)`.
- `--since` defaults to `24h`.

#### `dreamer startup`

Manage OS-level auto-start registration for the daemon.

```bash
dreamer startup install [--config <path>]
dreamer startup status
dreamer startup uninstall
```

| Platform | Mechanism                                        |
|----------|--------------------------------------------------|
| Windows  | Task Scheduler (`schtasks.exe`, ONLOGON trigger) |
| Linux    | systemd user service (`~/.config/systemd/user/`) |

### Inspect

#### `dreamer ls-chats`

List discovered chat sources for a working directory.

```bash
dreamer ls-chats [--project-path <dir>]
```

#### `dreamer web`

Print the web UI URL (with `--open`, launch a browser). Requires the
daemon to be running.

```bash
dreamer web [--open]
```

---

## Chat sources

Discovered sources:

| Source                     | Source type                       | Root                                                             | Scoping                                                   |
|----------------------------|-----------------------------------|------------------------------------------------------------------|-----------------------------------------------------------|
| Copilot CLI sessions       | `copilot-session-jsonl`           | `~/.copilot/session-state/**/*.jsonl`                            | unscoped                                                  |
| Codex CLI sessions         | `codex-session-jsonl`             | `~/.codex/{sessions,archived_sessions}/**/*.jsonl`               | `session_meta.payload.cwd` inside project path            |
| VS Code Copilot chat       | `vscode-chat-session`             | `%APPDATA%/Code/User/workspaceStorage/*/chatSessions/*.{json,jsonl}` | sibling `workspace.json`                             |
| Claude Code                | `claude-code-session-jsonl`       | `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/**/*.jsonl`            | `cwd` / `workingDirectory` probes                        |
| Antigravity / Gemini       | `antigravity-gemini-session`      | `${GEMINI_HOME:-~/.gemini}/antigravity/{conversations,inbox}/**` | cwd probe (home root); implicit (project-local root)      |
| Gemini CLI                 | `gemini-cli-session-jsonl`        | `${GEMINI_HOME:-~/.gemini}/tmp/*/chats/*.jsonl`                  | cwd probe                                                 |
| Kiro CLI                   | `kiro-cli-session-sqlite`         | `~/.kiro/sessions/**` (SQLite)                                   | cwd probe                                                 |
| OpenCode                   | `opencode-session-sqlite`         | `~/.opencode/**` (SQLite)                                        | session metadata                                          |
| Codebuff                   | `codebuff-session-json`           | `${CODEBUFF_CONFIG_DIR:-~/.codebuff}/**`                         | cwd probe                                                 |

---

## Providers

dreamer supports a fast primary + ACP fallback per platform:

| Platform  | Primary             | Fallback          | Default model                  |
|-----------|---------------------|--------------------|--------------------------------|
| Copilot   | `copilot-sdk`       | `copilot-acp`      | `auto`                         |
| Claude    | `claude-cli`        | `claude-acp`       | `claude-haiku-4-5-20251001`   |
| Gemini    | `gemini-cli`        | `gemini-acp`       | `gemini-3-flash-preview`      |
| Kiro      | `kiro-acp`          | n/a                | `claude-sonnet-4-5-20250929`  |
| Codex     | `codex-cli`         | `codex-acp`        | `gpt-5.4-mini`                |
| OpenClaude| `openclaude-cli`    | n/a                | `mimo-v2.5-pro`               |
| OpenCode  | `opencode-server`   | `opencode-acp`     | `deepseek-v4-flash`           |
| Codebuff  | `codebuff-sdk`      | n/a                | `claude-opus-4-7`             |

Configure each provider under `providers.<id>` in the global config. See
the inline comments produced by `dreamer setup` for valid options per
field. Provider not registered or its CLI missing -> exit 1 with an exact
remediation command on stderr.

**Default provider:** `openclaude-cli` (free via Gitlawb Opengateway, no
auth needed). Override via `default_provider` in config or `--provider`
on the CLI.

---

## Web UI

The daemon embeds an HTTP server bound to `127.0.0.1:7777` (configurable
via the `web:` config block). The UI is loopback-only by design.

### SPA routes

| Path                                | Page                      |
|-------------------------------------|---------------------------|
| `/`                                 | Dashboard                 |
| `/settings`                         | Settings                  |
| `/logs`                             | Logs                      |
| `/providers`                        | Providers                 |
| `/projects/{name}`                  | Project overview          |
| `/projects/{name}/findings`         | Project findings          |
| `/projects/{name}/chats`            | Project chats             |
| `/projects/{name}/history`          | Project history           |

### API endpoints

| Endpoint                                              | Method | Purpose                           |
|-------------------------------------------------------|--------|-----------------------------------|
| `/api/health`                                         | GET    | Health check                      |
| `/api/dashboard`                                      | GET    | Dashboard summary                 |
| `/api/projects`                                       | GET    | Project list                      |
| `/api/projects/{name}`                                | GET    | Project detail                    |
| `/api/projects/{name}/findings`                       | GET    | Findings for a project            |
| `/api/projects/{name}/findings/{hash}`                | GET    | Single finding detail             |
| `/api/projects/{name}/findings/{hash}/apply`          | POST   | Apply a finding                   |
| `/api/projects/{name}/findings/{hash}/undo`           | POST   | Undo an applied finding           |
| `/api/projects/{name}/findings/{hash}/dismiss`        | POST   | Dismiss a finding                 |
| `/api/projects/{name}/findings/{hash}/undismiss`      | POST   | Undismiss a finding               |
| `/api/projects/{name}/findings/{hash}/resolve`        | POST   | Resolve a finding                 |
| `/api/projects/{name}/findings/{hash}/unresolve`      | POST   | Unresolve a finding               |
| `/api/projects/{name}/run`                            | POST   | Trigger on-demand analysis        |
| `/api/projects/{name}/chats`                          | GET    | Chat sources for a project        |
| `/api/projects/{name}/chats:bulk-delete`              | POST   | Bulk delete chat sources          |
| `/api/projects/{name}/history`                        | GET    | Run history for a project         |
| `/api/providers`                                      | GET    | Registered provider metadata      |
| `/api/settings`                                       | GET    | Merged config view                |
| `/api/settings`                                       | PUT    | Write to `ui-overrides.yaml`      |
| `/api/logs/tail`                                      | GET    | Tail of `dreamer.log`             |
| `/api/events`                                         | GET    | SSE stream of pipeline events     |
| `/api/fs/exists`                                      | GET    | Check if a path exists            |
| `/api/daemon/restart`                                 | POST   | Trigger daemon restart            |

### Apply / undo engine

Findings with an `apply` block (categories: `doc`, `lint-rule`, `ci-check`,
`config`) can be applied directly from the web UI. Strategies:
`append-section`, `replace-section`, `insert-after`, `append-file`,
`replace-file`. Every apply records a reversal (pre-image bytes + pre/post
SHA-256) so undo is safe and refuses (409 Conflict) if the target file
has drifted.

---

## Output

- Per-project todos: `<output_root>/<project>/todos.md`
- Per-project state: `<output_root>/<project>/state.json`
- Job queue:         `<output_root>/jobs.json`
- Logs:              `<output_root>/dreamer.log`
- Web port file:     `<output_root>/web.port`
- Daemon lockfile:   `<output_root>/dreamer.daemon.lock`

Each `dreamer analyze` invocation appends a `## Run <RFC3339>` section
containing one bullet per new finding, grouped by category
(`lint-rule`, `test`, `ci-check`, `doc`, `config`, `refactor-boundary`),
with code-block guardrail snippets and `Evidence:` paths.

Findings dedup by `sha256(category | normalized_mistake |
guardrail.tool | guardrail.rule)`. Re-running with no upstream changes
short-circuits to `no changes (cache hit)` and zero provider calls.

---

## Overlay configuration

- `<UserConfigDir>/dreamer/config.yaml` -- operator-owned base config (written by `dreamer setup`).
- `<UserConfigDir>/dreamer/ui-overrides.yaml` -- UI-owned overlay merged on top.

The daemon watches both files via fsnotify and hot-reloads on write.
`PUT /api/settings` writes only to the overlay; `config.yaml` is never
rewritten by the daemon. Merge rules: scalars and map keys -- overlay
wins; lists (`projects`, `redaction.patterns`) -- overlay replaces when
non-empty.

---

## Architecture notes

- **Read-only sandbox:** file reads + URL fetches + read-only shell/MCP
  allowed, writes/edits/deletes denied. Enforced both via the per-session
  system prompt and a generic `analyzer.DecidePermission` handler.
- **Two-phase pipeline:** phase 1 extracts recurring mistakes; phase 2
  synthesizes the smallest preventative guardrail per category. Phase 2 is
  skipped when phase 1 produces zero mistakes.
- **MCP server:** an internal MCP server (`mcp-server` command, hidden)
  provides the `record_finding` tool for Phase 2 providers that support
  MCP tool transport. A fallback `record-finding` CLI command handles
  providers that only support Bash tool.
- **Job queue:** the daemon uses an in-memory job queue with persistent
  state (`jobs.json`). Concurrent workers dequeue and run analysis jobs.
  Jobs recover from crashes via stale-PID detection.
- **Rule packs** ship embedded under `internal/analyzer/rules/*.yaml`;
  each category defines its own `response_schema`. Projects can override
  per-category toggles via `<project>/.dreamer/rules/<category>.yaml`.
- **Redaction** (`internal/analyzer/redaction.go`) runs before any text
  reaches the provider. Built-in patterns cover AWS / GitHub / GitLab /
  JWT / PEM / Slack / generic env-shaped secrets. User-supplied regexes
  are tagged `[REDACTED:custom]`.
- **Transcript chunking:** large transcripts are split by byte budget
  (default 480KB per chunk). Chunks run sequentially by default; opt-in
  parallel execution via `--parallel` or config.

See [doc/spec.md](doc/spec.md) for the canonical contract.

---

## Build

```bash
make build          # release build (trimmed, stripped, ~18MB)
make build-dev      # dev build with debug symbols (~26MB)
make build-linux    # cross-compile for Linux
make test           # full test suite
make test-race      # tests with race detector
make vet            # go vet
make fmt            # gofmt -w .
make lint           # golangci-lint (auto-installs if missing)
make cover          # test coverage summary
make cover-html     # test coverage HTML report
make vulncheck      # dependency vulnerability check
make install-hooks  # activate pre-commit hook (.githooks/)
make clean          # remove built binaries
```

Always use `-trimpath` when building. The binary warns at startup if
built without it. Use `-tags notrimpath` to suppress (e.g. CI fast-builds).

SQLite uses `modernc.org/sqlite` (pure Go, no CGo) for zero-cgo
cross-compilation.
