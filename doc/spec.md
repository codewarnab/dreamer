# dreamer v1 — System Specification

Authoritative spec for the v1 system. Derived from `vision.md`, `discussion.md`, and the **chosen** answers in `requirements.md`. When this document conflicts with `discussion.md` or `questions.md`, this document wins.

Status legend: **[locked]** = answer chosen in `requirements.md`; **[provisional]** = recommendation in `requirements.md` not yet marked `[chosen]`, but adopted here as the v1 default.

---

## 1. Overview

`dreamer` is a single-binary Go CLI that:

1. **Discovers** every AI-assistant chat transcript on disk tied to a target project path.
2. **Redacts** secrets from the transcript text.
3. **Sends** a single task-shaped prompt to a configured provider (native SDK/CLI primary, ACP fallback) with the transcript, repo metadata, and toolchain summary pre-baked into context.
4. **Receives** structured findings — one per recurring agent mistake, each carrying a proposed *prevention guardrail*.
5. **Validates** findings (allow-list checks, dedup, hash).
6. **Appends** new findings to `todos.md` under stable category headings, never rewriting prior runs.
7. **Caches** state (analyzed chat hashes, repo HEAD) so a no-op re-run does zero LLM work.

The product is a **prevention engine**, not a code reviewer. Output answers: *"what rule / test / doc would have stopped the agent making mistake M against this repo last week?"*

---

## 2. CLI surface

```
dreamer analyze --path <abs project dir>
                [--provider <id>]
                [--force]
                [--dry-run]
                [--permissive]
                [--output-dir <dir>]
                [--config <path>]

dreamer daemon   [--config <path>]
dreamer setup    [--advanced]
dreamer add      [<path>]
dreamer web      [--serve] [--open] [--port <n>]
dreamer ls-chats --project-path <dir>
dreamer start|stop|status
dreamer startup  {install|status|uninstall} [--config <path>]
```

Flag semantics:

| Flag           | Effect                                                                                          |
|----------------|-------------------------------------------------------------------------------------------------|
| `--path`       | Required for `analyze`. Must be absolute; symlinks resolved before discovery comparisons.       |
| `--provider`   | Runtime override. Beats project config beats global config. Values: `copilot-sdk`, `copilot-acp`, `claude-cli`, `claude-acp`, `codex-cli`, `codex-acp`, `gemini-cli`, `gemini-acp`, `kiro-acp`, `openclaude-cli`, `openclaude-acp`, `opencode-acp`, `opencode-http`, `codebuff-sdk`. |
| `--force`      | Skip the incremental cache; re-analyze every discovered chat.                                   |
| `--dry-run`    | Run phase 1 only (mistake extraction). Skip guardrail synthesis. Print mistake list; no write to `todos.md`. |
| `--permissive` | Disable strict lint-rule allow-list. Emit unrecognised rule ids tagged `[unverified]`.          |
| `--output-dir` | Override the per-project output directory. Default: `<UserConfigDir>/dreamer/<project>/`.       |
| `--config`     | Override the global config path.                                                                |

Exit codes: `0` success, `1` fatal (config missing, auth failure, all providers unreachable), `2` partial (some projects analyzed, some skipped — daemon only).

> **Note:** `config init` was replaced by `dreamer setup` in v1.5. The hidden `mcp-server` and `record-finding` commands are internal/debug tools not listed here.

---

## 3. Configuration system

### 3.1 Resolution order

Highest precedence first:

1. `--provider` CLI flag.
2. Per-project config: `<project>/.dreamer/config.yaml` field `provider`.
3. Per-project rule overrides under `<project>/.dreamer/rules/<category>.yaml`.
4. Global config: `<UserConfigDir>/dreamer/config.yaml` field `default_provider`.
5. Built-in defaults: `default_provider: openclaude-cli`; default rule pack from `internal/analyzer/rules/`.

`<UserConfigDir>` is resolved via Go's `os.UserConfigDir()`:

- Linux: `$XDG_CONFIG_HOME/dreamer/` (fallback `~/.config/dreamer/`).
- macOS: `~/Library/Application Support/dreamer/`.
- Windows: `%AppData%\dreamer\`.

### 3.2 Global config schema

```yaml
# <UserConfigDir>/dreamer/config.yaml
default_provider: openclaude-cli

projects:
  - name: dreamer
    path: /home/ani/dev/fun/dreamer
  # Each entry is an absolute path. Daemon iterates this list.

daemon:
  frequency_seconds: 3600

