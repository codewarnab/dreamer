# Shared Helpers Reference

This document catalogues every reusable function and constant in the dreamer codebase that is either **already shared across multiple packages** or **has been extracted to prevent DRY violations**. Before implementing any new helper, check this file to see whether the functionality already exists. If you add a new shared helper, update this document in the same commit.

> [!IMPORTANT]
> If you find yourself writing logic that resembles anything listed here, stop and import the shared version instead. Inline reimplementations — even when "slightly different" — are the root cause of the divergence bugs described in this file.

---

## 1. Background Job Permissions & Validation (`internal/backgroundjobs/permissions.go`)

### `backgroundjobs.MaxPromptSize` — `const = 16 * 1024`

Maximum byte length for a background job prompt. Owned by the `backgroundjobs` package so both the CLI (`cmd/jobs.go`) and the web handler (`internal/web/handlers/jobs.go`) can import it without creating an import cycle.

**Do not define a local constant.** Import this.

```go
if len(prompt) > backgroundjobs.MaxPromptSize {
    return fmt.Errorf("prompt exceeds 16 KiB limit")
}
```

**Current callers:** `cmd/jobs.go`, `internal/web/handlers/jobs.go`

---

### `backgroundjobs.ParseFileAccess(s string) (FileAccessMode, error)`

Converts the user-facing `file_access` string (`"read_only"`, `"selected_writes"`, `"full_workspace"`) to the typed `FileAccessMode` constant, returning a consistent error message for unknown values.

**Do not write a local `switch file_access` block.** Call this instead.

```go
fa, err := backgroundjobs.ParseFileAccess(payload.FileAccess)
if err != nil {
    return err  // message already reads: "file_access must be one of: ..."
}
```

**Current callers:** `cmd/jobs.go`, `internal/web/handlers/jobs.go`

---

### `backgroundjobs.ValidateWritablePaths(projectRoot string, writablePaths []string) error`

Checks that every path in `writablePaths` resolves inside `projectRoot` after symlink resolution and does not target protected directories (`.dreamer`, `.git`, `.claude`, `.codex`, `.copilot`, `.gemini`).

**Current callers:** `internal/web/handlers/jobs.go` (3 sites), `internal/backgroundjobs/runner.go`

---

## 2. Schedule Helpers (`internal/backgroundjobs/schedule.go`)

### `backgroundjobs.ValidateSchedule(s ScheduleSpec) error`

Validates a `ScheduleSpec` — checks kind, `time_of_day`, `day_of_week`, cron expression, and timezone.

**Current callers:** `cmd/jobs.go`, `internal/web/handlers/jobs.go`

---

### `backgroundjobs.NextRun(s ScheduleSpec, now time.Time) (time.Time, error)`

Computes the next scheduled run time after `now` for any `ScheduleSpec` kind (interval, daily, weekly, cron).

**Current callers:** `cmd/jobs.go` (4 sites), `internal/web/handlers/jobs.go` (4 sites)

---

### `backgroundjobs.DefaultTimeoutFor(spec ScheduleSpec) time.Duration`

Derives a session timeout from the schedule: 80% of the interval (capped at 1h) for interval schedules; 30 minutes for all others.

**Current callers:** `internal/backgroundjobs/runner.go`

---

### `backgroundjobs.EveryDuration(spec ScheduleSpec) time.Duration`

Returns the parsed `Every` duration or `defaultIntervalDuration` (1h) as a fallback.

**Current callers:** `internal/backgroundjobs/runner.go` (overlap guard), `schedule.go` (internal)

---

## 3. Job ID Helpers (`internal/backgroundjobs/id.go`)

### `backgroundjobs.GenerateJobID() (string, error)`

Returns a cryptographically random 16-character lowercase hex job ID.

**Current callers:** `cmd/jobs.go`, `internal/web/handlers/jobs.go`

---

### `backgroundjobs.ValidateJobID(id string) error`

Checks that `id` is a valid 16-char lowercase hex string that does not collide with reserved API route segments (`preview`, `health`, `audit`, etc.).

**Current callers:** `cmd/jobs.go`, `cmd/jobs_run.go`, `internal/web/server.go`, `internal/web/handlers/jobs.go` (7 sites), `internal/backgroundjobs/reconcile.go`, `scheduler_linux.go`, `scheduler_darwin.go`, `scheduler_windows.go`

---

## 3b. Schedule Advisory Warnings (`internal/backgroundjobs/schedule.go`)

### `backgroundjobs.ScheduleWarnings(spec ScheduleSpec) []string`

Returns non-fatal advisory warnings for a schedule spec — conditions that are valid but behave differently on OS schedulers:

- **Timezone mismatch**: OS schedulers (Windows Task Scheduler `CalendarTrigger`, systemd `OnCalendar`, launchd calendar intervals) fire in machine-local wall-clock time. A spec timezone differing from the machine zone fires at the wrong hour.
- **DST shifts**: fixed HH:MM triggers in DST-observing zones skip or duplicate runs around transitions (detected via the Jan-vs-July UTC offset heuristic in `observesDST`).

