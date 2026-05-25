# Full Codebase Review: Implementation Plan

**Date:** 2026-05-25
**Scope:** 14 verified findings (9 Required, 5 Consider) + 6 Nits
**False positives removed:** 3 (FSExists, SSE event.Type, logging session body)

---

## Phase 1: Critical Correctness (Required — blocks merge if these were a PR)

### 1.1 State mutation race — per-project mutex

**File:** `internal/web/handlers/lifecycle.go`
**Problem:** All 6 lifecycle handlers (Apply, Undo, Dismiss, Resolve, Undismiss, Unresolve) do `state.Load() → mutate → state.Save()` without serialization. Two concurrent requests to the same project clobber each other's mutations.

**Fix:** Add a per-project `sync.Mutex` map keyed by project name. Wrap the entire Load→Mutate→Save cycle in `Lock()/Unlock()`. The mutex lives in the `handlers` package (or a shared `stateguard` sub-package) and is injected via `Deps`.

**Design:**
```go
// internal/web/handlers/stateguard.go

// ProjectLock serializes state Load→Mutate→Save cycles per project
// so concurrent web handlers don't clobber each other.
type ProjectLock struct {
    mu    sync.Mutex
    locks map[string]*sync.Mutex
}

func NewProjectLock() *ProjectLock {
    return &ProjectLock{locks: make(map[string]*sync.Mutex)}
}

// Lock acquires the per-project mutex. Always pair with defer Unlock.
func (pl *ProjectLock) Lock(projectName string) {
    pl.mu.Lock()
    entry, ok := pl.locks[projectName]
    if !ok {
        entry = &sync.Mutex{}
        pl.locks[projectName] = entry
    }
    pl.mu.Unlock()
    entry.Lock()
}

// Unlock releases the per-project mutex. No-op if projectName was never
// Locked (defensive against caller bugs — avoids nil-pointer panic).
func (pl *ProjectLock) Unlock(projectName string) {
    pl.mu.Lock()
    entry := pl.locks[projectName]
    pl.mu.Unlock()
    if entry != nil {
        entry.Unlock()
    }
}
```

Add `StateLock *ProjectLock` to `handlers.Deps`. Each lifecycle handler uses `defer`:
```go
deps.StateLock.Lock(name)
defer deps.StateLock.Unlock(name)
```

**Leak mitigation:** Entries are never removed (project names are stable config). For a long-running daemon this is fine — one `sync.Mutex` per configured project is negligible memory. If projects are removed from config, the orphaned mutex is harmless (never contended).

**Files to change:**
- New: `internal/web/handlers/stateguard.go` + `stateguard_test.go`
- Edit: `internal/web/handlers/deps.go` (add StateLock field)
- Edit: `internal/web/handlers/lifecycle.go` (6 handlers — add `defer` lock/unlock)
- Edit: `internal/web/server.go` (wire StateLock in `attachAPI`)

**Tests:** Concurrent Apply+Dismiss on same project, verify no lost updates. Test that `defer` releases lock even on panic.

---

### 1.2 Template parse-once cache

**File:** `internal/web/server.go`
**Problem:** `renderPage` calls `template.ParseFS(assets, ...)` on every request. The embedded FS never changes.

**Fix:** Parse templates once at startup, cache in a `map[string]*template.Template` on the `Server` struct. Both `renderPage` (page routes) and `renderProjectPage` (project tabs) use the same cache.

**Design:**
```go
// server.go
type Server struct {
    // ... existing fields ...
    templates map[string]*template.Template  // keyed by page template filename
}

func (s *Server) initTemplates() error {
    s.templates = make(map[string]*template.Template)
    // Collect all unique template filenames: page routes + project tabs
    allPages := map[string]bool{}
    for _, route := range pageRoutes {
        allPages[route.template] = true
    }
    for _, tmpl := range projectTabTemplates {
        allPages[tmpl] = true
    }
    for page := range allPages {
        tmpl, err := template.ParseFS(assets, "templates/layout.html", "templates/"+page)
        if err != nil {
            return fmt.Errorf("parse template %q: %w", page, err)
        }
        s.templates[page] = tmpl
    }
    return nil
}
```

`renderPage` and `renderProjectPage` both do `s.templates[pageTemplate]` map lookup instead of `template.ParseFS`.

**Files to change:**
- Edit: `internal/web/server.go`

**Files to change:**
- Edit: `internal/web/server.go`

**Tests:** Existing web tests pass; verify template rendering still works.

