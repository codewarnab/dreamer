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

Historical design background: [v1 system specification](docs/spec.md). Current behavior is documented in the CLI, API, architecture, and security references.

---

## Project status and support

Dreamer is under active development and has not published a stable release. See [SUPPORT.md](SUPPORT.md) for the tested-platform matrix and current sandbox and service-management limits. Use [GitHub Issues](https://github.com/codewarnab/dreamer/issues) for public bug reports and focused feature requests. Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## Install

Release binaries are published for Linux, macOS, and Windows on `amd64` and
`arm64`. Download the matching `dreamer_<os>_<arch>` file and
`checksums.txt` from [GitHub Releases](https://github.com/codewarnab/dreamer/releases), verify its SHA-256 checksum, then put
the binary on your `PATH` (for example `/usr/local/bin/dreamer` on Unix or a
directory listed in `%PATH%` on Windows). On Unix, mark it executable first:

```bash
# Linux
sha256sum -c checksums.txt --ignore-missing
chmod +x dreamer_<os>_<arch>
sudo install -m 0755 dreamer_<os>_<arch> /usr/local/bin/dreamer

# macOS
shasum -a 256 dreamer_darwin_<arch>
grep 'dreamer_darwin_<arch>' checksums.txt

# Windows PowerShell
Get-FileHash .\dreamer_windows_<arch>.exe -Algorithm SHA256
Select-String -Path .\checksums.txt -Pattern 'dreamer_windows_<arch>.exe'
```

To build from source, install Go 1.26.8 (or the version declared in `go.mod`), Git, and Make, then run `go mod download` and `make build`. Provider-backed analysis also requires the selected provider CLI and its normal authentication, except where the provider documentation says otherwise. On Linux, sandboxed provider execution requires `bwrap` and enabled unprivileged user namespaces; see [docs/SANDBOX.md](docs/SANDBOX.md).

Alternatively, build from source with `make build`. The release archives are
raw binaries, and `dreamer update` must be able to replace the installed file;
use an installation directory writable by your account or run the update with
the permissions required for that directory. To upgrade manually, verify a new
release and replace the binary. To roll back, repeat that process with a previous
release. To uninstall, stop Dreamer, remove any installed startup integration with
`dreamer startup uninstall`, then remove the binary. Configuration and analysis
output are retained unless you remove their directories yourself.

## Quick start

```bash
# 1. build only when installing from source
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

For a complete reference of all CLI commands, subcommands, and flags, see the **[CLI Commands Reference](docs/CLI.md)**.

---

## Chat sources

Discovered sources:

| Source                     | Source type                       | Root                                                             | Scoping                                                   |
|----------------------------|-----------------------------------|------------------------------------------------------------------|-----------------------------------------------------------|
| Copilot CLI sessions       | `copilot-session-jsonl`           | `~/.copilot/session-state/**/*.jsonl`                            | unscoped                                                  |
| Codex CLI sessions         | `codex-session-jsonl`             | `~/.codex/{sessions,archived_sessions}/**/*.jsonl`               | `session_meta.payload.cwd` inside project path            |
| VS Code Copilot chat       | `vscode-chat-session`             | `%APPDATA%/Code/User/workspaceStorage/*/chatSessions/*.{json,jsonl}` | sibling `workspace.json`                             |
| Claude Code                | `claude-code-session-jsonl`       | `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/**/*.jsonl`            | `cwd` / `workingDirectory` probes                        |
| Antigravity IDE & CLI (agy)| `antigravity-gemini-session`      | `${GEMINI_HOME:-~/.gemini}/antigravity/{conversations,inbox}/**`, `${GEMINI_CLI_HOME:-~/.gemini/antigravity-cli}/brain/**` | workspace_uris index & cwd probe |
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
| Kiro      | `kiro-acp`          | n/a                | `claude-sonnet-4.5`           |
| Codex     | `codex-cli`         | `codex-acp`        | `gpt-5.4-mini`                |
| OpenClaude| `openclaude-cli`    | n/a                | `mimo-v2.5-pro`               |
| OpenCode  | `opencode-server`   | `opencode-acp`     | `deepseek-v4-flash`           |
| Codebuff  | `codebuff-sdk`      | n/a                | `claude-opus-4-6`             |

Configure each provider under `providers.<id>` in the global config. See
the inline comments produced by `dreamer setup` for valid options per
field. Provider not registered or its CLI missing -> exit 1 with an exact
remediation command on stderr.

**Default provider:** `openclaude-cli` (free via Gitlawb Opengateway, no
auth needed). Override via `default_provider` in config or `--provider`
on the CLI.

---

## Web UI

For the complete list of SPA routes, REST API endpoints, and the apply/undo engine documentation, see the **[Web UI & API Reference](docs/API.md)**.

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

On Linux, application config lives under `$XDG_CONFIG_HOME/dreamer/` (or
`~/.config/dreamer/`). Daemon startup and background-job systemd units live
under `$XDG_CONFIG_HOME/systemd/user/` (or `~/.config/systemd/user/`). For
systemd compatibility, an empty, whitespace-only, or relative
`XDG_CONFIG_HOME` is ignored and the `~/.config` fallback is used.

See [docs/CLI.md](docs/CLI.md) for the current command contract and
[docs/spec.md](docs/spec.md) for historical v1 design context.


Minimal non-interactive setup example:

```bash
dreamer setup --non-interactive \
  --provider openclaude-cli \
  --output-root "$HOME/.local/share/dreamer"
```

Linux daemon auto-start example:

```bash
# With an absolute XDG path, the unit is written below this directory.
XDG_CONFIG_HOME="$HOME/.config" dreamer startup install
systemctl --user status dreamer.service
```

Windows uses `dreamer startup install` with Task Scheduler. The daemon startup
command does not install launchd on macOS; the background-job scheduler has a
separate Darwin launchd backend.

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

## Documentation maintenance

When commands, provider defaults, API routes, or internal command names change,
update their references in the same pull request. Useful automated drift checks
include Cobra command/flag inventory comparison, provider-table generation,
internal-link validation, API route inventory comparison, obsolete-command
spelling checks, and duplicate-heading detection.


## Contributing and license

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md), [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md), and [docs/TESTING.md](docs/TESTING.md) before opening a pull request.

Dreamer is licensed under the [MIT License](LICENSE).