Interval schedules are exempt (they fire relative to install time, not wall clock). Call this after `ValidateSchedule` succeeds and surface the results through CLI stderr and web API `warnings` arrays.

**Current callers:** `internal/web/handlers/jobs.go` (`validateCreatePayload`, `applyJobEdits`), `cmd/jobs.go` (`createAndSaveJob`, edit path)

---

## 4. Lookback / Since Window Parsing (`internal/pipeline/lookback.go`)

### `pipeline.ParseLookbackDuration(amount int, unit string) (time.Duration, bool)`

Converts a `(amount, unit)` pair to a `time.Duration`. Supported units: `m` (minutes), `h` (hours), `d` (days), `w` (weeks), `mo` (30-day months). Returns `(0, false)` for unknown units.

This is the single source of truth for the "since window" unit-to-duration conversion. Use it whenever you have already split a lookback string into its numeric and unit parts.

```go
dur, ok := pipeline.ParseLookbackDuration(amount, unit)
```

**Current callers:** `internal/pipeline/lookback.go` (internally), `internal/web/handlers/chats.go` (`parseSinceWindow`)

> **Note:** The full string parsing front-end (`parseLookbackWindow`, which handles `"7d"`, `"1mo"`, etc. as a single string) is currently unexported. If you need to parse a raw lookback string outside the `pipeline` package, use the `parseSinceWindow` helper in `internal/web/handlers/chats.go` (package-private within `handlers`) or replicate its thin splitting logic and delegate to `ParseLookbackDuration`.

---

## 5. Finding Lifecycle Counts (`internal/web/handlers/dashboard.go`)

### `buildLifecycleCounts(findings map[string]state.FindingState, findingHashes []string) (applied, dismissed, resolved, open int)`

Computes the four lifecycle bucket counts (applied, dismissed, resolved, open) from a project's `Findings` map and `FindingHashes` slice. The `open` count is `len(FindingHashes) − lifecycleTouched`, clamped to zero.

This is the **single source of truth** for the open-findings formula. Any code that counts findings must call this function.

```go
applied, dismissed, resolved, open := buildLifecycleCounts(st.Findings, st.FindingHashes)
```

**Current callers:** `internal/web/handlers/dashboard.go` (`buildDashboard`), `internal/web/handlers/projects.go` (`projectRollup`)

> **Note:** The function is unexported but lives in the `handlers` package. All handler files share it freely.

---

## 6. Provider Health (`internal/web/handlers/dashboard.go` + `internal/web/handlers/providers.go`)

### `buildProviderHealth(usage map[string]state.ProviderUsage) (healthy, seen map[string]bool, totalFailures, totalRuns int64)`

Aggregates `ProviderUsage` entries into healthy/seen sets and failure/run totals. A provider is counted as **healthy** when: `LastError == ""` AND `Runs > 0` AND `LastSuccessUTC` is within the last 24 hours (`providerHealthWindow`).

```go
healthyProviders, seenProviders, failures, runs := buildProviderHealth(st.ProviderUsage)
```

**Current callers:** `internal/web/handlers/dashboard.go`

> **Warning:** The `providerHealthWindow = 24h` recency check in `providers.go:Providers` uses the same predicate. Both sites must agree on what "healthy" means. If the predicate changes, update both files (or consolidate into a shared `isProviderHealthy` helper in `state` or a shared handlers file).

---

## 7. Project Lookup (`internal/web/handlers/chats.go`)

### `findProjectByName(cfg *config.App, name string) *config.ProjectConfig`

Linear scan through `cfg.Projects` returning a pointer to the matching entry, or `nil`. Returns a pointer (not a copy) so callers can read fields without copying and the nil check is idiomatic.

```go
proj := findProjectByName(cfg, name)
if proj == nil {
    http.NotFound(w, r)
    return
}
```

**Current callers (within `handlers` package):** `chats.go` (3 sites), `lifecycle.go`

> **Note:** A near-duplicate `findProject(cfg, name) (config.ProjectConfig, bool)` exists in `findings.go` returning a value + bool. Prefer `findProjectByName` for new code — the pointer form is idiomatic Go for optional lookups and avoids the copy.

---

## 8. Since-Window Parsing (`internal/web/handlers/chats.go`)

### `parseSinceWindow(value string) (time.Duration, bool)`

Parses a project's `since` field (e.g. `"7d"`, `"30d"`, `"1mo"`) into a `time.Duration` and an `enabled` flag. Returns `(0, false)` for empty strings, `"lifetime"`, or invalid formats — meaning the lookback filter is disabled and every source is included.

Internally delegates to `config.IsLifetimeSince` and `pipeline.ParseLookbackDuration`.

```go
lookback, enabled := parseSinceWindow(project.Since)
```

