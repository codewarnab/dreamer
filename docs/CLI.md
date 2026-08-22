# CLI Commands Reference

This document provides a detailed reference for all command-line interface (CLI) commands, flags, and TUI flows in the `dreamer` tool.

---

## 1. Core Commands

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
                [--parallel]
                [--jobs <n>]
                [--chunk-size <bytes>]
                [--json]
                [--no-color]
                [--quiet]
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
| `--json`       |       | Machine-readable JSON output.                                                |
| `--no-color`   |       | Disable colored output (also respects NO_COLOR env var).                      |
| `--quiet`      | `-q`  | Suppress decorative output for machine/agent use.                            |

### `dreamer daemon`

Run periodic analysis for every project in the global config. Also starts the embedded web server (when `web.enabled` is true).

```bash
dreamer daemon [--config <path>]
               [--parallel] [--jobs <n>] [--chunk-size <bytes>]
```

- Uses `daemon.frequency_seconds` from config (default 3600s).
- Runs one cycle immediately, then repeats on that interval.
- Concurrent workers process projects in parallel (`max_concurrent_jobs`).
- Auth failure on one project is logged and the daemon continues.
- Graceful shutdown via Ctrl+C (SIGTERM on Linux).

### `dreamer start`

Start the daemon as a detached background process. The daemon writes its PID to `<output_root>/dreamer.daemon.lock` and logs to `<output_root>/dreamer.log`.

```bash
dreamer start [--config <path>] [--parallel] [--jobs <n>] [--chunk-size <bytes>]
```

- `--parallel`, `-p`: Forward parallel execution mode to the daemon process.
- `--jobs`, `-j`: Forward parallel worker session cap to the daemon.
- `--chunk-size`: Forward custom transcript chunk byte size to the daemon.

### `dreamer stop`

Stop the running daemon by reading its PID from the lockfile and requesting shutdown.

```bash
dreamer stop [--config <path>]
```

- Preferred path: writes the sentinel file `<output_root>/dreamer.daemon.stop`; the daemon polls for it, cancels its context, and shuts down with full state flushing. This works from every shell (Git Bash `kill`, PowerShell, cmd) and is the **only graceful** mechanism on Windows, where a detached daemon cannot receive SIGTERM.
- Fallback: if the daemon does not exit within 20s (wedged or pre-stop-file version), it is force-terminated via `taskkill /T /F` on Windows or a process-group signal on Unix.
- The daemon also removes any leftover sentinel on exit so a future start is not immediately stopped.

### `dreamer status`

Show the analysis job queue status.

```bash
dreamer status [--json] [--all] [--project <name>]
```

| Flag        | Short | Effect                                      |
|-------------|-------|---------------------------------------------|
| `--json`    | `-j`  | Machine-readable JSON output.               |
| `--all`     | `-a`  | Show all history (not just last 24h).       |
| `--project` | `-P`  | Filter to one project.                      |

---

## 2. Setup & Config

### `dreamer setup`

Interactive TUI wizard that writes `<UserConfigDir>/dreamer/config.yaml`. Built with Bubble Tea + Lip Gloss. Supports back-navigation (left/esc).

```bash
dreamer setup [--advanced] [--force] [--no-startup] [--non-interactive --provider <id> --output-root <dir> [--model <model>] [--frequency <seconds>]]
```

| Flag                 | Short | Effect                                                                               |
|----------------------|-------|--------------------------------------------------------------------------------------|
| `--advanced`         |       | Branch into advanced steps (log level, rule timeout, parallel, etc.)                 |
| `--force`            |       | Overwrite existing config.yaml.                                                      |
| `--no-startup`       |       | Skip the startup-install step.                                                       |
| `--non-interactive`  |       | Bypasses the TUI wizard to write config directly from CLI flags.                      |
| `--provider`         |       | Provider ID (required for `--non-interactive`). Must be a registered id; unknown ids fail at setup time. |
| `--output-root`      |       | Root directory to write analysis findings and logs (required for `--non-interactive`).|
| `--model`            |       | Custom model name override.                                                          |
| `--frequency`        |       | Sleep duration in seconds between daemon runs.                                        |

