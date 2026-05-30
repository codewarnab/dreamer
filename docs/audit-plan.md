# Dreamer Bug & Edge Case Audit Plan

## Context

The dreamer codebase (~37,500 lines Go, 208 files, 16+ packages) has had 5 prior audits (documentation, design, CLI flags, naming, provider verification). Known bugs are tracked in `doc/bugs.md`. This plan finds **new** bugs and missed edge cases — research only, no fixes. Each phase writes findings to `findings.md` in the project root.

## Output Format

Each finding in `findings.md`:
```markdown
### [SEVERITY] Title
- **Location:** file.go:line
- **Description:** What is wrong
- **Impact:** What breaks
- **Reproduction:** Steps (if applicable)
```

Severity: CRITICAL (panic/data-loss/security-bypass), HIGH (wrong-result/resource-leak), MEDIUM (non-default-conditions/hygiene), LOW (theoretical/cosmetic).

---

## Phase 1: Concurrency Safety
**Focus:** Mutex-guarded state, sync.Map usage, goroutine lifecycles, data races.
**Can run in parallel with:** Phase 2, Phase 3.

### Files to examine

**Primary: `internal/analyzer/providers/acpcore/transport.go`**
- Unchecked type assertion on sync.Map value at line 223: `ch := chRaw.(chan jsonrpcResponse)` — panics if corrupted
- Goroutine with silent error discard at line 473: `go func() { _ = cmd.Wait() }()`
- WaitGroup lifecycle at lines 489-491 — verify drain on all error paths
- Channel close ordering at lines 288-289 — verify no concurrent Call after markClosed

**Secondary: `cmd/jobs.go`**
- Package-level sync.Map never cleaned up at line 188 — unbounded memory leak
- Unchecked type assertion at line 191: `guard.(*atomic.Bool)` — panics if wrong type

**Secondary: `internal/state/cache.go`**
- TOCTOU between RUnlock and Lock at lines 47-63
- No copy-on-return — callers must treat as read-only (verify no handler mutates)

**Other files:**
- `internal/pipeline/discovery_cache.go` — mtime TOCTOU
- `internal/web/handlers/stateguard.go` — deadlock between pl.mu and entry.mu
- `internal/analyzer/sessionpool.go` — live counter on factory panic

### Greps
- `sync\.Map` — verify every Load/LoadOrStore uses ok-form for type assertion
- `\.Lock\(\)` without `defer` — manual unlock risks leaks on panic
- `go func` — verify error capture and lifecycle management

---

## Phase 2: Error Handling & Silent Swallows
**Focus:** All `_ = err` patterns, silent `continue` on parse errors, missing error returns.
**Can run in parallel with:** Phase 1, Phase 3.

### Files to examine

**Primary: `internal/chat/readers/` — All reader files**
- Missing `rows.Err()` check in `opencode.go` after loop (line 182)
- Silent timestamp zeroing in `opencode.go:95,136,198` and `kiro.go:68`
- Silent JSON parse skips across ALL JSONL readers (count total instances)
- Kiro assistant turns have no timestamp (B9) — check for similar gaps in other readers

**Secondary:**
- `cmd/stop.go:24` — overlay error ignored
- `internal/analyzer/providers/acpcore/transport.go:473` — cmd.Wait() error discarded

### Greps
- `_ = err` or `_ = .*Err` — categorize as cleanup (acceptable) vs meaningful swallow (risky)
- `if err := .*; err != nil { continue }` — silent parse skips
- `scanner\.Err\(\)` — find all scanner usage, verify Err() checked after loop
- `rows\.Err\(\)` — find all rows iteration, verify Err() checked

---

## Phase 3: Sandbox Containment & Security
**Focus:** Sandbox on each platform, permissions, path traversal, SSRF.
**Can run in parallel with:** Phase 1, Phase 2.

### Files to examine