**Current callers (within `handlers` package):** `chats.go` (`listProjectChats`), `projects.go` (`projectsPost` — since validation)

---

## 9. Sparkline Aggregation (`internal/web/handlers/dashboard.go`)

### `buildSparklines(days []state.DaySummary, cutoff30d, cutoff7d time.Time) (sparkline map[string]state.DaySummary, weekTokens, weightedRunMillis, runsForAvg int64, perCategory map[string]int)`

Aggregates history days into a 30-day sparkline map and 7-day token/run totals. Called once per project during dashboard assembly.

**Current callers:** `internal/web/handlers/dashboard.go` (`buildDashboard`)

---

## 10. Schedule Display (`internal/web/handlers/jobs.go`)

### `buildScheduleSummary(s backgroundjobs.ScheduleSpec) string`

Returns a human-readable schedule description (e.g. `"Daily at 09:00 UTC"`, `"Every 30m"`). This is the server-side source of truth; API responses always include the pre-rendered `schedule_summary` field so the frontend never re-implements the formatting.

**Current callers:** `internal/web/handlers/jobs.go` (4 sites in `JobList`, `JobCreate`, `JobPreview`, `JobDetail`, `JobEdit`)

---

## 11. Findings Loading (`internal/web/handlers/findings.go`)

### `loadFindingsFor(deps Deps, outputRoot, projectName string) (map[string]bool, []FindingsEntry, error)`

Single dispatch point for loading findings: uses `deps.FindingsLoader` when injected (tests, alternative backends), otherwise falls back to `defaultLoadFindings` which reads `todos.md` from disk.

**Current callers:** `internal/web/handlers/findings.go` (`ProjectFindings`, `FindingDetail`)

---

### `buildFindingView(entry FindingsEntry, st *state.State, latestRunHashes map[string]bool) FindingView`

Reconciles a raw `FindingsEntry` against the persisted `state.FindingState` to produce a `FindingView` with status, timestamps, and recurrence flag.

**Current callers:** `internal/web/handlers/findings.go` (`ProjectFindings`, `FindingDetail`)

---

## 12. Atomic File Writes (`internal/fsutil/atomic.go`)

### `fsutil.WriteFileAtomic(path string, contentBytes []byte, perm os.FileMode) error`

The single point of truth for crash-safe file writes throughout the codebase. Writes via a temp file + `os.Rename` + fsync so a crash mid-write never leaves a partial file at the target path.

**Always use this instead of `os.WriteFile` or manual temp-file patterns.**

```go
if err := fsutil.WriteFileAtomic(path, data, fsutil.SecretPerms); err != nil {
    return fmt.Errorf("write config: %w", err)
}
```

**Current callers (~18 across the codebase):** `cmd/add.go`, `cmd/remove.go`, `cmd/setup.go`, `internal/state/tracker.go`, `internal/state/history.go`, `internal/web/handlers/projects.go`, `internal/web/handlers/settings.go`, `internal/backgroundjobs/store.go`, `internal/backgroundjobs/runner.go`, `internal/backgroundjobs/runtoken.go`, `internal/output/generator.go`, `internal/fsutil/filelist.go`, `internal/astcheck/baseline.go`, and more.

**Permission constants:** `fsutil.DirPerms` (0755), `fsutil.FilePerms` (0644), `fsutil.SecretPerms` (0600 — use for config files that may hold secrets).

---

## 13. Path Utilities (`internal/fsutil/path.go`)

### `fsutil.ExpandUserHome(p string) (string, error)`
Expands a leading `~` or `~/` into the user's home directory. Returns the input unchanged if it does not start with `~`.

**Current callers:** `internal/pipeline/projectname.go`, `internal/web/handlers/projects.go`, `internal/config/loader.go`

---

### `fsutil.NormalizeRootPath(root string) (string, error)`
Returns the absolute, symlink-resolved, cleaned path. Rejects null bytes. Use for validating user-supplied project paths at API boundaries.

**Current callers:** `internal/web/handlers/projects.go`

---

### `fsutil.CanonicalPath(p string) string`
Resolves `~/` and relative paths to an absolute, cleaned path. On Windows, lowercases the result for case-insensitive comparison.

**Current callers:** `internal/config/yaml_ops.go` (duplicate-project detection)

---

### `fsutil.ResolveSymlinks(abs string) (string, error)`
Resolves symlinks, walking up to the deepest existing ancestor when the leaf does not exist.

**Current callers:** `internal/web/apply/apply.go`, `internal/sandbox/sandbox.go`, `internal/mcpserver/validation.go`

---

### `fsutil.PathWithinRoot(path, root string) bool`
Reports whether `path` is within `root` (or equal to it). Case-insensitive on Windows.

**Current callers:** `internal/web/handlers/fs.go`, `internal/web/apply/apply.go`, `internal/sandbox/sandbox.go`, `internal/mcpserver/validation.go`

