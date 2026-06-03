# dreamer — Vision

## One-line

A local background agent that learns from your AI coding assistants' past mistakes on *your* codebase and proposes the smallest guardrails that would have prevented them.

## The problem

AI coding assistants (Copilot, Claude Code, Cursor, Gemini, Kiro, Codex,etc) repeat the same class of mistakes against the same codebase, session after session. They:

- re-introduce nil dereferences the user already fixed,
- forget project-specific invariants written nowhere the agent reads,
- propose the wrong abstraction at the same module boundary,
- skip the same test that catches the same regression,
- violate the same style or layering rule the team agreed on offline.

The information needed to stop this is already on disk: every modern assistant writes its session transcripts locally. Today that history is a graveyard — humans rarely re-read it, and the assistants themselves cannot.

## The product

`dreamer` is a single-binary CLI that runs locally and:

1. **Discovers** every chat transcript on disk that is tied to a given project, across all supported assistants — Copilot CLI, Codex, VS Code Copilot Chat, Claude Code, Antigravity/Gemini, Gemini CLI, Kiro CLI, OpenCode, and Codebuff.
2. **Mines** those transcripts via an LLM for recurring *mistakes the agent made against this codebase*, not generic code smells.
3. **Grounds** each mistake in real symbols by reading the repo under a read-only sandbox.
4. **Proposes** the smallest preventative guardrail per mistake — a lint rule, a missing test, a CI check, a CLAUDE.md/AGENTS.md clarification, a config tweak, or a structural fence.
5. **Writes** human-actionable proposals to a per-project `todos.md`. The web dashboard (v1.5) supports applying guardrails directly to config files with undo support.

The output is not "here are bugs". The output is: *"if rule R / test T / doc D had existed, the agent would not have made mistake M last Tuesday."*

## Design principles

- **Local-first.** Discovery, state, and output stay on the user's machine. The LLM call is the only network boundary; chat content is regex-redacted before it leaves.
- **Read-only by default.** The analyzer runs under a permission handler that allows reads, searches, and URL fetches, and rejects every write, shell side-effect, and out-of-scope path.
- **Proposes, does not apply.** v1 emits Markdown checklist items with config snippets. The human enables the rule, writes the test, edits the doc. Reversibility and trust beat automation.
- **Provider-agnostic.** The analysis LLM is reached either via [ACP](https://agentclientprotocol.com) (one adapter, many agents) or via per-vendor adapters where native SDK/CLI is meaningfully faster. Copilot SDK, Claude CLI, OpenClaude CLI, Codex CLI, Gemini CLI, Kiro (ACP), OpenCode (ACP), Codebuff all sit behind the same `Provider` interface. No third-party scraping; no account-ban risk.
- **Provider choice per project.** Global default in user config dir, override in `<project>/.dreamer/config.yaml`, override-override on the `--provider` CLI flag.
- **Grounded, not hallucinated.** Proposed lint rule IDs are checked against curated allow-lists for the top linters (golangci-lint, eslint, ruff). Unknown linters are tagged `[unverified]`. Detected toolchain is passed into the synthesis prompt so suggestions match the project's actual stack.
- **Incremental.** State per project records analyzed chat IDs, file hashes, and repo HEAD. Re-runs do no LLM work when nothing changed. `--force` bypasses.
- **Idempotent output.** Findings are hashed; `todos.md` is appended-to, never rewritten. Re-runs deduplicate across prior and current entries.

## What v1 ships

- `dreamer analyze --path <dir> [--provider <id>] [--force] [--dry-run]`
- `dreamer daemon` — periodic scan of all configured projects, spawns embedded web server.
- `dreamer setup` — interactive TUI wizard for config creation.
- `dreamer add [path]` — append a project to config.
- `dreamer ls-chats`, `dreamer web`, `dreamer startup` — utilities.
- Six guardrail categories: `lint-rule`, `test`, `ci-check`, `doc`, `config`, `refactor-boundary`. Each category's prompt + JSON schema is configurable via YAML so users can tune without code edits.
- Two-phase orchestration: *mistake extraction* → *guardrail synthesis*. Phase 2 runs only when phase 1 yields ≥1 mistake. `--dry-run` stops after phase 1 for cheap previews.
- Toolchain detector (Go, JS/TS, Python, Rust) that biases proposals toward tools the project already uses.
- Regex-based secret redaction with user-extensible patterns.
- Hard-fail auth UX with the exact remediation command — never silent fallback.

## What v1.5 adds over v1

- **Web dashboard** at `127.0.0.1:<port>` with per-project findings, apply/undo, live activity, settings CRUD.
- **Apply engine** — guardrails can be applied directly to config files (`append-section`, `replace-section`, `insert-after`, `append-file`, `replace-file`) with containment checks and undo.
- **UI overlay config** (`ui-overrides.yaml`) — hot-reloadable settings without touching `config.yaml`.
- **Per-finding lifecycle** — findings transition through applied/dismissed/resolved states.
- **Setup wizard** replacing `config init`.
- **Default provider** switched to `openclaude-cli`.

## What remains out of scope

- Cross-project pattern mining ("you make this mistake in every Go repo").
- Cloud sync, shared rule libraries, team-level aggregation.

## Success criteria

`dreamer` is working if:

- Pointing it at this repo produces at least one realistic, actionable guardrail proposal that maps to a real symbol in the codebase.
- Zero writes occur outside `<UserConfigDir>/dreamer/<project>/` (or the user-chosen `--output-dir`).
- A second run with no new chats and no repo HEAD change makes zero LLM calls.
- Switching `--provider` between Copilot SDK, Claude CLI, and an ACP-conformant agent produces equivalent output shapes with no code changes.
- A user reading `todos.md` can ratify a proposal in under a minute: the category, the mistake it prevents, the exact tool + rule ID, and a copy-pasteable config snippet are all present.

## Non-goals worth naming

- **Not a code reviewer.** Static analyzers and human review already cover "is this code good". `dreamer` covers "is the *assistant* making the same mistake on this code, repeatedly".
- **Not a chat archiver.** Existing tools index and search chats. `dreamer` reads chats to mine *patterns*, not to preserve them.
- **Not a prompt-injection defense layer.** Chat redaction protects user secrets in transit; it does not vet the analyzer's output for adversarial content embedded in chats. Treat proposals as suggestions, not commands.