---

### 1.3 Log `tmpl.Execute` errors

**File:** `internal/web/server.go:225`
**Problem:** `_ = tmpl.Execute(w, s.layoutData(extra))` discards errors. Truncated HTML with 200 status on failure.

**Fix:** Log the error. Since `tmpl.Execute` may have partially written to `w`, we can't send an HTTP error after it starts. But we can log for debugging.

```go
if err := tmpl.Execute(w, s.layoutData(extra)); err != nil {
    s.opts.Logger.Error("template execute failed", logging.Any("err", err))
}
```

**Files to change:**
- Edit: `internal/web/server.go`

---

### 1.4 Overlay merge — missing fields

**File:** `internal/config/overlay.go`
**Problem:** `mergeAnalyzer` omits `IncludeSubagentTranscripts`. `mergeOverlay` omits `MaxConcurrentJobs`, `MaxAnalysisDuration`, `JobHistoryRetention`.

**Fix:** Add the missing merge guards.

**Bool field caveat:** `IncludeSubagentTranscripts` is a plain `bool` (not `*bool`). The zero-value guard `if overlay.IncludeSubagentTranscripts` means you can overlay `true` but not `false`. This matches the existing pattern for `RuleTimeoutSeconds` (int) and other non-pointer fields — overlay is additive. To overlay to `false`, the user omits the field (base value wins). This is acceptable because the default is `false` — overlaying to `false` is a no-op. If a user needs to override `true→false`, they edit `config.yaml` directly.

```go
// mergeAnalyzer — add:
if overlay.IncludeSubagentTranscripts {
    base.IncludeSubagentTranscripts = overlay.IncludeSubagentTranscripts
}

// mergeOverlay — add inside the daemon block:
if overlay.Daemon.MaxConcurrentJobs != 0 {
    base.Daemon.MaxConcurrentJobs = overlay.Daemon.MaxConcurrentJobs
}
if overlay.Daemon.MaxAnalysisDuration != "" {
    base.Daemon.MaxAnalysisDuration = overlay.Daemon.MaxAnalysisDuration
}
if overlay.Daemon.JobHistoryRetention != "" {
    base.Daemon.JobHistoryRetention = overlay.Daemon.JobHistoryRetention
}
```

**Files to change:**
- Edit: `internal/config/overlay.go`
- Edit: `internal/config/overlay_test.go` (add test cases for each new field)

---

### 1.5 gemini-cli nil context

**File:** `internal/analyzer/providers/geminicli/geminicli.go:133`
**Problem:** Substitutes `context.Background()` instead of returning `ErrNilContext`. Breaks cancellation propagation.

**Fix:**
```go
if ctx == nil {
    return "", analyzer.ErrNilContext
}
```

**Files to change:**
- Edit: `internal/analyzer/providers/geminicli/geminicli.go`
- Add: `internal/analyzer/providers/geminicli/geminicli_test.go` (nil context test)

---

### 1.6 codex-cli stream error swallowing

**File:** `internal/analyzer/providers/codexcli/codexcli.go:245`
**Problem:** If codex emits an error after streaming partial text, the error is discarded and partial text returned as success. Rate limits become invisible.

**Fix:** Always return the error when `streamErr != ""`, regardless of `assembled.Len()`.

```go
if streamErr != "" {
    return "", fmt.Errorf("codex stream error: %s", streamErr)
}
return strings.TrimSpace(assembled.String()), nil
```

**Files to change:**
- Edit: `internal/analyzer/providers/codexcli/codexcli.go`
- Edit: `internal/analyzer/providers/codexcli/codexcli_test.go` (add test for partial text + error)

---

### 1.7 status.go missing overlay merge

**File:** `cmd/status.go:29`
**Problem:** Uses `config.LoadConfig` instead of `config.LoadConfigWithOverlay`. Reads wrong `jobs.json` if overlay overrides `output_root`.

**Fix:**
```go
overlayPath, _ := config.GlobalOverlayPath()
cfg, err := config.LoadConfigWithOverlay(resolvedConfigPath, overlayPath)
```

**Files to change:**
- Edit: `cmd/status.go`

---

### 1.8 add.go duplicate detection

**File:** `cmd/add.go:166`
**Problem:** Compares raw YAML value (`~/dev/foo`) against resolved absolute path (`/home/user/dev/foo`). Duplicates not detected.

**Fix:** Resolve `pathNode.Value` through `config.ExpandUserHome` + `filepath.Abs` + `filepath.Clean` before comparing.