---

## 14. File Discovery (`internal/fsutil/filelist.go`)

### `fsutil.ListProjectFiles(projectRoot string, maxFiles int) ([]string, error)`
Returns repo-relative file paths, using `git ls-files` when available and falling back to `filepath.WalkDir`. Results are capped, normalized, and sorted.

**Current callers:** `cmd/filepicker.go`

---

### `fsutil.ShouldSkipDir(name string, isRoot bool, extraDirs ...string) bool`
Returns true for common noise directories (`.git`, `node_modules`, `vendor`, etc.). Pass extra dirs to extend the skip list without modifying this function.

### `fsutil.ShouldSkipFile(name string) bool`
Returns true for binary/media file extensions.

**Current callers:** `internal/analyzer/grounding/detect_files.go`

---

## 15. Config Helpers (`internal/config/`)

### Provider defaults (`defaults.go`)

| Function | Purpose |
|---|---|
| `config.DefaultModelFor(providerID string) string` | Default model string for a provider ID |
| `config.AllModelsFor(providerID string) []string` | All known models for the model picker |
| `config.DefaultSandboxFor(providerID string) string` | Default sandbox mode |
| `config.ModelFallbacksFor(providerID string) []string` | ACP fallback models |
| `config.RemediationMessage(providerID string) string` | Operator-facing setup/recovery hint |
| `config.LookupProviderDefaults(id ProviderID) (ProviderDefaults, bool)` | Full defaults struct |
| `config.AllProviderDefaults() map[ProviderID]ProviderDefaults` | All registered defaults |

**Do not hardcode provider model lists or remediation strings.** They live here and are registered via `RegisterProviderDefaults` in `defaults.go:init()`.

**Current callers:** `internal/web/handlers/providers.go`, `internal/pipeline/pipeline.go`

---

### YAML config mutations (`yaml_ops.go`)

| Function | Purpose |
|---|---|
| `config.AppendProjectToYAML(configBytes []byte, name, path, since string) ([]byte, error)` | Add a project to `config.yaml`, comment-preserving |
| `config.RemoveProjectFromYAML(configBytes []byte, name string) ([]byte, error)` | Remove a project from `config.yaml`, comment-preserving |
| `config.FindMappingChild(node *yaml.Node, key string) (k, v *yaml.Node)` | Low-level YAML AST traversal helper |
| `config.ErrProjectNotFound` | Sentinel error returned by `RemoveProjectFromYAML` |

**Current callers:** `cmd/add.go`, `cmd/remove.go`, `internal/web/handlers/projects.go`

---

### Validation helpers (`loader.go`)

| Symbol | Purpose |
|---|---|
| `config.ValidateProjectName(name string) error` | Validates a project name against reserved names and illegal chars |
| `config.IsLifetimeSince(value string) bool` | Returns true for `""` or any case of `"lifetime"` |
| `config.DefaultSince` | The default lookback string used when `since` is omitted |

**Current callers of `ValidateProjectName`:** `internal/state/tracker.go`, `internal/output/generator.go`, `internal/web/handlers/projects.go`

**Current callers of `IsLifetimeSince`:** `internal/pipeline/lookback.go`, `internal/web/handlers/chats.go`, `internal/web/handlers/projects.go`

**Current callers of `DefaultSince`:** `cmd/analyze.go`, `cmd/add.go`, `cmd/helpers.go`, `internal/pipeline/chunker.go`, `internal/web/handlers/projects.go`

---

## 16. State Persistence (`internal/state/`)

### Core I/O (`tracker.go`)

| Function | Purpose |
|---|---|
| `state.Load(outputRoot, projectName string) (*State, error)` | Read state.json; returns a default state when missing |
| `state.LoadWithResult(outputRoot, projectName string) (LoadResult, error)` | Load + migration metadata |
| `state.Save(outputRoot, projectName string, state *State) error` | Atomic write of state.json |
| `state.PathForProject(outputRoot, projectName string) (string, error)` | Resolves the state.json path |

**Current callers of `Load`:** `dashboard.go`, `projects.go`, `findings.go`, `providers.go`, `chats.go`, `lifecycle.go` (all fall back to direct `Load` when `StateCache` is nil)

**Current callers of `Save`:** `lifecycle.go`, `chats.go`, `pipeline/pipeline.go`

---

### History I/O (`history.go`)

| Function | Purpose |
|---|---|
| `state.LoadHistory(outputRoot, projectName string) (*History, error)` | Read history.json; returns empty history when missing |
| `state.SaveHistory(outputRoot, projectName string, h *History) error` | Atomic write of history.json |
| `state.UpdateHistoryToday(outputRoot, projectName, today string, delta DaySummaryDelta, lock *ProjectLock) error` | Merge a run delta into today's bucket and persist under the shared per-project lock |
| `state.HistoryPath(outputRoot, projectName string) (string, error)` | Resolves the history.json path |

