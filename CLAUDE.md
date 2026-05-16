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

`cmd/analyze_pipeline.go:executeAnalyze` is the analysis core (spec §17). Both `analyze` and `daemon` call into it. Pipeline per project:
1. `resolveAbsoluteProjectPath` + `DeriveProjectName` derive the absolute path and the `project-<basename>[-<hash>]` directory name used under the output root.
2. `config.LoadProjectFileConfig` + `cfg.ResolveProviderConfig` resolve the active provider id and `ProviderBlock` (global → per-project → `--provider`).
3. `chat.DiscoverChats(projectPath)` enumerates chat sources; `filterSourcesByLookback` (from `--since`) prunes them.
4. `state.Load(outputRoot, projectName)` reads `<outputRoot>/<project>/state.json`; `state.HashFile` + `state.ChatCacheKey(path, fileHash, repoHeadSHA)` build a per-source cache key. If every key matches and `RepoHeadSHA` is unchanged (and `--force` is off), the run is a cache hit and exits early.
5. `mergeRulePacks` loads embedded YAML rule packs and applies `analyzer.rules.<category>.enabled` toggles + `rule_timeout_seconds` from global and per-project config.
6. `buildRedactor` + `buildRedactedTranscript` read each source via `readMessagesFromSource` (a thin dispatch into `chat.ProviderFor(source.Tool).ReadMessages`); per-source sanitization (Claude `SanitizeClaudeMessages`, Codex/Copilot JSONL flags) lives inside each provider's `ReadMessages` method.
7. `toolchain.Detect` + `buildCodebaseContext` assemble grounding context for the prompts.
8. `analyzer.NewProvider(id, providerCfg)` → `provider.Start` → `provider.NewSession` (read-only, working-dir scoped). A `loggingSession` wraps the raw session for prompt/response logging.
9. `analyzer.NewOrchestrator(packs).Run` executes a two-phase analysis (mistake extraction → guardrail synthesis); findings are deduped against `state.FindingHashes` via `ExistingHashes`.
10. `output.GenerateTodos` appends a `## Run <RFC3339>` section to `<outputRoot>/<project>/todos.md`, deduping by SHA-256 hash recorded as `<!-- dreamer:finding:<hex> -->` HTML comments.
11. `state.Save(outputRoot, projectName, …)` updates `LastRunUTC`, `RepoHeadSHA`, `ChatHashes` (new cache-key map), `FindingHashes` (union), `ProviderUsage`, and `UsageStats` counters.

`cmd/helpers.go` holds shared Cobra-layer helpers: `defaultConfigFileName`, `resolveConfigPath`, `expandHomePath`. The `readMessagesFromSource` dispatch lives in `internal/pipeline/transcript.go` and now resolves to a registered `chat.ChatSourceProvider`.

### Chat discovery (`internal/chat`)

`discovery.go` is a thin orchestrator: `DiscoverChats` resolves env vars into a `DiscoveryEnvironment` and `discoverChatsFromEnvironment` runs every registered provider's `Discover` method in parallel (via `golang.org/x/sync/errgroup`), then merge-sorts the result by `ModifiedTime` desc, tie-breaking on `Path` asc. Each source lives in its own `source_<name>.go` file with a `ChatSourceProvider` implementation that self-registers in `init()`. Shared helpers sit in `paths.go` (`normalizeDiscoveryPathForComparison`, `pathWithinNormalizedRoot`, …) and `probe.go` (`walkChatFiles`, `probeJSONLForCWD`, `recursiveExtract`, `extractPathValue`).

Per-provider discovery rules:
- Copilot CLI (`source_copilot.go`): `~/.copilot/session-state/**/*.jsonl` (no scoping — always included).
- Codex (`source_codex.go`): `~/.codex/{sessions,archived_sessions}/**/*.jsonl` — included only when `session_meta.payload.cwd` (probed from first 200 lines) is inside `projectPath`.
- VS Code Copilot chat (`source_vscode.go`): `%APPDATA%/Code/User/workspaceStorage/*/chatSessions/*.{json,jsonl}` — scoped by reading sibling `workspace.json`.
- Claude Code (`source_claude.go`): `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/**/*.jsonl` — scoped by probing `cwd`/`workingDirectory`/etc evidence keys.
- Antigravity/Gemini (`source_antigravity.go`): `${GEMINI_HOME:-~/.gemini}/antigravity/{conversations,inbox}/**/*.{pb,pbtxt,json,jsonl}` plus `<project>/.gemini/antigravity/...`. Home root requires probe evidence; project-local root is implicitly in-scope.
- Gemini CLI (`source_gemini_cli.go`): `${GEMINI_HOME:-~/.gemini}/tmp/*/chats/*.jsonl` — scoped by probing `directories` or cwd evidence keys.
- OpenCode (`source_opencode.go`) and Kiro CLI (`source_kiro.go`): SQLite-backed; rows filtered by stored `Directory` column inside `projectPath`.

