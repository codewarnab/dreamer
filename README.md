# dreamer

`dreamer` discovers Copilot/VS Code chat history, analyzes recurring engineering patterns, and writes actionable todos.

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
  model: gpt-5
  use_logged_in_user: true
  auto_start: false
  copilot_home: ""
  cli_url: ""
  rules: {}
```

### `dreamer ls-chats`

List discovered chat sources:

```bash
dreamer ls-chats [--project-path <dir>]
```

- Default `--project-path` is current working directory.
- Prints: `TOOL`, `MODIFIED_AT`, `PATH`.
- Returns an error when no sources are discovered.