**Current callers of `LoadHistory`:** `dashboard.go` (cache fallback), `history.go` endpoint handler

---

### Read-through cache (`cache.go`)

| Function | Purpose |
|---|---|
| `state.NewStateCache() *StateCache` | Creates an empty mtime-based cache |
| `(*StateCache).GetState(outputRoot, projectName string) (*State, error)` | Cached `Load` — prefer this in web handlers |
| `(*StateCache).GetHistory(outputRoot, projectName string) (*History, error)` | Cached `LoadHistory` |
| `(*StateCache).Invalidate(projectName string)` | Drops a project's cache entry after a write |

**Current callers:** injected via `Deps.StateCache` into every handler; all handlers check `if deps.StateCache != nil` before falling back to direct `state.Load`.

---

### Utility functions (`tracker.go`)

| Function | Purpose |
|---|---|
| `state.HashFile(path string) (string, error)` | SHA-256 hex of a file's contents |
| `state.ChatCacheKey(path, fileHash, repoHeadSHA string) string` | Length-prefixed cache key for one chat file |
| `state.RepoHeadSHA(workingDirectory string, loggers ...*logging.Logger) string` | Current git HEAD SHA; returns `""` on failure |
| `state.TruncateError(msg string) string` | Clamps to 500 runes; appends `…` |

---

### Finding status constants (`findings.go`)

```go
state.FindingStatusApplied    // "applied"
state.FindingStatusDismissed  // "dismissed"
state.FindingStatusResolved   // "resolved"
```

**Used in every finding lifecycle handler and counter.** Do not use raw strings.

---

## 16b. SQLite Chat Source Fingerprints (`internal/chat/`, `internal/chat/readers/`)

SQLite-backed providers (opencode, kiro-cli) store every session as a row inside one shared database file, and `Source.Path` encodes `<dbFile>#<sessionID>`. The incremental cache must never hash that encoded path as a file — use these instead.

| Symbol | Purpose |
|---|---|
| `chat.SourceHasher` (interface, `provider.go`) | Optional provider extension: `SourceHash(source Source) (string, error)` returns a stable per-source content digest. Implemented by the opencode and kiro providers. |
| `readers.OpenCodeReader.SessionFingerprint(dbPath, sessionID string) (string, error)` | Hex digest of one session's message/part aggregates plus `session.time_updated`. Errors when the session row is missing. |
| `readers.KiroReader.ConversationFingerprint(dbPath, conversationID string) (string, error)` | Hex digest of one conversation's value length plus `updated_at`. Errors when the row is missing. |

**Dispatch:** `pipeline.sourceContentHash(src)` prefers `SourceHasher` via `chat.ProviderFor` and falls back to `state.HashFile` for plain-file sources. Do not call `state.HashFile` directly on `chat.Source.Path` in new code.

**Current callers:** `internal/pipeline/cache.go` (`computeCacheKeys`)

---

## 17. Categories (`internal/categories/`)

### `categories.ApplyEligible(c Category) bool`
Returns true if the web UI is allowed to auto-apply findings in this category. The eligible set is the single source of truth — do not maintain a parallel map.

**Current callers:** `internal/web/handlers/findings.go`

---

### `categories.AllApplyEligible() []Category`
Returns only the auto-apply-eligible categories in canonical order.

**Current callers:** `internal/web/apply/apply.go` (builds `EligibleCategories` map at init)

---

### `categories.All() []Category`
All six v1 rule categories in canonical order.

**Current callers:** `internal/analyzer/rules.go`, `internal/mcpserver/validation.go`

---

## 18. Structured Logging (`internal/logging/`)

### `logging.Any(key string, value any) Attr`
Builds a structured `slog.Attr`. Used everywhere a logger call needs a key-value pair.

### `logging.String(key string, value string) Attr`
Same but typed for string values.

### `logging.ErrAttr(err error) []Attr`
Returns a slice of `slog.Attr` for `err`. For `*errs.Error` values, automatically expands `Kind`, `Provider`, `Op`, and `Details` as separate fields. For plain errors, returns `[Any("err", err)]`.

```go
deps.Logger.Error("job run executor", logging.ErrAttr(err)...)
```

**Current callers:** `internal/web/handlers/jobs.go` (~15 sites), `internal/web/server.go`, `internal/backgroundjobs/runner.go`

---

## 19. Git Subprocesses (`internal/gitutil/command.go`)

### `gitutil.Command(parent context.Context, args ...string) (*exec.Cmd, context.CancelFunc)`

Creates a `git` command with a 10-second timeout and `GIT_TERMINAL_PROMPT=0` so local metadata probes cannot hang Dreamer on credential prompts, broken repositories, or slow network filesystems. Callers must `defer cancel()` after creation.

```go
cmd, cancel := gitutil.Command(context.Background(), "rev-parse", "HEAD")
defer cancel()
out, err := cmd.Output()
```