logging:
  level: info                # error | warn | info | debug
  file: ""                   # blank = <UserConfigDir>/dreamer/logs/dreamer.log

redaction:
  patterns: []               # extra regex strings appended to built-ins

providers:
  copilot-sdk:
    model: gpt-5.3-codex
    use_logged_in_user: true
    auto_start: false
    copilot_home: ""
    cli_url: ""
  copilot-acp:
    command: ["copilot", "--acp"]
    env: {}
  claude-cli:
    command: ["claude", "-p", "--output-format=stream-json", "--permission-mode", "plan", "--bare", "--tools", "Read,Grep,Glob"]
    env: {}
  claude-acp:
    command: ["claude", "--acp"]
    env: {}
  codex-cli:
    command: ["codex", "exec", "--json", "--sandbox", "read-only"]
    env: {}
  codex-acp:
    command: ["codex-acp"]
    env: {}
  gemini-cli:
    command: ["gemini", "--headless"]
    env: {}
  gemini-acp:
    command: ["gemini", "--acp"]
    env: {}
  kiro-acp:
    command: ["kiro", "--acp"]
    env: {}
  openclaude-cli:
    command: ["openclaude", "-p", "--output-format=stream-json", "--permission-mode", "plan", "--bare", "--tools", "Read,Grep,Glob"]
    env: {}
  opencode-acp:
    command: ["opencode", "--acp"]
    env: {}
  codebuff-sdk:
    # Codebuff HTTP API — text-only, no tools
    env: {}
```

### 3.3 Per-project config schema

```yaml
# <project>/.dreamer/config.yaml
provider: claude-cli           # optional; falls back to global default_provider

# Optional provider-scoped overrides applied on top of global.providers.<id>
providers:
  claude-cli:
    command: ["claude", "-p"]

# Optional rule pack overrides — equivalent to .dreamer/rules/<category>.yaml entries.
rules:
  lint-rule:
    enabled: true
  refactor-boundary:
    enabled: false

redaction:
  patterns:
    - "ACME_INTERNAL_[A-Z0-9]{32}"
```

### 3.4 Per-category rule config

```yaml
# <project>/.dreamer/rules/lint-rule.yaml   (and similar for each category)
category: lint-rule
enabled: true
threshold: 0.70
timeout_seconds: 45
prompt_template: |
  Identify recurring mistakes in this chat history that a lint rule
  would have prevented in the project at {{project_root}}.
  Toolchain in use: {{toolchain_summary}}.
  Return JSON conforming to the response_schema below.
response_schema:
  type: object
  required: [findings]
  properties:
    findings:
      type: array
      items:
        type: object
        required: [mistake, guardrail, confidence]
        properties:
          mistake: {type: string}
          guardrail:
            type: object
            required: [kind, tool, rule]
            properties:
              kind: {type: string, enum: [lint-rule]}
              tool: {type: string}
              rule: {type: string}
              config_snippet: {type: string}
          codebase_evidence:
            type: array
            items:
              type: object
              properties:
                path: {type: string}
                lines: {type: string}
                symbol: {type: string}
          confidence: {type: number}
```

The same shape exists per category, with `guardrail.kind` constrained to the matching value.

---

## 4. Provider system

### 4.1 Strategy: dual provider per platform

Per `requirements.md` Q1 **[locked]**:

| Platform    | Primary (fast)           | Fallback (legal/standards) |
|-------------|--------------------------|----------------------------|
| Copilot     | `copilot-sdk` (Go SDK)   | `copilot-acp`              |
| Claude      | `claude-cli`             | `claude-acp`               |
| OpenClaude  | `openclaude-cli`         | n/a                        |
| Codex       | `codex-cli`              | `codex-acp`                |
| Gemini      | `gemini-cli`             | `gemini-acp`               |
| Kiro        | `kiro-acp` (only path)   | n/a (ACP is native)        |
| OpenCode    | `opencode-acp`/`opencode-http` | n/a                   |
| Codebuff    | `codebuff-sdk` (HTTP)    | n/a                        |

Rule: never scrape; never use third-party reverse-engineered clients. ACP is the legal fallback when a native SDK is missing or unstable.

**Out of scope for v1.** Cursor is not supported as a provider or as a chat-discovery source. No `cursor-cli` adapter, no `cursor.go` reader, no Cursor entries in any config schema or file-tree. Revisit only if Cursor ships an official ACP server or a documented headless mode.

### 4.2 Selection rule

The resolved provider id (§3.1) is looked up in `providers.<id>`. If the binary or SDK auth check fails, dreamer **hard-fails** with the exact remediation command (§13). No silent fallback (Q10 **[locked]**).

### 4.3 Interfaces

```go
// internal/analyzer/provider.go
type Provider interface {
    ID() string
    Start(ctx context.Context) error
    NewSession(ctx context.Context, cfg SessionConfig) (Session, error)
    Close() error
}