Path scoping uses `normalizeDiscoveryPathForComparison` (case-insensitive on Windows, symlink-resolved). When adding a new chat source: create `source_<name>.go` with a struct implementing `ChatSourceProvider`, register it via `registerProvider(...)` in `init()`, add the `SourceType` constant to `types.go`, and (if needed) extend `DiscoveryEnvironment` with whatever inputs the new provider reads.

### Chat readers (`internal/chat/readers`)

Per-source decoders that return `[]ChatMessage{Role, Content, Timestamp}`. Each provider's `ReadMessages` method is the only caller; sanitization (`SanitizeClaudeMessages` for Claude, `SanitizeCodex`/`SanitizeCopilotSession` flags on `ReadJSONLWithOptions`) lives in the relevant provider, so `buildRedactedTranscript` stays provider-agnostic.

### Analyzer (`internal/analyzer`)

- `client.go` wraps `github.com/github/copilot-sdk/go`. `NewClient` builds `copilot.ClientOptions` honoring `CopilotHome` (sets `COPILOT_HOME` env), `CLIURL` (external CLI server — incompatible with `UseLoggedInUser`), and `UseLoggedInUser` (defaults to true when no `CLIURL`). Per-session `WorkingDirectory` plus a custom `OnPermissionRequest` enforce read-only mode: file reads/shell/MCP must resolve under the project root; URL fetches allowed; any write/delete/shell write-redirection rejected. Model fallback: if the configured model fails, retry once with `Model: ""` (SDK auto-select).
- `orchestrator.go` runs each enabled `AnalysisRule` as its own prompt against the same session, parses JSON (tolerates ```fenced``` blocks and both bare arrays and `{"findings": [...]}`), applies per-rule confidence `Threshold`, normalizes categories, dedupes by `(category, description)`.
- `rules.go` defines built-in `RuleCategory` values with per-rule prompt templates and timeouts. `mergeRulePacks` + `applyRuleToggles` in `cmd/analyze_pipeline.go` toggle them via `analyzer.rules.<category>.enabled` in global or per-project config.

### Config (`internal/config`)

`LoadConfig` reads YAML, applies defaults, then validates: every `projects[].path` must resolve to an absolute existing directory (`~` expanded); `daemon.output_root` must be absolute. Provider knobs (`model`, `copilot_home`, `cli_url`, `use_logged_in_user`, `auto_start`, `command`, `env`, …) live under `providers:<id>:` and are resolved per-call by `Config.ResolveProviderConfig`. `AnalyzerConfig` carries only analyzer-wide knobs: `rule_timeout_seconds` and per-category `rules.<category>.enabled` toggles. Pointers (`*bool`) on `UseLoggedInUser`/`AutoStart` in `ProviderBlock` distinguish "unset" from explicit `false`.

### Output / state

- Todos live at `<daemon.output_root>/<project>/todos.md`. Existing finding hashes are re-extracted from prior file contents (`### Heading` + `- [ ] desc <!-- dreamer:finding:<hex> -->`) so re-runs are idempotent across both new and pre-existing entries.
- State at `<output_root>/<project>/state.json` (same root as todos). The `--output-dir` flag overrides `daemon.output_root` for a single `analyze` run.

### Logging (`internal/logging`)

Daemon and analysis emit structured-ish key=value lines via `logging.Logger` to `<output_root>/dreamer.log` plus stderr at the configured level. Tests inject loggers; never call `log.*` directly.

## Conventions

- Commits follow `HOW_TO_COMMIT.md` — explain *why*, 50-char imperative subject, multi-bullet body for multi-part commits.
- Tests sit next to the code (`_test.go`) and use `testing` + `t.TempDir()` heavily; chat-source tests build fixtures on disk rather than mocking the FS.
- Errors are wrapped with `fmt.Errorf("verb noun %q: %w", ...)` for operator-facing context.
- Windows-only code paths (`cmd/startup.go`) gate on `runtime.GOOS`; do not assume POSIX path separators in discovery (`normalizeDiscoveryPathForComparison` handles both).