**Current callers:** `internal/state/tracker.go`, `internal/fsutil/filelist.go`, `internal/astcheck/runner.go`

---

## 20. Structured Errors (`internal/errs/`)

### Constructors
| Function | Use case |
|---|---|
| `errs.NotInstalled(provider, op, hint string, cause error) *Error` | Provider binary not on PATH |
| `errs.RateLimit(provider, op string, retryAfter time.Duration, cause error) *Error` | Provider returned 429/quota |
| `errs.ProviderUnavailable(provider, op string, cause error) *Error` | Provider unreachable / spawn failure |
| `errs.ConfigInvalid(field string, value any, cause error) *Error` | Invalid config field |

### Inspection
| Function | Use case |
|---|---|
| `errs.KindOf(err error) Kind` | Extract the `Kind` from an error chain |
| `errs.Is(err error, k Kind) bool` | Check whether `err` chain has a specific `Kind` |
| `errs.ProviderOf(err error) string` | Extract the provider ID from an error chain |

**Current callers:** `internal/analyzer/providers/*/`, `internal/backgroundjobs/runner.go`, `internal/config/loader.go`, `internal/analyzer/orchestrator_chunked.go`

---

## 21. Job Validation Helpers (`internal/web/handlers/jobs.go`)

### `validateCreatePayload(cfg *config.App, payload createPayload, lookup func(string) *backgroundjobs.ProviderMeta) (projectPath string, warnings []string, err error)`

Validates a job create/preview payload: prompt non-empty + size, schedule, provider background-safety, and project existence. Returns the resolved `projectPath`.

**Current callers:** `JobCreate`, `JobPreview` (within `jobs.go`)

---

### `resolveProjectPath(cfg *config.App, projectName string) (string, error)`

Validates `projectName` against `cfg.Projects` and returns the matching `Path`. Rejects names not in config to prevent path traversal.

**Current callers:** `validateCreatePayload` (within `jobs.go`)

---

### `applyJobEdits(job *backgroundjobs.Job, payload editPayload, lookup func(string) *backgroundjobs.ProviderMeta) (scheduleChanged bool, warnings []string, err error)`

Validates and applies a partial PATCH payload to a `Job`. Returns whether the schedule changed (so the caller can reinstall the OS schedule).

**Current callers:** `JobEdit` (within `jobs.go`)

---

## 21. Model List Cache (`internal/analyzer/modellistcache.go`)

### `(*ModelListCache).Load(dir string) error`

Reads `<dir>/model-list-cache.json` and pre-populates the in-memory cache with entries that are no older than 7 days (`modelListDiskTTL`). Returns `nil` when the file does not exist (cold start). Safe to call at daemon startup before the HTTP server binds.

```go
if err := modelListCache.Load(outputRoot); err != nil {
    logger.Warn("model list cache load failed", logging.Any("err", err))
}
```

**Current callers:** `internal/web/server.go` (`attachAPI`, called once at startup)

---

### `(*ModelListCache).Save(dir string) error`

Atomically writes all in-memory cache entries to `<dir>/model-list-cache.json` using `fsutil.WriteFileAtomic`. Called from the background goroutine in `enrichWithLiveModels` after a successful `ListModels` fetch. Failure is logged but never fatal.

```go
if err := cache.Save(cacheDir); err != nil {
    deps.Logger.Warn("model list cache save failed", logging.Any("err", err))
}
```

**Current callers:** `internal/web/handlers/providers.go` (`enrichWithLiveModels` background goroutine)

---

## Frontend Shared Helpers (`internal/web/static/js/app.js`)

The web UI is vanilla JS + Alpine.js + HTMX. Shared client-side knowledge lives in `app.js`, loaded on every page before the per-page scripts. Do not re-derive these inline in a page script or template.

### `window.dreamerAPI`

Single source of truth for HTTP + CSRF. `csrf()` reads the `<meta name="csrf-token">` tag; `requestJSON`/`getJSON`/`postJSON`/`deleteJSON` inject the `X-Dreamer-CSRF` header on mutating requests and throw on non-2xx (with `err.body.error`). Page state objects that keep a `csrf()` method must delegate to `window.dreamerAPI.csrf()` rather than re-querying the meta tag.

**Current callers:** `layouts/layout.html` (`addProjectModal`), `pages/jobs.js`, `pages/job_detail.js`, `pages/settings.js`.

### `window.dreamerJobs`

Job-form serialization shared by the create (jobs list) and edit (job detail) flows so the two never drift:
- `buildSchedule(form)` — maps `schedule_kind`/`time_of_day`/`day_of_week`/`cron` to the API schedule object.
- `parseWritablePaths(raw)` — splits the comma-separated input into a trimmed, empty-free array.

**Current callers:** `pages/jobs.js` (`buildPayload`), `pages/job_detail.js` (`saveJob`).

### `x-modal-close` (Alpine directive)