**Default TUI flow (5 steps):**
1. **Provider (`1/5`)**: Select default provider.
2. **Model (`2/5`)**: Select model for the chosen provider.
3. **Frequency (`3/5`)**: Set analysis frequency (in minutes).
4. **Output Root (`4/5`)**: Directory to save reports, logs, and state.
5. **Startup Auto-Start (`5/5`)**: Choose whether to configure OS auto-start daemon.

**Advanced TUI branch (adds 5+ steps, requires `--advanced`):**
6. **Log Level (`6/10`)**: Choose logging verbosity.
7. **Rule Timeout (`7/10`)**: Set rule execution timeout (in seconds).
8. **Parallel Mode (`8/10`)**: Enable or disable parallel execution.
   - **Max Concurrency (`8.5/10`)**: Set concurrency limit (only if parallel mode is enabled).
9. **Max Chunk Bytes (`9/10`)**: Set maximum transcript bytes per analyzer chunk.
10. **First Project (`10/10`)**: Choose whether to configure a project immediately.
    - **Project Path (`10/10`)**: Absolute path to the project directory.
    - **Project Name (`10/10`)**: Display name for the project.
    - **Project Since (`10/10`)**: Lookback window for chat discovery.
11. **Summary**: Review and write the generated YAML configuration.

### `dreamer add [path]`

Append a project to `config.yaml`. Preserves YAML comments via the `yaml.v3` Node API; rejects duplicates by name or path.

```bash
dreamer add [path] [-n <name>] [-s <window>]
```

- `path` defaults to `.` (current directory).
- `-n, --name`: Project name override (defaults to basename of path).
- `-s, --since`: Lookback window for chat history (e.g. 30m, 1h, 1d, 1w, 1mo, lifetime. Defaults to 24h).

### `dreamer remove <name>`

Remove a project from `config.yaml`. Preserves YAML comments and other keys via the `yaml.v3` AST Node API. The name is case-sensitive and must match exactly.

```bash
dreamer remove <name>
```

### `dreamer startup`

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

---

## 3. Inspect

### `dreamer ls-chats`

List discovered chat sources for a working directory.

```bash
dreamer ls-chats [--project-path <dir>]
```

### `dreamer runs <project>`

List captured analysis runs (every LLM prompt/response pair dreamer stored, including calls that failed to parse).

```bash
dreamer runs <project> [--run <run-id>] [-o <output-root>]
```

- Without `--run`: one row per run — id, kind (`analysis`/`replay`), started time, provider/model, call count, worst status.
- With `--run <id>`: every captured call of that run — index, phase, chunk, status (`ok` / `parse_failed` / `error`), duration, error head.
- Pair with `dreamer replay` to recover dropped mistakes or compare models.

### `dreamer replay <project> <run-id>`

Re-run one captured call. Two modes:

```bash
# Re-parse: decode the stored raw response again with the current rule packs.
# Free — no LLM call. Recovers mistakes that were dropped on a parse failure.
dreamer replay <project> <run-id> --call <index> [--json]

# Re-send: send the exact stored prompt back through the provider.
# Optionally override provider/model to compare models on identical input.
# The exchange is captured as a new run tagged as a replay of the parent.
dreamer replay <project> <run-id> --call <index> --resend [-P <provider>] [--model <model>] [--json]
```

- Phase-1 calls only; phase-2 (finding synthesis) calls cannot be replayed.
- `--resend` performs a live LLM call and can take minutes; results land under `<outputRoot>/<project>/runs/<newRunID>/` with `parent_run_id` lineage.

### `dreamer web`

Open the dreamer web dashboard. By default it discovers a running daemon's web server and prints (or, with `--open`, launches) the URL.

```bash
dreamer web [--open]
```

With `--serve`, it runs a standalone, read-only web server in the foreground — no daemon required. This is handy for browsing prior findings, history, and chat sources without keeping the daemon running.

```bash
dreamer web --serve [--open] [--port <n>] [--dev] [--dev-dir <dir>]
```

