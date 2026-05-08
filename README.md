# dreamer

`dreamer` discovers Copilot CLI, VS Code Copilot, Claude Code, and Antigravity/Gemini chat history, analyzes recurring engineering patterns, and writes actionable todos.

## Quick start

```bash
dreamer config init
```

Creates `~/.dreamer/config.yaml` (use `--force` to overwrite an existing file).

Add at least one project entry in that config with an **absolute** `path`.

## Commands

### `dreamer config init`

Create a default config file:

```bash
dreamer config init [--force]
```

### `dreamer analyze`

Run one analysis pass for a configured project:

```bash
dreamer analyze --project <name> [--config <path>]
```

- `--project` is required.
- Default config path: `~/.dreamer/config.yaml`.
- Requires GitHub Copilot CLI authentication (`copilot auth login`).

### `dreamer daemon`

Run periodic analysis for all configured projects:

```bash
dreamer daemon [--config <path>]
```

- Uses `daemon.frequency_seconds` from config.
- Runs one cycle immediately, then repeats on that interval.

### Analyzer settings

Configure SDK client behavior in `~/.dreamer/config.yaml`:

```yaml
analyzer:
  model: gpt-5.3-codex
  use_logged_in_user: true
  auto_start: false
  copilot_home: ""
  cli_url: ""
  rules: {}
```

- If the configured model is unavailable, Dreamer retries with the SDK auto-selected model.
- Analyzer sessions run in read-only mode: read/search + web URL fetch + read-only tools are allowed; file writes/edits/deletes are denied.

### `dreamer ls-chats`

List discovered chat sources:

```bash
dreamer ls-chats [--project-path <dir>]
```

- Default `--project-path` is current working directory.
- Prints: `TOOL`, `MODIFIED_AT`, `PATH`.
- Returns an error when no sources are discovered.
- Discovers sources from:
  - `~/.copilot/session-state/**/*.jsonl`
  - `~/.codex/sessions/**/*.jsonl` (active Codex sessions)
  - `~/.codex/archived_sessions/**/*.jsonl` (archived Codex sessions)
  - `%APPDATA%\\Code\\User\\workspaceStorage\\*\\chatSessions\\*.{json,jsonl}`
  - `~/.claude/projects/**/*.jsonl` (or `$CLAUDE_CONFIG_DIR/projects/**/*.jsonl` when set)
  - `~/.gemini/antigravity/{conversations,inbox}/**/*.{pb,pbtxt,json,jsonl}` (or `$GEMINI_HOME/antigravity/...` when set)
  - `<project>/.gemini/antigravity/{conversations,inbox}/**/*.{pb,pbtxt,json,jsonl}`
- Codex rows are emitted with `TOOL=codex-session-jsonl`.
- Codex files are included only when `session_meta.payload.cwd` (including `{"type":"session_meta","payload":...}` variants) resolves inside `--project-path`.

