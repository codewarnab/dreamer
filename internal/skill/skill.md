# dreamer CLI — Agent Skill

Quick reference for AI agents using dreamer as a subprocess.

## What dreamer does

Discovers chat history from AI coding tools (Copilot, Claude, Codex, Gemini, etc.), analyzes recurring engineering patterns, and appends actionable findings to per-project `todos.md` files.

## Commands

### Core

| Command | Purpose | Key flags |
|---------|---------|-----------|
| `analyze` | One-shot analysis for a project | `--path` (required), `--json/-j`, `--force/-f`, `--provider/-P`, `--since/-s`, `--dry-run` |
| `daemon` | Periodic analysis of all projects | Runs until interrupted (SIGINT/SIGTERM) |
| `jobs` | Background job management | `list`, `show`, `create`, `edit`, `delete`, `run`, `pause`, `resume`, `health`, `reconcile` |

### Setup

| Command | Purpose | Key flags |
|---------|---------|-----------|
| `setup` | Write config.yaml | `--non-interactive` + `--provider` + `--output-root` (agent mode), or TUI |
| `add` | Append a project to config | `add /path/to/project` |

### Inspect

| Command | Purpose | Key flags |
|---------|---------|-----------|
| `status` | Job queue status | `--json/-j`, `--all/-a`, `--project/-P` |
| `ls-chats` | List discovered chat sources | `--project-path` |

## Agent-friendly flags (all commands)

- `--no-color` — strip ANSI escape codes (also respects `NO_COLOR` env var)
- `--quiet/-q` — suppress banners, hints, decorative output
- `--config/-c` — path to config file

## Typical agent workflow

```bash
# 1. Configure (one-time)
dreamer setup --non-interactive \
  --provider copilot \
  --output-root ~/.dreamer/output \
  --force

# 2. Run analysis
dreamer analyze --path /path/to/project --json

# 3. Check job status
dreamer status --json

# 4. Read findings
cat ~/.dreamer/output/project-<name>/todos.md
```

## Exit codes

| Code | Meaning | Agent action |
|------|---------|-------------|
| 0 | Success | Parse output |
| 1 | General error | Read stderr, fix and retry |
| 2 | Config/usage error | Fix flags or config, don't retry |
| 3 | Provider error | Check provider install, may retry later |

## JSON output

`analyze --json` returns:
```json
{
  "todos_path": "/path/to/todos.md",
  "findings": 3,
  "mistakes": 5,
  "warnings": 0,
  "sources_analyzed": 2,
  "messages_read": 147,
  "cache_hit": false,
  "provider": "copilot",
  "mistakes_found": true
}
```

`status --json` returns an array of job objects.

## Common patterns

- **Incremental analysis**: dreamer caches by default. Use `--force` to re-analyze.
- **Lookback window**: `--since 1d` (valid: 30m, 1h, 1d, 1w, 1mo, lifetime).
- **Provider override**: `--provider claude` overrides config-level default.
- **Dry run**: `--dry-run` analyzes but doesn't write findings.

## Gotchas

- `setup` without `--non-interactive` launches a TUI that will hang your subprocess.
- `jobs create` without `--prompt` also launches a TUI. Always pass `--prompt "..."`.
- `jobs run` requires `DREAMER_RUN_TOKEN` env var to be set.
- Config path defaults to `<UserConfigDir>/dreamer/config.yaml`.
- Output root must be an absolute path.
