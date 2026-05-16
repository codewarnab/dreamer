# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build / test commands

```bash
go build ./...                  # build all packages
go run . <command> [flags]      # run dreamer CLI
go test ./...                   # full test suite
go test ./cmd -run TestName     # single test (any package)
go test -race ./...             # race detector
```

No lint/format target is wired in; use `go vet ./...` and `gofmt -w .` directly.

## Architecture

`dreamer` is a single-binary Go CLI (entry: `main.go` → `cmd.Execute`) that periodically scans local AI coding-assistant chat logs, runs them through Copilot SDK with rule-based prompts, and appends actionable findings to per-project `todos.md` files.

### Command layer (`cmd/`)

Cobra commands registered in `cmd/root.go`:
- `analyze` — one-shot analysis for one project (`--project` required).
- `daemon` — periodic analysis of all configured projects on `daemon.frequency_seconds`. Uses `signal.NotifyContext` for graceful shutdown.
- `config init` — writes default `~/.dreamer/config.yaml`.
- `ls-chats` — debug command to list discovered chat sources for a directory.
- `startup install|uninstall|status` — Windows-only Task Scheduler integration (`schtasks.exe`). Guarded by `runtime.GOOS == "windows"`.

`cmd/runtime.go` is the analysis core (`analyzeProject` → `analyzeSource`). Pipeline per project:
1. `state.LoadState(project.Name)` from `~/.dreamer/<project>/state.json`.
2. `chat.DiscoverChats(project.Path)` — enumerate chat sources.
3. `filterSourcesByLookback` (uses `project.since` or `--since`) + `filterSourcesToAnalyze` (skip sources whose path is in `AnalyzedChatIDs` and `ModifiedTime <= LastRun`).
4. `analyzer.NewClient` → one Copilot client per project, one `Session` per source.
5. `orchestrator.Analyze` runs each enabled rule sequentially as a separate prompt; findings are deduped by `(category, description)`.
6. `output.GenerateTodos` appends a `## Run <RFC3339>` section to `<output_root>/<project>/todos.md`, deduping by SHA-256 hash recorded as `<!-- dreamer:finding:<hex> -->` HTML comments.
7. `state.SaveState` updates `LastRun`, `AnalyzedChatIDs` (union of prior + new IDs), and `UsageStats` counters.

### Chat discovery (`internal/chat`)

`discovery.go` walks well-known per-tool roots and emits `ChatSource{Path, Tool, ModifiedTime}`:
- Copilot CLI: `~/.copilot/session-state/**/*.jsonl` (no scoping — always included)
- Codex: `~/.codex/{sessions,archived_sessions}/**/*.jsonl` — included only when `session_meta.payload.cwd` (probed from first 200 lines) is inside `projectPath`.
- VS Code Copilot chat: `%APPDATA%/Code/User/workspaceStorage/*/chatSessions/*.{json,jsonl}` — scoped by reading sibling `workspace.json`.
- Claude Code: `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/**/*.jsonl` — scoped by probing `cwd`/`workingDirectory`/etc evidence keys.
- Antigravity/Gemini: `${GEMINI_HOME:-~/.gemini}/antigravity/{conversations,inbox}/**/*.{pb,pbtxt,json,jsonl}` plus `<project>/.gemini/antigravity/...`. Home root requires probe evidence; project-local root is implicitly in-scope.

Path scoping uses `normalizeDiscoveryPathForComparison` (case-insensitive on Windows, symlink-resolved). When adding a new chat source type, add a probe to align its CWD evidence with `projectPath` via `pathWithinNormalizedRoot`.

### Chat readers (`internal/chat/readers`)

Per-source decoders that return `[]ChatMessage{Role, Content, Timestamp}`. `runtime.go:readMessagesForAnalysis` dispatches by `source.Tool` and extension. Claude JSONL goes through `SanitizeClaudeMessages` which drops/truncates noisy entries and reports `claudeProcessingDiagnostics` merged into `UsageStats`. Codex/Copilot session JSONL use sanitizer flags on `ReadJSONLWithOptions`.

### Analyzer (`internal/analyzer`)

- `client.go` wraps `github.com/github/copilot-sdk/go`. `NewClient` builds `copilot.ClientOptions` honoring `CopilotHome` (sets `COPILOT_HOME` env), `CLIURL` (external CLI server — incompatible with `UseLoggedInUser`), and `UseLoggedInUser` (defaults to true when no `CLIURL`). Per-session `WorkingDirectory` plus a custom `OnPermissionRequest` enforce read-only mode: file reads/shell/MCP must resolve under the project root; URL fetches allowed; any write/delete/shell write-redirection rejected. Model fallback: if the configured model fails, retry once with `Model: ""` (SDK auto-select).
- `orchestrator.go` runs each enabled `AnalysisRule` as its own prompt against the same session, parses JSON (tolerates ```fenced``` blocks and both bare arrays and `{"findings": [...]}`), applies per-rule confidence `Threshold`, normalizes categories, dedupes by `(category, description)`.
- `rules.go` defines nine built-in `RuleCategory` values with per-rule prompt templates and timeouts. `mergeRuleOverrides` in `cmd/runtime.go` toggles them via `analyzer.rules.<category>.enabled` in config.

### Config (`internal/config`)

`LoadConfig` reads YAML, applies defaults, then validates: every `projects[].path` must resolve to an absolute existing directory (`~` expanded); `daemon.output_root` must be absolute. Pointers (`*bool`) on `UseLoggedInUser`/`AutoStart` distinguish "unset" from explicit `false`.

### Output / state

- Todos live at `<daemon.output_root>/<project>/todos.md`. Existing finding hashes are re-extracted from prior file contents (`### Heading` + `- [ ] desc <!-- dreamer:finding:<hex> -->`) so re-runs are idempotent across both new and pre-existing entries.
- State at `~/.dreamer/<project>/state.json`. `output_root` may differ from state root, but state always uses `~/.dreamer/<project>`.

### Logging (`internal/logging`)

Daemon and analysis emit structured-ish key=value lines via `logging.Logger` to `<output_root>/dreamer.log` plus stderr at the configured level. Tests inject loggers; never call `log.*` directly.

## Conventions

- Commits follow `HOW_TO_COMMIT.md` — explain *why*, 50-char imperative subject, multi-bullet body for multi-part commits.
- Tests sit next to the code (`_test.go`) and use `testing` + `t.TempDir()` heavily; chat-source tests build fixtures on disk rather than mocking the FS.
- Errors are wrapped with `fmt.Errorf("verb noun %q: %w", ...)` for operator-facing context; sentinel errors `errNoChatSources` / `errNoLookbackChatSources` / `errNoNewChatSources` let the daemon distinguish "skip cycle" from real failure.
- Windows-only code paths (`cmd/startup.go`) gate on `runtime.GOOS`; do not assume POSIX path separators in discovery (`normalizeDiscoveryPathForComparison` handles both).