Centralizes modal dismissal: clicking the backdrop (the `.modal-overlay`/`.modal-backdrop` element itself) or pressing Escape closes the modal. Apply it to the overlay element with the close action as its expression, e.g. `<div class="modal-overlay" x-modal-close="isOpen = false">`. No `@click.stop` on the inner dialog is needed (a backdrop click only fires when the click target *is* the overlay). Add the `.no-backdrop` modifier for confirmations (remove-project, delete-chat) that must not be dismissed by an accidental backdrop click (Escape still works). This replaces the old per-modal mix of `@click.self`, `@click.stop`, `@click.outside`, and `@keydown.escape.window` — which is how some modals silently shipped without backdrop-dismiss.

Implementation notes: handlers are gated on actual visibility via `el.checkVisibility()` — never `offsetParent`, which is always null for the fixed-positioned overlays used here. One Escape press closes exactly one visible modal (`stopImmediatePropagation`), so stacked modals unwind one at a time.

**Current callers:** add-project (`layouts/layout.html`), jobs create/edit (`partials/jobs/*_modal.html`), remove-project (`pages/dashboard.html`), chat delete (`pages/projects/chats.html`), finding details (`pages/projects/findings.html`), prompt editor (`partials/settings/rules_panel.html`).

---

## 22. Host Platform Introspection (`internal/sysinfo/wsl.go`)

### `sysinfo.IsWSL() bool`

Reports whether the process runs under WSL (v1 or v2) by checking `/proc/version`
for the Microsoft kernel marker. Cached via `sync.Once`; always false on
non-Linux GOOS.

**Current callers:** `cliharness.LookPath` (Windows-interop provider warning), `cmd.warnIfWSLInteropWorkspace`, `backgroundjobs` runner (run warning).

### `sysinfo.IsWindowsInteropPath(p string) bool`

True when an absolute POSIX path targets a WSL interop drive mount
(`/mnt/<letter>/...`). Kernel mounts like `/mnt/wsl` are excluded. Use this —
never a raw `strings.HasPrefix(p, "/mnt/")` — so `/mnt/wsl` and friends are not
misclassified.

**Current callers:** same as `IsWSL()`.

---

## 23. Provider Registry Predicates (`internal/analyzer/providers.go`)

### `analyzer.IsRegisteredProvider(id string) bool`

Reports whether a plain-string provider id (e.g. from a CLI flag) names a registered provider. Use this instead of hand-rolling registry lookups when validating user input.

**Current callers:** `cmd/setup.go` (non-interactive `--provider` validation).

### `analyzer.RegisteredProviderIDStrings() []string`

Returns `RegisteredProviders()` as plain strings, sorted, for error messages and flag help text.

**Current callers:** `cmd/setup.go` (unknown-provider error listing).

---

## 24. LLM Call Capture & Replay (`internal/capture/`, `internal/replay/`)

| Symbol | Purpose |
|---|---|
| `capture.RunsRoot(outputRoot, projectName string) string` | Directory holding every captured run for a project (`<root>/<project>/runs`). |
| `capture.RunDir(outputRoot, projectName, runID string) string` | One run's directory. Do not hand-roll this join elsewhere. |
| `capture.Open(dir string, meta RunMeta) (*Writer, error)` | Creates the run dir, writes `meta.json`, opens `calls.jsonl`. |
| `(*capture.Writer).Append(Record) error` | Assigns sequence index, applies `max_field_kb` truncation, appends one JSONL line. Concurrency-safe. |
| `capture.ListRuns(runsRoot string) ([]RunSummary, error)` | Newest-first summaries with derived worst-status. |
| `capture.LoadRun(runsRoot, runID string) (RunMeta, []Record, error)` | Full read of one run; returns sentinel `capture.ErrRunNotFound`. |
| `capture.Prune(runsRoot string, keep int) error` | Removes oldest run dirs beyond `keep` (invalid-meta dirs sort oldest). |
| `capture.NewRunID() (string, error)` | Random 8-hex run ID. Pipeline keeps its own generator; new callers use this one. |
| `analyzer.ParsePhase1Response(raw string, packs []RulePack) (...)` | Exported phase-1 decoder used by replay tooling; same behavior as the live path. |
| `pipeline.BuildRulePacks(cfg *config.App, projectPath string) ([]analyzer.RulePack, error)` | Effective packs (defaults + project packs + toggles + timeout) for CLI/web replay decoding. |
| `replay.Reparse(outputRoot, project, runID string, callIndex int, packs)` | Re-decode a stored response. Pure computation. |
| `replay.Resend(ctx, Options) (Result, error)` | Resend a stored prompt via registry provider with optional overrides; persists a `kind=replay` run. |

**Do not** re-implement status mapping (`ok`/`parse_failed`/`error`) outside `pipeline.callRecorder`, and never build capture paths with `filepath.Join` by hand — use `RunsRoot`/`RunDir` so the layout stays uniform.