```go
func findDuplicateProject(seq *yaml.Node, name, path string) string {
    for _, item := range seq.Content {
        if item.Kind != yaml.MappingNode {
            continue
        }
        _, nameNode := findMappingChild(item, "name")
        _, pathNode := findMappingChild(item, "path")
        if nameNode != nil && nameNode.Value == name {
            return fmt.Sprintf("name=%q", name)
        }
        if pathNode != nil {
            resolved, err := resolveAndCleanPath(pathNode.Value)
            if err == nil && resolved == path {
                return fmt.Sprintf("path=%q", path)
            }
        }
    }
    return ""
}
```

**Files to change:**
- Edit: `cmd/add.go`
- Edit: `cmd/add_test.go` (test with `~/` paths)

---

## Phase 2: Robustness Improvements (Consider)

### 2.1 VS Code `file://` URL decoding

**File:** `internal/chat/source_vscode.go:125`
**Problem:** `strings.TrimPrefix(path, "file://")` doesn't URL-decode. `file:///c%3A/Users/...` becomes `/c%3A/Users/...` which doesn't match the project path.

**Fix:**
```go
import "net/url"

func readVSCodeWorkspaceEvidence(workspaceJSONPath string) (string, bool) {
    // ... existing code ...
    path := recursiveExtract(document, vscodeWorkspaceEvidenceKeys, vscodeProbeMaxDepth)
    if path == "" {
        return "", false
    }
    path = strings.TrimPrefix(path, "file://")
    // URL-decode percent-encoded characters (e.g. %3A → :).
    if decoded, err := url.PathUnescape(path); err == nil {
        path = decoded
    }
    // Windows: file:///c:/Users/foo → /c:/Users/foo after TrimPrefix.
    // Strip leading slash when followed by a drive letter.
    if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
        path = path[1:] // /c: → c:
    }
    return path, true
}
```

**Why the extra slash strip?** On Windows, `file:///c:/Users/foo` strips `file://` to `/c:/Users/foo`. `filepath.Clean` + `filepath.FromSlash` produces `\c:\Users\foo`, which `filepath.IsAbs` rejects (no drive letter prefix). The downstream `normalizeDiscoveryEvidencePath` then fails. Stripping `/c:` → `c:` fixes this.

**Files to change:**
- Edit: `internal/chat/source_vscode.go`
- Edit: `internal/chat/source_vscode_test.go` (test with percent-encoded URIs AND Windows drive letter paths)

**Files to change:**
- Edit: `internal/chat/source_vscode.go`
- Edit: `internal/chat/source_vscode_test.go` (test with percent-encoded URIs)

---

### 2.2 ValidateProjectName — Windows reserved names

**File:** `internal/config/loader.go:552`
**Problem:** `CON`, `PRN`, `AUX`, `NUL`, `COM1`-`COM9`, `LPT1`-`LPT9` pass validation. `os.MkdirAll` on these names writes to the device.

**Fix:**
```go
var windowsReservedNames = map[string]bool{
    "CON": true, "PRN": true, "AUX": true, "NUL": true,
    "COM1": true, "COM2": true, "COM3": true, "COM4": true,
    "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
    "LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
    "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

func ValidateProjectName(projectName string) error {
    // ... existing checks ...
    upper := strings.ToUpper(name)
    base := strings.SplitN(upper, ".", 2)[0] // "CON.txt" is also reserved
    if windowsReservedNames[base] {
        return fmt.Errorf("project name %q is a Windows reserved device name", projectName)
    }
    return nil
}
```

**Files to change:**
- Edit: `internal/config/loader.go`
- Edit: `internal/config/loader_test.go` (test reserved names)

---

### 2.3 Sequential vs parallel summary check

**File:** `internal/analyzer/orchestrator_chunked.go:168-178`
**Problem:** Empty summary causes hard abort in sequential mode but silent data loss in parallel mode.

**Fix:** Add the same check to the parallel result-processing loop. Match the sequential behavior: treat missing summary as a schema violation and skip the chunk's mistakes (but don't hard-abort — parallel mode already handles per-chunk failures gracefully).

**Why not hard-abort like sequential?** In parallel mode, other chunks may have already completed successfully. Aborting discards their valid results.