**Primary: `internal/sandbox/`**
- `windows_arm64.go` — Available() returns false, no warning to user
- `linux_bwrap.go` — bwrap command construction, /tmp isolation, writable dir cap
- `darwin_seatbelt.go` — `(allow default)` base policy, verify ProjectDir deny
- `windows.go` + `windows_acl.go` — WRITE_RESTRICTED token, Job Object limits

**Secondary:**
- `internal/analyzer/permission.go` — SSRF via DNS rebinding, empty root allow-by-default, symlink resolution
- `internal/web/handlers/fs.go:22-31` — FSExists uses filepath.Clean but NOT EvalSymlinks (path traversal via symlink)
- `internal/mcpserver/validation.go:216-235` — os.TempDir() can be redirected via TMP/TEMP on Windows

### Greps
- `Available()` — find all callers, verify they handle "unavailable" with warning
- `filepath\.EvalSymlinks` and `fsutil\.ResolveSymlinks` — find all path resolution sites
- `os\.TempDir\(\)` — find all temp dir usage, verify containment

---

## Phase 4: Web Handler Input Validation
**Focus:** HTTP handlers, injection, CSRF bypass, DoS vectors.
**Can run in parallel with:** Phase 1-3.

### Files to examine

**Primary: `internal/web/handlers/`**
- `jobs.go:212-250` — job creation validation, 16 KiB prompt cap, 64 KiB body cap
- `settings.go:91-150` — PUT validation, reject provider fields, mergePartial nil semantics
- `lifecycle.go:46-53` — hash parameter validation (should be hex only)
- `chats.go:36-37` — delete path traversal claim verification
- `fs.go:22-31` — symlink traversal in FSExists
- `events.go:44-48` — SSE event injection via payload

**Secondary:**
- `internal/web/csrf.go:24-55` — Origin: null handling, IPv6 bracket notation

### Greps
- `r\.URL\.Query\(\)\.Get` — find all query parameter reads, verify validation
- `r\.URL\.Path` — find all path parsing, verify no traversal
- `json\.NewDecoder\(r\.Body\)` — find all body parsing, verify size limits
- `http\.MaxBytesReader` — verify all POST/PUT handlers have body limits

---

## Phase 5: Background Jobs State Machine
**Focus:** Job lifecycle, run recording, schedule reconciliation, cross-process safety.
**Can run in parallel with:** Phase 1-4.

### Files to examine

**Primary: `internal/backgroundjobs/`**
- `runner.go:107-199` — execution sequence, timeout detection, lock release on panic
- `store.go:45-73` — cross-process file locking, concurrent Update serialization
- `schedule.go` — parseTimeOfDay error ignore (line 95), impossible cron (366-day search), cross-field validation
- `reconcile.go` — orphan schedules, disabled jobs with active OS schedules
- `health.go` — timeout vs failure accounting

**Secondary:**
- `internal/web/handlers/jobs.go:186-199` — runGuards release on error paths

### Greps
- `RunStatus` — find all status transitions, verify state machine completeness
- `defer release\(\)` — verify all lock acquisitions have matching defers
- `NextRun` — find all callers, verify error handling

---

## Phase 6: Chat Reader Robustness
**Focus:** All readers, discovery probes, malformed input.
**Can run in parallel with:** Phase 1-5.

### Files to examine

**Primary: `internal/chat/readers/`**
- `opencode.go:151-189` — manual rows.Close without defer, missing rows.Err()
- `kiro.go:185-200` — assistant turn timestamp gap (B9)
- `claude_tool_fold.go:132-142` — content field type handling (neither string nor array)

**Secondary:**
- `internal/chat/probe.go:100-136` — missing scanner.Err() check in probeJSONLForCWD
- `internal/chat/probe.go:141-143` — recursiveExtract depth limit verification
- `internal/chat/source_gemini_cli.go` — case-sensitive path filter (B17)