type SessionConfig struct {
    WorkingDirectory string
    Model            string
    ReadOnly         bool   // always true in v1
    SystemMessage    string
}

type Session interface {
    Run(ctx context.Context, prompt string, timeout time.Duration) (string, error)
    Close() error
}
```

Implementations live under `internal/analyzer/providers/<id>/`. The existing `copilotClient` (`internal/analyzer/client.go`) is moved to `internal/analyzer/providers/copilotsdk/` and exposed via this interface.

### 4.4 ACP adapter

**[locked]** Transport: **stdio JSON-RPC 2.0** only.

Wire protocol:

1. `Start()` spawns the configured command with stdin/stdout pipes attached.
2. Send `initialize` with our capability set: `{"capabilities": {"read": true, "search": true, "url_fetch": true, "writes": false, "shell": false}}`.
3. Receive the agent's capability response. Unknown capabilities default to denied.
4. `NewSession()` issues `session/new` with `working_directory` and `system_message`.
5. `Run()` issues `session/prompt` with the prompt text. While streaming, every `session/request_permission` is routed through the ported read-only handler (§4.5). Tool calls returning data are forwarded to the agent. Tool calls requesting writes or out-of-scope paths are denied.
6. `Close()` sends `session/end` then drains stdout.
7. Provider `Close()` sends `shutdown` then waits up to 5s for the subprocess to exit, then SIGTERMs.

### 4.5 Read-only permission handler

The existing handler in `internal/analyzer/client.go` (`buildReadOnlyPermissionHandler`) is the canonical implementation. Generalize it from Copilot's `PermissionRequest` to a provider-agnostic shape:

```go
type PermissionRequest struct {
    Kind          PermissionKind   // Read | URL | Shell | MCPTool | CustomTool
    Path          *string
    PossiblePaths []string
    ReadOnly      *bool
    Commands      []ShellCommand
    HasWriteFileRedirection *bool
}
```

Rules (unchanged in spirit):

- `Read`: allowed if resolved path is inside the configured working directory; symlinks resolved; null bytes rejected; URI schemes rejected.
- `URL`: allowed.
- `Shell`: allowed only if every command is `ReadOnly` and there is no write redirection.
- `MCPTool` / `CustomTool`: allowed only if `ReadOnly` is explicitly true.
- Anything else: denied with a structured reason.

Even though §7 sets `ReadOnly: true` and pre-bakes context (Q5 **[locked]**), this handler is still installed as a safety net to defeat misbehaving providers.

---

## 5. Discovery system

### 5.1 Sources

Implemented in `internal/chat/discovery.go`:

- Copilot session-state: `~/.copilot/session-state/**/*.jsonl`
- Codex sessions: `~/.codex/sessions/**/*.jsonl` + `~/.codex/archived_sessions/**/*.jsonl` (filtered by `session_meta.payload.cwd`)
- VS Code Copilot Chat: `%APPDATA%/Code/User/workspaceStorage/*/chatSessions/*.{json,jsonl}` filtered by `workspace.json` evidence.
- Claude Code: `~/.claude/projects/**/*.jsonl` (or `$CLAUDE_CONFIG_DIR/projects/**`) filtered by `cwd` probe.
- Antigravity/Gemini: `~/.gemini/antigravity/{conversations,inbox}/**/*.{pb,pbtxt,json,jsonl}` (or `$GEMINI_HOME/antigravity/...`), plus `<project>/.gemini/antigravity/...`.
- Gemini CLI (non-Antigravity): `${GEMINI_HOME:-~/.gemini}/tmp/*/chats/*.jsonl` — cwd-probe analogous to Claude Code.
- Kiro CLI: SQLite-backed sessions — rows filtered by stored `Directory` column inside `projectPath`.
- OpenCode: SQLite-backed sessions at `${OPENCODE_DB:-<DataHomeDir>/opencode/opencode.db}` — rows filtered by stored `Directory` column inside `projectPath`.

### 5.2 Out of scope

**Cursor** is not supported as a chat-discovery source. No `cursor.go` reader, no Cursor entries in any config schema or file-tree. Revisit only if Cursor ships an official ACP server or a documented headless mode.

Each new source: new `SourceType` constant, new `discover<Tool>Sessions` function in `discovery.go`, new reader + sanitizer in `internal/chat/readers/`, tests in `discovery_test.go`.

### 5.3 Discovery invariants

- Missing root directories return `(nil, nil)`, not error.
- Symlinks resolved on both candidate and project paths before comparison.
- Sorted by mtime desc, path asc.
- Cross-source dedup by file path.
- Corrupt or malformed candidate files are skipped — Q14 **[provisional]**: their identifiers are collected and rendered into a `## Warnings (Run <timestamp>)` section in `todos.md`. No new files are created for warnings.

---

## 6. Redaction (Q7 [locked])

Built-in regex patterns target:

- AWS access key id / secret access key
- GitHub personal access tokens (`ghp_…`, `github_pat_…`)
- GitLab tokens (`glpat-…`)
- JWTs (three base64 segments)
- Generic `password=…`, `secret=…`, `api[_-]?key=…`, `token=…`
- Bearer header values
- PEM private key headers
- Slack bot/user tokens (`xox[abps]-…`)
- Common `.env` shaped lines

Match policy:

- Replace each hit with `[REDACTED:<type>]`. `<type>` is the regex's symbolic name (`aws-key`, `jwt`, `bearer`, etc).
- Counts logged at `info` level; matched content is **never** logged.
- User-extensible via `redaction.patterns` (config). Custom entries get `[REDACTED:custom]`.
- Redaction runs **before** any chat content reaches the provider, including ACP transport.

The redaction module lives at `internal/analyzer/redaction.go`.

---

## 7. Analysis pipeline

### 7.1 Shape (Q5 [locked]): task-based, no live tool calls

Each provider call is a single request/response. The orchestrator pre-builds the prompt with:

- The redacted chat transcript (or a windowed slice if the transcript exceeds token budget — see §7.6).
- A toolchain summary (§8).
- The project name + absolute root.
- The active rule's `prompt_template` and `response_schema`.

The provider returns one JSON document; no mid-flight tool calls happen. The read-only permission handler (§4.5) remains installed as a safety net but is not part of the happy path.

### 7.2 Two phases

**Phase 1 — Mistake extraction.** For each enabled category, send one prompt asking only: *"what recurring mistake did the assistant make that this category could prevent?"* No guardrail proposal in this phase. JSON output: `{"mistakes": [{"category", "summary", "evidence_excerpt", "confidence"}]}`.

**Phase 2 — Guardrail synthesis.** Aggregates the phase-1 mistakes plus a static codebase summary (file list + symbol index from §8) into one prompt per category and asks for the smallest preventative guardrail per mistake. JSON output per the per-category `response_schema` (§3.4).

### 7.3 Phase-2 gate (Q8 [provisional])

Phase 2 runs **only when phase 1 yields ≥ 1 mistake total**. `--dry-run` forces phase-1-only and prints the mistake list to stdout, writing nothing.

### 7.4 Codebase grounding (Q5 [locked])

No live tool calls. Grounding happens entirely via the **pre-baked context** assembled by `internal/analyzer/grounding/`:

- File list (paths under repo root, capped at N entries, default 2000).
- Toolchain summary (§8).
- Symbol index (Go: `go list -deps -json` → exported funcs/types; JS/TS: top-level exports parsed from `tsc --noEmit --listEmittedFiles` or a tree-sitter pass; Python: `ruff check --no-fix --select F` or pyflakes top-level defs; Rust: `cargo metadata`).
- A capped "relevant files" slice for each phase-1 mistake, where relevance is matched on substrings in the mistake summary against the file list.

If the assembled context exceeds the provider's token budget, the orchestrator truncates oldest chats first, then non-matching file list entries, then symbol index entries. Truncation is logged.

### 7.5 Validation

For each finding:

1. Dedup by `sha256(category | normalized_description)`. Existing hashes loaded from `todos.md` markers.
2. Confidence filter: drop findings below `rules.<category>.threshold`.
3. For `lint-rule` findings: check `guardrail.tool + guardrail.rule` against the allow-list (§10). On miss: drop in strict mode, tag `[unverified]` in permissive mode.
4. For all findings: verify `codebase_evidence[].path` exists under the working directory. Drop if not.
5. For `config` findings: verify the named config file exists in the repo. Drop if not.

### 7.6 Token budget

Per call: provider-dependent; default cap 200k tokens input, 8k output. Configurable per provider under `providers.<id>.max_input_tokens`.

---

## 8. Toolchain detection

`internal/analyzer/toolchain/detect.go` returns:

```go
type Toolchain struct {
    Languages     []string         // ["go", "typescript", ...]
    Linters       []LinterConfig   // golangci-lint, eslint, ruff, clippy, biome, ...
    TestFrameworks []string        // ["go test", "vitest", "pytest", ...]
    ConfigFiles   []string         // absolute paths of detected configs
}

type LinterConfig struct {
    Tool       string             // "golangci-lint"
    ConfigPath string             // ".golangci.yml"
    EnabledRules []string         // parsed where possible
}
```

Detection heuristics:

- **Go**: `go.mod` present → language `go`; `.golangci.{yml,yaml,toml}` → `golangci-lint`; else `staticcheck` + `go vet`. Tests: `go test`.
- **JS/TS**: `package.json` present; check `eslint.config.{js,cjs,mjs,ts}`, `.eslintrc*`, `biome.json`. Tests: derive from `package.json` scripts.
- **Python**: `pyproject.toml`/`ruff.toml` → `ruff`; `.flake8`/`setup.cfg` → `flake8`; `mypy.ini` → `mypy`. Tests: `pytest` if importable.
- **Rust**: `Cargo.toml` → `clippy`. Tests: `cargo test`.

The toolchain summary is serialised into a short string injected into prompts via `{{toolchain_summary}}`.

---

## 9. Rule packs

Per Q4 **[locked]**, all six categories ship with externalised YAML configs:

- `lint-rule`
- `test`
- `ci-check`
- `doc`
- `config`
- `refactor-boundary`

Default rule packs live in `internal/analyzer/rules/*.yaml` and are embedded via `//go:embed`. Project overrides at `<project>/.dreamer/rules/<category>.yaml` are merged on top — last-write-wins per field. A user disables a category by setting `enabled: false` in either layer.

Each rule pack defines its own `response_schema` so the orchestrator validates returned JSON before validation (§7.5) runs. Schema violations: log + skip the finding, do not fail the run.

---

## 10. Lint-rule allow-list (Q6 [locked])

`internal/analyzer/lintrules/` ships curated allow-lists for the top three linters:

- `golangci.go` — list of valid `golangci-lint` linter names (`govet`, `nilness`, `errcheck`, `revive`, `gosec`, ...).
- `eslint.go` — core ESLint rule ids + `@typescript-eslint/*` + common plugins.
- `ruff.go` — Ruff rule codes (`E…`, `F…`, `B…`, `S…`, `UP…`, `RUF…`).

Algorithm:

1. Lookup `(guardrail.tool, guardrail.rule)` in the allow-list table for the matching tool.
2. **Strict mode (default)**: drop the finding if not found.
3. **Permissive mode (`--permissive`)**: keep the finding, prefix the rendered todo line with `[unverified]`, and add a `<!-- dreamer:lintrule:unverified -->` marker for telemetry.
4. Tools not in the allow-list table: treated as permissive automatically.

Allow-list update process is documented in `CONTRIBUTING.md`.

---

## 11. Output

### 11.1 Default location (Q11 [provisional])

`<UserConfigDir>/dreamer/<project>/todos.md`.

`<project>` is the basename of the absolute project path with non-`[A-Za-z0-9._-]` characters replaced by `_`. Collisions are resolved by appending a short hash of the full path.

`--output-dir` overrides the parent directory. The trailing `<project>/todos.md` shape is preserved.

### 11.2 File format

```markdown
# dreamer todos — <project name>

<!-- dreamer:version:1 -->

## Run 2026-05-15T13:42:00Z

### Lint rule
- [ ] Enable `nilness` in golangci-lint to prevent the assistant re-introducing nil dereferences on `copilotSession.Data` (seen in 3 chats).
  ```yaml
  linters:
    enable: [nilness]
  ```
  Evidence:
  - `internal/analyzer/client.go:142-150` (`copilotSession.Run`)
  <!-- dreamer:finding:9f3a…b71 -->

### Test
- [ ] Add a regression test for `discoverClaudeCodeSessions` covering malformed JSONL lines (assistant repeatedly retried instead of skipping).
  Evidence:
  - `internal/chat/discovery.go:257-292`
  <!-- dreamer:finding:5b0e…c0d -->

### Doc
- [ ] Document the read-only sandbox contract in `CLAUDE.md` so future sessions stop suggesting `os.WriteFile` from within the analyzer.
  <!-- dreamer:finding:2cc1…ff4 -->

## Warnings (Run 2026-05-15T13:42:00Z)

- Skipped `~/.codex/sessions/2026-05-12T08:00:11-abc.jsonl` (malformed `session_meta` line 3).
```

Constraints:

- Section headers and HTML markers must be left intact across runs; merging is append-only.
- Finding-hash dedup: hash key is `sha256(category | normalized_description | guardrail.tool | guardrail.rule)`.
- Empty categories within a run are omitted; categories may appear in any order alphabetically.
- Code blocks inside todo bullets must use four-space indent so they nest correctly under the `- [ ]` line.

### 11.3 Warnings section

A `## Warnings (Run <ts>)` section is appended whenever discovery or validation skips at least one item. Format: bulleted list of `Skipped <path> (<reason>).` lines. No PII; no chat content.

---

## 12. State & incremental runs (Q9 [provisional])

`internal/state/tracker.go` stores per-project state at `<UserConfigDir>/dreamer/<project>/state.json`:

```json
{
  "version": 2,
  "last_run_utc": "2026-05-15T13:42:00Z",
  "repo_head_sha": "c32ad17…",
  "chat_hashes": {
    "/home/ani/.claude/projects/-dreamer/abc.jsonl": "sha256:…"
  },
  "finding_hashes": ["9f3a…b71", "5b0e…c0d"],
  "provider_usage": {
    "copilot-sdk": {"runs": 12, "total_tokens": 482103}
  }
}
```

Cache key per chat file: `sha256(path || file_hash || repo_head_sha)`. If every discovered chat's cache key is unchanged and `repo_head_sha` is unchanged, the orchestrator emits no provider calls and exits with `Run summary: no changes`.

`--force` bypasses the cache.

Repo HEAD is read via `git rev-parse HEAD` from the working directory. Non-git working directories use `""` and effectively disable the HEAD half of the key.

---

## 13. Auth & error handling (Q10 [locked])

### 13.1 Startup checks

Before any provider call:

1. Resolve provider id (§3.1).
2. Call `provider.Start(ctx)` with a 30s timeout.
3. On error: log full details to `<UserConfigDir>/dreamer/logs/dreamer.log`, print a short message to stderr including the exact remediation command (e.g. `Provider claude-cli failed to start. Run 'claude auth login' and retry.`), exit `1`.

### 13.2 Per-provider remediation messages

`internal/config/providers.go` maps provider id → remediation. Examples:

- `copilot-sdk`: `Run 'copilot auth login' (GitHub Copilot CLI must be installed).`
- `claude-cli`: `Run 'claude auth login'.`
- `openclaude-cli`: `Run 'openclaude auth login'.`
- `codex-cli`: `Run 'codex auth login' (OpenAI Codex CLI must be installed).`
- `gemini-cli`: `Run 'gemini auth login'.`
- `kiro-acp`: `Install the Kiro CLI and ensure 'kiro --acp' starts cleanly.`
- ACP variants: `Ensure '<command>' starts and emits an ACP initialize response.`

### 13.3 Daemon mode

In daemon mode, an auth failure logs at `error` and **skips the affected project for that cycle**; the daemon stays running. The next cycle retries cleanly.

### 13.4 Mid-run errors

- Provider returns malformed JSON: log + skip the finding; continue.
- Provider session timeout: see Issue I1 (§19). Workaround until fixed: bound `Run()` with the rule's `timeout_seconds` plus a 5s grace window; on timeout, retry once with a fresh session; on second failure, log + skip rule.
- Discovery file errors: skip + warnings section (§11.3).

---

## 14. Daemon (Q12 [provisional])

The existing daemon is kept and rewired to use `Provider`:

- Loop over `config.projects`.
- Per project: resolve provider, run the analyze pipeline, update state, sleep `daemon.frequency_seconds`.
- Signal handling: `SIGTERM` → finish current session, save state, exit `0`.
- PID file at `<UserConfigDir>/dreamer/daemon.pid` with `flock` on Unix and `os.Rename` lock on Windows.

Windows-only `startup install` continues to point at the daemon binary.

---

## 15. Logging

Single structured logger at `internal/logging/logger.go`. Key=value format via `log/slog`:

```
time=2026-05-15T13:42:00Z level=INFO msg=analyze_started project=dreamer provider=openclaude-cli
```

Default sink: `<output_root>/dreamer.log`. Rotation: single backup file (`dreamer.log.1`) when primary exceeds `logging.max_size_mb` (default 100 MB). Sensitive content (chat text, redaction matches) is never logged.

---

## 16. Directory & file layout

Repo structure after v1 lands:

```
main.go
cmd/
  root.go
  analyze.go
  daemon.go
  setup.go
  add.go
  web.go
  ls_chats.go
  startup.go
  start.go
  stop.go
  status.go
  mcpserver.go               # hidden: MCP stdio server
  recordfinding.go            # hidden: JSONL finding recorder
  helpers.go
internal/
  analyzer/
    provider.go               # interfaces (Provider, Session)
    permission.go             # read-only permission handler
    orchestrator.go           # two-phase pipeline
    orchestrator_chunked.go   # chunked execution mode
    grounding/
      detect_files.go
      symbol_index.go
    lintrules/
      golangci.go
      eslint.go
      ruff.go
    rules/                    # embedded default rule packs (YAML)
      lint-rule.yaml
      test.yaml
      ci-check.yaml
      doc.yaml
      config.yaml
      refactor-boundary.yaml
    toolchain/
      detect.go
    transport/
      scanner.go, env.go, inputcap.go, ratelimit.go, syncwriter.go
    providers/
      acpcore/                # shared ACP stdio JSON-RPC client
      copilotsdk/
      copilotacp/
      claudecli/
      claudeacp/
      codexcli/
      codexacp/
      geminicli/
      geminiacp/
      kiroacp/
      openclaudecli/
      opencodeacp/
      opencodehttp/
      codebuffsdk/
      flagutil/
  categories/                 # canonical RuleCategory types
  chat/
    discovery.go
    source_copilot.go
    source_codex.go
    source_vscode.go
    source_claude.go
    source_antigravity.go
    source_gemini_cli.go
    source_kiro.go
    source_opencode.go
    source_codebuff.go
    paths.go
    probe.go
  errs/                       # shared error types
  fsutil/                     # atomic writes, lock files, process management
  jobqueue/                   # daemon job scheduler
  logging/
    logger.go
  mcpserver/                  # MCP server implementation
  output/
    generator.go
  pipeline/
    pipeline.go               # Run — the analysis core
    rules.go                  # mergeRulePacks, applyRuleToggles
    cache.go
    chunker.go
    transcript.go
    lookback.go
    projectname.go
  sandbox/                    # OS-level sandbox (Windows, Linux, macOS)
    sandbox.go, none.go, windows.go, windows_*.go
  state/
    tracker.go
    findings.go
    history.go
  web/
    server.go, csrf.go, sse.go, activity.go, runner.go
    apply/apply.go
    handlers/
    templates/
    static/
doc/
  vision.md, spec.md, spec.v1.1.md, spec.v1.2.md, spec.v1.5.md, bugs.md, code-review.md
```
  questions.md
  requirements.md
  spec.md                      # this file
```

---

## 17. End-to-end run sequence

`dreamer analyze --path /repo`:

1. **Load config.** Global + project + CLI flag merge. Resolve provider id, output dir, rule pack.
2. **Resolve project.** Absolute path + symlink-resolved; basename → output subdirectory.
3. **Discover chats.** Scan all sources from §5.1 + §5.2. Apply cwd-probe filters. Collect candidates.
4. **Load state.** Read `state.json`. Compute current cache keys for every candidate. Compute `repo_head_sha`.
5. **Short-circuit.** If `state.repo_head_sha == current_sha` and every candidate's cache key is unchanged and `--force` is not set → log `no changes` and exit `0`.
6. **Read + sanitize chats.** Each candidate goes through its reader + sanitizer (existing). Output: a list of `ChatMessage` slices.
7. **Redact.** Run §6 regex pass over each message text. Replace hits with `[REDACTED:<type>]`. Log counts.
8. **Detect toolchain.** §8.
9. **Build phase-1 prompts.** For each enabled category, format the rule's `prompt_template` with `{{project_root}}` + `{{toolchain_summary}}` + redacted chat slices.
10. **Provider start.** `provider.Start(ctx)`; on failure → §13.
11. **Phase 1.** For each enabled category, open a session, run the prompt with the rule's timeout, parse JSON via the category's `response_schema`. Collect mistakes.
12. **Gate.** If total mistakes == 0 and `--dry-run` is not set → write a Warnings entry "no recurring mistakes found", update state, exit `0`. If `--dry-run` → print mistakes, exit `0`.
13. **Build phase-2 prompts.** Per category, format the synthesis template with the phase-1 mistakes + grounded codebase context (§7.4).
14. **Phase 2.** For each category with at least one mistake, run synthesis, parse, validate (§7.5). Apply lint-rule allow-list (§10).
15. **Render.** Group surviving findings by category. Hash. Append a new `## Run <ts>` section to `todos.md`. Append `## Warnings` if any items were skipped.
16. **Save state.** Update `state.json` with the new cache keys, `repo_head_sha`, finding hashes, and provider usage stats.
17. **Close provider.** `provider.Close()`. Exit `0`.

---

## 18. Provider-specific notes

- **`copilot-sdk`**: current impl (`internal/analyzer/client.go`). Honour Issue I1 workaround (§19). Read-only sandbox via the existing `PermissionHandler`.
- **`claude-cli`**: spawn `claude -p --output-format=stream-json --permission-mode plan --add-dir <root>`. Read prompt from stdin; collect stream-json events until the final `assistant` message; return its text. Validate flags via `context7` at implementation time.
- **`gemini-sdk`**: official Google Generative AI Go SDK. Pre-bake full prompt; no tools.
- **`*-acp`**: shared client in `internal/analyzer/providers/acpcore/`. Each platform-specific `*acp` package supplies the command and platform-quirks shims (e.g. capability fingerprints).
- **`kiro-acp`**: uses [Kiro-Goacp](https://github.com/Quorinex/Kiro-Goacp) as the client library; bundled via go modules. Native client wraps `acpcore` for the actual transport.

---

## 19. Open issues

### I1 — Copilot SDK session timeout

**Symptom.** `Copilot request failed; verify authentication and connectivity: waiting for session.idle: context deadline exceeded`.

**Status.** Open (carried from `requirements.md`).

**v1 mitigation.**

1. Increase per-rule default timeout from 45s to 90s for `copilot-sdk` until upstream is fixed. Other providers keep 45s.
2. On `session.idle` timeout: `session.Close()`, re-open one fresh session, retry once. Second failure → log + skip.
3. Telemetry: record timeout counts in `state.json:provider_usage.copilot-sdk.timeouts`.

**Owner.** Implementer of the orchestrator phase work.

---

## 20. Acceptance criteria (mirrors `vision.md` success criteria, formalised)

A v1 build is acceptable when, on this repository:

1. `dreamer analyze --path /home/ani/dev/fun/dreamer --provider copilot-sdk` writes ≥ 1 finding to `<UserConfigDir>/dreamer/dreamer/todos.md`, with each finding citing at least one real symbol from the repo.
2. Re-running the same command immediately produces zero new findings and zero provider calls (cache hit).
3. `dreamer analyze --path /home/ani/dev/fun/dreamer --provider claude-cli` and `--provider copilot-acp` (when ACP is wired) produce the same output shape with no source changes.
4. No file is written outside `<UserConfigDir>/dreamer/dreamer/` (or `--output-dir`).
5. A chat containing an `aws-key`-shaped string is redacted before reaching the provider; the redaction count appears in the log.
6. Pointing at a project with no detected mistakes finishes without writing any `## Run` section; only the Warnings line ("no recurring mistakes found") is appended.
7. `dreamer ls-chats --project-path` lists at least one entry for each installed assistant.
8. `dreamer analyze` against a project whose configured provider's CLI is missing exits `1` with the exact remediation command from §13.2 on stderr and full detail in the log.
9. `dreamer daemon` running with two configured projects survives an auth failure in one and continues to process the other.

---

## 21. Glossary

- **ACP** — [Agent Client Protocol](https://agentclientprotocol.com). stdio JSON-RPC protocol for talking to AI coding agents.
- **Mistake** — recurring failure mode of the assistant against this codebase, surfaced by phase 1.
- **Guardrail** — concrete prevention (lint rule / test / doc / ci check / config / refactor boundary) proposed in phase 2.
- **Finding** — validated, hashed, deduplicated `{mistake, guardrail, evidence, confidence}` record written to `todos.md`.
- **Working directory** — absolute, symlink-resolved project root. All provider read/search calls are scope-locked to this directory.
- **`<UserConfigDir>`** — `os.UserConfigDir()` result. Linux: `~/.config`. macOS: `~/Library/Application Support`. Windows: `%AppData%`.