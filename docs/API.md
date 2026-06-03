# Web UI & API Reference

This document covers the SPA routing structure, REST API endpoints, and the apply/undo strategy engine for the `dreamer` web dashboard interface.

---

## 1. Web UI SPA Routes

The daemon embeds an HTTP server bound to `127.0.0.1:7777` (configurable via the `web:` config block). The UI is loopback-only by design.

| Path | Page | Description |
| :--- | :--- | :--- |
| `/` | Dashboard | Main dashboard summarizing recent analysis runs and system state. |
| `/settings` | Settings | Merged view of operator-owned settings and UI configuration overrides. |
| `/logs` | Logs | Real-time scrollable view of `dreamer.log`. |
| `/providers` | Providers | Overview of configured LLM providers (primary & alternatives). |
| `/projects/{name}` | Project overview | Dashboard for a specific project. |
| `/projects/{name}/findings` | Project findings | Full list of issues, guardrails, and statuses. |
| `/projects/{name}/chats` | Project chats | Discovered chat logs scoped to the project directory. |
| `/projects/{name}/history` | Project history | Chronological pipeline run history for this project. |
| `/jobs` | Jobs dashboard | Scheduler dashboard to configure periodic background analysis. |
| `/jobs/{id}` | Job details and logs | Job definition, schedule, last 10 execution logs, and run history. |

---

## 2. API Endpoints

### General & Health
- `GET /api/health` — Basic daemon health check.
- `GET /api/dashboard` — Global dashboard summary metrics.
- `POST /api/daemon/restart` — Safely shut down and hot-restart the daemon worker pools.
- `GET /api/logs/tail` — Fetch the last $N$ lines of `dreamer.log`.
- `GET /api/events` — Server-Sent Events (SSE) stream of active pipeline run updates.

### Projects Management
- `GET /api/projects` — List registered projects.
- `POST /api/projects` — Register a new project directory (resolves absolute path, inserts defaults).
- `GET /api/projects/{name}` — Retrieve detailed settings and stats for a project.
- `DELETE /api/projects/{name}` — Remove a project from config (preserves overlay comments).
- `POST /api/projects/{name}/run` — Trigger an on-demand analysis pass immediately.

### Chat Sources & History
- `GET /api/projects/{name}/chats` — Retrieve discovered chat sources for a project.
- `DELETE /api/projects/{name}/chats` — Dismiss/remove a specific discovered chat source file from tracking.
- `POST /api/projects/{name}/chats:bulk-delete` — Batch remove multiple chat sources.
- `GET /api/projects/{name}/history` — Retrieve run history logs for a project.

### Findings & Remediation
- `GET /api/projects/{name}/findings` — Retrieve findings for a project (supports category & state filters).
- `GET /api/projects/{name}/findings/{hash}` — Retrieve full details of a single finding.
- `POST /api/projects/{name}/findings/{hash}/apply` — Execute the finding's `apply` rule against the codebase.
- `POST /api/projects/{name}/findings/{hash}/undo` — Revert an applied finding using the stored pre-image snapshot.
- `POST /api/projects/{name}/findings/{hash}/dismiss` — Mark a finding as ignored (so it won't reappear or block builds).
- `POST /api/projects/{name}/findings/{hash}/undismiss` — Restore a dismissed finding.
- `POST /api/projects/{name}/findings/{hash}/resolve` — Mark a finding manually resolved.
- `POST /api/projects/{name}/findings/{hash}/unresolve` — Revert a manually resolved state.

### Configuration & Systems
- `GET /api/providers` — List supported and active provider plugins.
- `GET /api/settings` — Get the current merged config (base `config.yaml` + UI override config).
- `PUT /api/settings` — Update preferences (writes exclusively to `ui-overrides.yaml`).
- `GET /api/fs/exists` — Validate if a folder path exists locally.
- `POST /api/fs/pick-directory` — Trigger a native OS directory picker dialog (returns absolute folder path).

### Background Scheduler (Jobs)
- `GET /api/jobs` — List all configured background analysis jobs.
- `POST /api/jobs` — Create a new background analysis job (TUI fallback).
- `POST /api/jobs/preview` — Dry-run execute a job layout to preview execution output.
- `GET /api/jobs/health` — Diagnostically verify system runner and cron file permissions.
- `GET /api/jobs/audit` — Retrieve system-wide execution audit logs.
- `GET /api/jobs/{id}` — Fetch configuration details for a job.
- `PATCH /api/jobs/{id}` — Modify settings/schedule of a job.
- `DELETE /api/jobs/{id}` — Delete a job and purge its historical run records.
- `POST /api/jobs/{id}/run` — Trigger a background job run immediately in the background pool.
- `POST /api/jobs/{id}/pause` — Suspend periodic scheduling for a job.
- `POST /api/jobs/{id}/resume` — Re-enable periodic scheduling for a job.
- `GET /api/jobs/{id}/runs` — Fetch execution records list for a specific job.
- `GET /api/jobs/{id}/runs/{rid}` — Fetch detailed state and results of a single job run execution.
- `GET /api/jobs/{id}/runs/{rid}/activity` — Retrieve or stream stdout/stderr lines of an active background run.

---

## 3. Apply / Undo Strategy Engine

Findings with an `apply` block (categories: `doc`, `lint-rule`, `ci-check`, `config`) can be automated directly from the web UI.

### Supported Strategies
- **`append-section`**: Adds a new heading/bullet block to the end of a markdown or structured text file.
- **`replace-section`**: Targets an existing block/heading and swaps its body content.
- **`insert-after`**: Searches for a matching token/comment and injects the remediation code block immediately after.
- **`append-file`**: Adds lines directly to the bottom of the file.
- **`replace-file`**: Replaces the entire file contents.

### Reversals & Safety Checks
- Every apply action stores a state reversal entry under `<output_root>/<project>/state.json`.
- The entry preserves the original pre-image file bytes, content hashes (SHA-256) before the action, and target offsets.
- **Collision Protection**: If the target file is edited or drifts after the finding is applied (causing the current SHA-256 to mismatch the post-apply SHA-256), the UI refuses to undo and returns a `409 Conflict` to prevent accidental loss of developer edits.