### Greps
- `scanner\.Err\(\)` — find all scanner usage, verify Err() checked
- `rows\.Err\(\)` — find all rows iteration, verify Err() checked
- `json\.Unmarshal.*continue` — find all silent JSON skips
- `rows\.Close\(\)` without `defer` — find manual close patterns

---

## Phase 7: Config Overlay & Merge Semantics
**Focus:** Config loading, overlay merging, defaults, validation.
**Can run in parallel with:** Phase 1-6.

### Files to examine

**Primary: `internal/config/`**
- `overlay.go:77-123` — zero-value guards prevent resetting fields to zero (MaxConcurrentJobs, Port, etc.)
- `overlay.go:105-109` — list replacement vs merge (Projects, Redaction.Patterns replaced entirely)
- `overlay.go:111-119` — map merge semantics (deep vs shallow copy)
- `loader.go:300-339` — MaxChunkBytes=0 escape hatch broken by default (line 355-357 replaces 0 with default)
- `loader.go:324-327` — web.Enabled defaults to true (problematic for headless?)

### Greps
- `!= 0` and `!= ""` in overlay.go — find all zero-value guards
- `MaxChunkBytes` — find all references, verify disable-chunking escape hatch
- `LoadConfigWithOverlay` — find all callers, verify error handling

---

## Phase 8: Pipeline Cache & State Management
**Focus:** Caching correctness, state persistence, finding dedup.
**Can run in parallel with:** Phase 1-7.

### Files to examine

**Primary: `internal/pipeline/`**
- `cache.go:35-65` — computeCacheKeys hash failure handling
- `cache.go:67-89` — cacheUnchanged empty cacheKeys edge case
- `cache.go:142-163` — mergeHashLists MaxFindingHashes=5000 cap (old findings can re-surface)
- `pipeline.go:537,622,830` — provider close errors swallowed

**Secondary:**
- `internal/state/cache.go` — verify Invalidate called after every state.Save
- `internal/pipeline/discovery_cache.go` — verify Update called AFTER state.Save

### Greps
- `Invalidate` — find all callers, verify placement after Save
- `CachedPhase1` — find all references, verify cache key matching
- `MaxFindingHashes` — find all references, verify cap applied consistently

---

## Phase 9: Platform-Specific & Untested Code
**Focus:** Windows code, platform differences, untested packages.
**Can run in parallel with:** Phase 1-8.

### Files to examine

**Primary: `internal/analyzer/providers/cliharness/harness.go`**
- 404 lines, 0 tests — shared lifecycle for 4 CLI providers
- Verify: error handling, sandbox integration, stream parsing, timeout handling

**Secondary:**
- `cmd/proc_windows.go` — process kill edge cases (not-found, access-denied, already-terminated)
- `cmd/startup.go:120-122` — only double-quotes escaped in schtasks (shell metachar injection risk)
- `internal/backgroundjobs/scheduler_windows.go` — cron rejection, schtasks translation
- `internal/fsutil/lock.go` — PID reuse, UWP processes, executable identity
- `internal/analyzer/redaction.go` — env-line over-redact, ReDoS in custom patterns
- `internal/fsutil/atomic.go` — stale temp cleanup glob matching

### Greps
- `runtime\.GOOS` — find all platform-specific paths, verify all platforms handled
- `os\.Executable\(\)` — find all uses, verify error handling
- `regexp\.Compile` — find all user-supplied regex, verify no ReDoS risk

---

## Execution

- All 9 phases are independent — run in parallel or sequentially
- Each phase appends its section to `findings.md`
- After all phases: append summary with counts by severity
- Skip findings already in `doc/bugs.md` unless worse variant
- Each phase takes ~15-30 minutes of agent time

## Verification

After all phases complete:
- `go vet ./...` — catch issues the audit missed
- `go test ./...` — ensure findings don't break existing tests
- Cross-check top 5 CRITICAL/HIGH findings by reading actual code
