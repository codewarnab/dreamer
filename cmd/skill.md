# dreamer CLI — Agent Skill File

This document teaches AI agents how to use the `dreamer` CLI efficiently.
Run `dreamer --skill` to print this file.

## What dreamer does

dreamer scans AI coding assistant chat logs, finds recurring engineering
mistakes, and writes actionable todos to a project's `todos.md` file.

## Key commands

### `dreamer analyze --path <dir>`

Run a one-shot analysis. This is the primary command for agents.

```
dreamer analyze --path /path/to/project --json
dreamer analyze --path /path/to/project --json --provider copilot
dreamer analyze --path /path/to/project --json --force
```

**Flags:**
- `--path` (required) — project directory to analyze
- `--json` / `-j` — output structured JSON instead of text
- `--provider` — override provider (default: from config)
- `--force` — re-analyze even if cache hit
- `--since` — lookback window (e.g. `1h`, `1d`, `1w`, `lifetime`)
- `--output-dir` — override output directory
- `--dry-run` — analyze without writing todos
- `--permissive` — allow unverified rule ids

**JSON output fields:**
- `provider` — which analyzer provider was used
- `sources_analyzed` — number of chat sources processed
- `messages_read` — total messages read
- `mistakes` — distinct mistakes identified
- `findings_added` — new findings written to todos.md
- `warnings` — analysis warnings
- `todos_path` — path to the output todos.md file
- `cache_hit` — true if no new data since last run

### `dreamer status`

Show job queue status.

```
dreamer status --json
dreamer status --json --project my-project
```

### `dreamer jobs list`

List background jobs.

```
dreamer jobs list --json
```

### `dreamer ls-chats --project-path <dir>`

Discover available chat sources for a project. Output is TSV.

### `dreamer setup --non-interactive`

Write config without TUI (agent-safe).

```
dreamer setup --non-interactive --provider copilot --output-root /path/to/output
dreamer setup --non-interactive --provider claude --model claude-sonnet-4-5-20250929 --output-root /tmp/dreamer --force
```

**Required flags:** `--provider`, `--output-root`
**Optional flags:** `--model`, `--frequency` (seconds), `--force`

## Exit codes

- `0` — success
- `1` — general/unexpected error
- `2` — config/usage error (bad flags, missing config, invalid path)
- `3` — provider error (not installed, unavailable, rate limited)

## Agent-friendly flags

- `--json` / `-j` — structured JSON output (on `analyze`, `status`, `jobs list`)
- `--no-color` — disable ANSI colors (also respects `NO_COLOR` env var)
- `--quiet` / `-q` — suppress banners and decorative output
- `--config` / `-c` — path to config file

## Common workflows

### First-time setup
```bash
dreamer setup --non-interactive --provider copilot --output-root ~/.dreamer/output
```

### Analyze a project
```bash
dreamer analyze --path /path/to/project --json --quiet --no-color
```

### Check if there are new findings
```bash
dreamer analyze --path /path/to/project --json 2>/dev/null | jq '.cache_hit'
```

### List available chat sources
```bash
dreamer ls-chats --project-path /path/to/project
```

## Tips

- Always use `--json --quiet --no-color` for machine consumption
- Use `--force` to bypass cache when you need fresh analysis
- The `--since` flag accepts duration strings: `30m`, `1h`, `1d`, `1w`, `1mo`, `lifetime`
- Config lives at `<UserConfigDir>/dreamer/config.yaml`
- Todos are written to `<output_root>/<project>/todos.md`
- State (cache) is at `<output_root>/<project>/state.json`