**Why still merge mistakes?** In parallel mode, `priorSummary` is always `""` (line 145: `builder.BuildPhase1(chunk, req, "", ...)`), so summaries are unused for chaining. An empty summary with valid mistakes is unusual but not a schema violation that invalidates the findings. Warn but still merge — the mistakes themselves may be valid even if the model omitted the summary field.

```go
// In the parallel results loop, after the parseErr check (line 176):
if chunkRes.summary == "" && chunkRes.index < len(chunkInputs.Chunks)-1 {
    warnings = append(warnings, fmt.Sprintf("schema violation chunk=%d (missing summary); findings merged but summary absent", chunkRes.index))
}
```

**Files to change:**
- Edit: `internal/analyzer/orchestrator_chunked.go`
- Edit: `internal/analyzer/orchestrator_chunked_test.go` (test: parallel chunk with empty summary is skipped with warning)

---

### 2.4 start.go flag forwarding

**File:** `cmd/start.go:59`
**Problem:** Users cannot pass `--parallel`, `--jobs`, `--chunk-size` to `dreamer start`. These flags are only registered on `analyze` and `daemon` commands (via `registerAnalyzerFlags`). Cobra rejects unknown flags, so `dreamer start --parallel` errors — it's not "silently ignored", it's impossible.

**Fix:** Register analyzer flags on the `start` command, then forward them to the daemon child process.

```go
func newStartCommand() *cobra.Command {
    // ... existing var block ...
    command := &cobra.Command{
        // ... existing fields ...
        RunE: func(cmd *cobra.Command, args []string) error {
            // ... existing code up to daemonArgs ...
            daemonArgs := []string{"daemon", "--config", resolvedConfigPath}
            if parallel, _ := cmd.Flags().GetBool("parallel"); parallel {
                daemonArgs = append(daemonArgs, "--parallel")
            }
            if jobs, _ := cmd.Flags().GetInt("jobs"); jobs > 0 {
                daemonArgs = append(daemonArgs, "--jobs", strconv.Itoa(jobs))
            }
            if chunkSize, _ := cmd.Flags().GetInt("chunk-size"); chunkSize > 0 {
                daemonArgs = append(daemonArgs, "--chunk-size", strconv.Itoa(chunkSize))
            }
            daemonProcess := exec.Command(exePath, daemonArgs...)
            // ... rest of existing code ...
        },
    }
    registerAnalyzerFlags(command)  // register --parallel, --jobs, --chunk-size
    return command
}
```

**Files to change:**
- Edit: `cmd/start.go`

---

### 2.5 deriveProjectName collision map

**File:** `internal/pipeline/projectname.go:41`
**Problem:** `dreamer analyze` with two same-basename projects shares state/todos directory.

**Fix:** Pass the config's project list as the collision map in the pipeline's `runDiscovery` path, or document the limitation.

Preferred: pass the collision map.

**Files to change:**
- Edit: `internal/pipeline/pipeline.go` (build `usedNames` map from config projects)

---

## Phase 3: Nits (low priority, can batch)

### 3.1 `isProcessAlive` access mask
**File:** `internal/fsutil/process_windows.go:25`
**Fix:** Change `processQueryInformation` to `processQueryLimitedInformation`.

### 3.2 `openclaudecli` log.Printf
**File:** `internal/analyzer/providers/openclaudecli/openclaudecli.go:212`
**Fix:** Remove the `log.Printf` call. Match codexcli/geminicli behavior (silently skip malformed lines). Remove the `import "log"`.

### 3.3 `BuildCodebaseContext` unused param
**File:** `internal/analyzer/codebase_context.go:17`
**Fix:** Remove the unused `toolchain.Toolchain` parameter. Update callers in `internal/pipeline/`.

### 3.4 `acpcore` NormalizeRootPath error
**File:** `internal/analyzer/providers/acpcore/acpcore.go:142`
**Fix:** Capture the error and include it in the denial reason.

### 3.5 `PruneHistory` pointer leak
**File:** `internal/jobqueue/queue.go:250`
**Fix:** Nil out dangling slots *between* the filter loop and the `q.jobs = filtered` reassignment. The nil-out must happen while `q.jobs` still has the original length:

```go
filtered := q.jobs[:0]
for _, j := range q.jobs {
    if keep(j) {
        filtered = append(filtered, j)
    }
}
// Nil out dangling slots to release *Job pointers for GC.
for i := len(filtered); i < len(q.jobs); i++ {
    q.jobs[i] = nil
}
q.jobs = filtered
```

### 3.6 History days cap
**File:** `internal/web/handlers/history.go:52`
**Fix:** Cap `days` at 365.