**Current callers:** `internal/pipeline/capture.go` (writer wiring), `cmd/runs.go` + `cmd/replay.go` (CLI), `internal/web/handlers/runs.go` (HTTP surface).

---

## 26. URI Decoding & Timestamp Parsing (`internal/chat/paths.go`, `internal/chat/readers/jsonl.go`)

### `chat.decodeFileURI(raw string) (string, bool)`

Strips the `file://` scheme and applies URL-decoding (`url.PathUnescape`) so percent-encoded characters (spaces, colons) round-trip correctly. On Windows, handles leading slashes before drive letters (`/C:/...` -> `C:/...`). Returns `("", false)` if unescaping fails.

**Do not write a local `file://` strip or custom unescape block.** Call this shared helper instead.

**Current callers:** `internal/chat/source_vscode.go` (`decodeVSCodePath`), `internal/chat/source_antigravity.go` (`discoverAntigravitySourcesFromDB`).

---

### `readers.ParseTimestamp(value any) (time.Time, bool)`

Normalizes any-typed timestamp representations (`time.Time`, `json.Number`, `string`, `int64`, `float64`, `[]byte`) to a UTC `time.Time`. Supports RFC3339Nano, RFC3339, SQLite datetime formats with timezone offsets (`2006-01-02 15:04:05.999999999-07:00`, `2006-01-02 15:04:05-07:00`), and epoch formats (nanos, micros, millis, seconds).

**Current callers:** `internal/chat/readers/jsonl.go`, `internal/chat/readers/sqlite_common.go`, `internal/chat/source_antigravity.go`.

---

## Quick Reference Table

| What you need | Use |
|---|---|
| Check a provider id is valid | `analyzer.IsRegisteredProvider` |
| Crash-safe file write | `fsutil.WriteFileAtomic` |
| Expand `~` in a path | `fsutil.ExpandUserHome` |
| Validate + resolve a user-supplied project path | `fsutil.NormalizeRootPath` |
| Check path containment | `fsutil.PathWithinRoot` |
| Decode a file:// URI to path | `chat.decodeFileURI` |
| Flexible timestamp parse | `readers.ParseTimestamp` |
| Validate a job prompt size | `backgroundjobs.MaxPromptSize` |
| Parse `file_access` string | `backgroundjobs.ParseFileAccess` |
| Validate writable paths | `backgroundjobs.ValidateWritablePaths` |
| Validate a job schedule | `backgroundjobs.ValidateSchedule` |
| Compute next run time | `backgroundjobs.NextRun` |
| Generate a job ID | `backgroundjobs.GenerateJobID` |
| Validate a job ID | `backgroundjobs.ValidateJobID` |
| Advisory schedule warnings (timezone/DST) | `backgroundjobs.ScheduleWarnings` |
| Parse a lookback unit/amount | `pipeline.ParseLookbackDuration` |
| Parse a `since` string in a handler | `parseSinceWindow` (handlers package) |
| Count finding lifecycle buckets | `buildLifecycleCounts` (handlers package) |
| Aggregate provider health | `buildProviderHealth` (handlers package) |
| Look up a project by name | `findProjectByName` (handlers package) |
| Load + parse findings | `loadFindingsFor` (handlers package) |
| Build a finding view | `buildFindingView` (handlers package) |
| Add a project to config.yaml | `config.AppendProjectToYAML` |
| Remove a project from config.yaml | `config.RemoveProjectFromYAML` |
| Validate a project name | `config.ValidateProjectName` |
| Check lifetime since | `config.IsLifetimeSince` |
| Get provider default model | `config.DefaultModelFor` |
| Get provider remediation hint | `config.RemediationMessage` |
| Read state.json (with cache) | `deps.StateCache.GetState` |
| Read history.json (with cache) | `deps.StateCache.GetHistory` |
| Compute a chat cache key | `state.ChatCacheKey` |
| Check auto-apply eligibility | `categories.ApplyEligible` |
| Detect WSL / interop paths | `sysinfo.IsWSL` / `sysinfo.IsWindowsInteropPath` |
| Build structured log field | `logging.Any` / `logging.String` |
| Build error log fields | `logging.ErrAttr` |
| Run bounded non-interactive git | `gitutil.Command` |
| Create a tagged provider error | `errs.NotInstalled` / `errs.RateLimit` / `errs.ProviderUnavailable` |
| Warm model list from disk | `(*ModelListCache).Load(dir)` |
| Make a CSRF'd JSON request (frontend) | `window.dreamerAPI` (`csrf`/`postJSON`/`getJSON`/`deleteJSON`) |
| Serialize a job schedule / writable paths (frontend) | `window.dreamerJobs.buildSchedule` / `parseWritablePaths` |
| Close a modal on backdrop click + Escape (frontend) | `x-modal-close` Alpine directive |
| Persist model list to disk | `(*ModelListCache).Save(dir)` |