- `--serve` always serves, even when `web.enabled: false` in config (that gate only governs the daemon's embedded server).
- `--port <n>` overrides the configured port. `--port 0` binds an ephemeral port and writes it to `<output_root>/web.port` so a separate `dreamer web` can discover it.
- `--dev`: Enable developer mode (live-reload templates/static files from disk).
- `--dev-dir <dir>`: Override path to `internal/web/` for developer mode. Auto-detected from cwd if omitted.
- Standalone mode is read-only: triggering runs, restarting the daemon, the background-jobs endpoints, and saving settings return `503` because no daemon is backing them. Everything read-only (dashboard, projects, findings, chats, history, providers, log tail, settings view) works.
- Press Ctrl+C to stop.

---

## 4. Background Jobs

### `dreamer jobs`

Launch the interactive Terminal UI (TUI) jobs dashboard. This dashboard lets you view, configure, and monitor scheduled background runs.

```bash
dreamer jobs
```

Or manage jobs directly via CLI subcommands:

- **`list`**: List all configured background jobs.
  ```bash
  dreamer jobs list [--json] [--verbose] [--since <duration>] [--output-root <dir>]
  ```
- **`create [project-path]`**: Configure a new background analysis job. Launches the interactive wizard if no arguments are provided or with `-i/--interactive`.
  ```bash
  dreamer jobs create [project-path] --prompt <text> [--name <name>] [--provider <id>] [--model <model>] [--schedule <kind>] [--every <duration>] [--time-of-day <HH:MM>] [--day-of-week <day>] [--cron <expr>] [--timezone <iana>] [--file-access <mode>] [--writable-paths <paths>] [--dry-run]
  ```
- **`edit <job-id>`**: Modify fields of an existing background job. Only explicitly passed flags will be updated.
  ```bash
  dreamer jobs edit <job-id> [--name <name>] [--prompt <text>] [--provider <id>] [--model <model>] [--schedule <kind>] [--every <duration>] [--time-of-day <HH:MM>] [--day-of-week <day>] [--cron <expr>] [--timezone <iana>] [--file-access <mode>] [--writable-paths <paths>]
  ```
- **`show <job-id>`**: Show detailed job configuration, permissions, schedule, and recent runs history.
  ```bash
  dreamer jobs show <job-id> [--output-root <dir>]
  ```
- **`pause <job-id>`**: Disable a job to stop its scheduled execution.
  ```bash
  dreamer jobs pause <job-id> [--output-root <dir>]
  ```
- **`resume <job-id>`**: Re-enable a paused background job and recalculate its next run time.
  ```bash
  dreamer jobs resume <job-id> [--output-root <dir>]
  ```
- **`delete <job-id>`**: Permanently delete a job and its execution history.
  ```bash
  dreamer jobs delete <job-id> [--yes] [--output-root <dir>]
  ```
- **`run <job-id>`**: Execute a scheduled background job immediately. Used internally by the OS scheduler integration.
  ```bash
  dreamer jobs run <job-id> [--timeout <duration>] [--run-token-file <path>] [--force] [--output-file <path>] [--dry-run]
  ```
- **`runs <job-id>`**: List recent runs for the specified background job.
  ```bash
  dreamer jobs runs <job-id> [--json] [--limit <n>] [--status <status>]
  ```
- **`logs <job-id>`**: View the full log output (provider stdout/stderr) for a job run.
  ```bash
  dreamer jobs logs <job-id> [--run <run-id>] [--tail <lines>] [--follow]
  ```
- **`reconcile`**: Synchronize host OS scheduler jobs (e.g. systemd/cron/Task Scheduler) with the internal job store. Corrects drift, missing runs, or obsolete schedule definitions.
  ```bash
  dreamer jobs reconcile [--dry-run] [--verbose] [--json]
  ```
- **`health`**: Diagnostic check on schedule integrity, OS task health, and active directory permission configurations.
  ```bash
  dreamer jobs health [--verbose] [--json]
  ```

---

## 5. System Utilities

### `dreamer version`

Print build version, commit hash, build date, Go compiler version, and system OS/architecture.

```bash
dreamer version [--verbose]
```

- `-v, --verbose`: Show full detailed compiler and commit information instead of just the tag version.

`dreamer --version` is also accepted as a shorthand and prints the same plain one-liner (`dreamer <version>`).

### `dreamer update`

Check GitHub Releases for newer releases of the dreamer tool and automatically replace the running binary.

```bash
dreamer update [--check] [--force]
```

- `--check`: Perform a dry-run checking for updates and comparing local vs latest versions without downloading.
- `--force`: Forcefully re-download and overwrite the current binary even if already up to date.