---

## Execution Order

1. **Phase 1** (8 items) — each is an independent commit. Do them in order:
   - 1.1 state race → 1.2 template cache → 1.3 execute error → 1.4 overlay → 1.5 gemini nil ctx → 1.6 codex errors → 1.7 status overlay → 1.8 add duplicate

2. **Phase 2** (5 items) — independent commits after Phase 1.

3. **Phase 3** (6 items) — can batch into 1-2 commits.

---

## Adversarial Review of This Plan

**Q: Is the per-project mutex the right granularity?**
A: Yes. A global mutex would serialize across unrelated projects. Per-hash would be too fine-grained (state.json is per-project, not per-finding). Per-project matches the file-level persistence boundary.

**Q: Should the mutex be in `state/` instead of `handlers/`?**
A: No. `state.Load`/`state.Save` are intentionally stateless (no package-level state). The serialization concern belongs to the caller (web handlers). Putting it in `state/` would couple the persistence layer to the web layer.

**Q: Is the template cache safe for concurrent reads?**
A: Yes. `template.Template` is safe for concurrent `Execute` calls after parsing. The map is written once at startup and only read afterward.

**Q: Does the codex error fix change existing behavior?**
A: Yes — it's stricter. Previously, partial text + error = success. Now it = error. This means runs that previously "succeeded" with truncated output will now fail. This is correct behavior: truncated analysis is worse than a clear error, because it silently misses findings.

**Q: Is the `add.go` fix backward-compatible?**
A: Yes. It only affects duplicate detection. Existing config files are unchanged. The fix just resolves both sides of the comparison to absolute paths before comparing.

**Q: Should Phase 2.5 (collision map) be Phase 1?**
A: No. `dreamer analyze` is a one-shot debug command. The daemon (primary workflow) already uses config-defined names. The collision risk is real but low-impact.

**Q: Are there any missing findings?**
A: The agents flagged the `DefaultModel` field in `mergeProviderBlock` as missing, but on inspection `DefaultModel` is not a user-configurable field — it's derived from the provider's hardcoded default. Not a finding.

### Adversarial Review Gaps (addressed)

| Gap | Item | Issue | Resolution |
|-----|------|-------|------------|
| 1 | 1.1 | Mutex map leak — entries never removed | Harmless: one `sync.Mutex` per configured project is negligible. Orphaned entries (after config removal) are never contended. |
| 2 | 1.1 | `defer` not emphasized in design | Fixed: design now shows explicit `defer deps.StateLock.Unlock(name)` pattern. |
| 3 | 1.2 | `renderProjectPage` not covered by cache | Fixed: cache now includes all project tab templates. Both `renderPage` and `renderProjectPage` use map lookup. |
| 4 | 2.3 | Parallel fix uses `continue` but sequential uses hard abort | Fixed with justification: parallel mode already handles per-chunk failures gracefully; hard-abort discards valid results from other chunks. `continue` + warning matches existing `parseErr` handling. |
| 5 | 2.4 | Flags not registered on `start` command | Fixed: plan now includes `registerAnalyzerFlags(command)` step and `strconv.Itoa` for int flags. |
| 6 | 1.1 | Unlock on missing key panics | Fixed: Unlock is now nil-safe (no-op on missing key). |
| 7 | 1.4 | `bool` zero-value guard can't overlay to `false` | Accepted: overlay is additive by design. Default is `false`, so overlaying `false` is a no-op. Documented in plan. |
| 8 | 2.1 | Leading `/` before drive letter on Windows | Fixed: strip `/c:` → `c:` after URL-decode. |
| 9 | 2.3 | Parallel summary check drops valid findings | Fixed: warn but still merge. Summaries unused in parallel mode; findings may be valid. |
| 10 | 3.5 | Nil-out ordering — must happen before reassignment | Fixed: plan now shows precise ordering with code example. |

---

## Naming Conventions (dreamer-design)

All new identifiers follow these rules:
- **Intent-revealing:** `ProjectLock` not `pLock`, `resolveAndCleanPath` not `fixPath`
- **No cryptic abbreviations:** `stateguard` not `sg`, `templateCache` not `tmplCache`
- **Go idioms:** `mu sync.Mutex`, `err error`, `cfg config.Config` are fine
- **No shadowing:** Avoid `fs`, `cap`, `in` as variable names
- **Constants over magic numbers:** `maxHistoryDays = 365`, `defaultSymbolCap = 800`
