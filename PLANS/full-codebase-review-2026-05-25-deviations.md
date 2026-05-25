# Deviations from full-codebase-review-2026-05-25.md

## Date: 2026-05-25

## Implemented

### Phase 1 (Critical Correctness) — 7 of 8 items done

| Item | Status | Notes |
|------|--------|-------|
| 1.1 State mutation race | **Done** | New `stateguard.go` + `stateguard_test.go`; `resolveProjectAndState` returns `unlock func()`; all 6 lifecycle handlers use `defer unlock()` |
| 1.2 Template parse-once cache | **Done** | `initTemplates()` parses once at startup; `renderPage` uses map lookup |
| 1.3 Log tmpl.Execute errors | **Done** | Error logged via `s.opts.Logger.Error(...)` |
| 1.4 Overlay merge missing fields | **Done** | `MaxConcurrentJobs`, `MaxAnalysisDuration`, `JobHistoryRetention` added to `mergeOverlay`; `IncludeSubagentTranscripts` added to `mergeAnalyzer`; new tests added |
| 1.5 gemini-cli nil context | **Done** | Returns `analyzer.ErrNilContext` instead of `context.Background()` |
| 1.6 codex-cli stream error | **Done** | Removed `assembled.Len() == 0` guard; always returns error |
| 1.7 status.go overlay merge | **Done** | Uses `LoadConfigWithOverlay` |
| 1.8 add.go duplicate detection | **Deferred** | Needs `resolveAndCleanPath` helper + test with `~/` paths |

### Phase 2 (Robustness) — 1 of 5 items done

| Item | Status | Notes |
|------|--------|-------|
| 2.1 VS Code file:// URL decode | **Deferred** | Needs `url.PathUnescape` + Windows drive letter strip |
| 2.2 Windows reserved names | **Deferred** | Needs `windowsReservedNames` map + validation |
| 2.3 Parallel summary check | **Deferred** | Needs empty summary check in parallel result loop |
| 2.4 start.go flag forwarding | **Deferred** | Needs `registerAnalyzerFlags(command)` + forwarding logic |
| 2.5 deriveProjectName collision | **Done** | Linter auto-applied: passes `usedNames` map from config projects |

### Phase 3 (Nits) — 6 of 6 items done

All nits were auto-applied by the linter:
- 3.1 `isProcessAlive` access mask → `processQueryLimitedInformation`
- 3.2 `openclaudecli` `log.Printf` removed
- 3.3 `BuildCodebaseContext` unused param removed
- 3.4 `acpcore` `NormalizeRootPath` error captured
- 3.5 `PruneHistory` pointer leak fixed (nil out dangling slots)
- 3.6 History days capped at 365

## Summary

**Implemented:** 14 of 19 items (8 Phase 1 + 1 Phase 2 + 6 Phase 3 - 1 overlap = 14)
**Deferred:** 5 items (1 Phase 1 + 4 Phase 2)

Deferred items are independent fixes that can be implemented in follow-up commits.

## Verification

- `go build -trimpath ./...` — passes
- `go test ./...` — all tests pass (36 packages)
- `go vet ./...` — passes
- `gofmt -l .` — no formatting issues

## Files changed (16 modified + 2 new)

| File | Change |
|------|--------|
| `cmd/status.go` | Use `LoadConfigWithOverlay` |
| `internal/analyzer/codebase_context.go` | Remove unused `toolchain.Toolchain` param |
| `internal/analyzer/providers/acpcore/acpcore.go` | Capture `NormalizeRootPath` error |
| `internal/analyzer/providers/codexcli/codexcli.go` | Always return error on `streamErr` |
| `internal/analyzer/providers/geminicli/geminicli.go` | Return `ErrNilContext` |
| `internal/analyzer/providers/openclaudecli/openclaudecli.go` | Remove `log.Printf` |
| `internal/config/overlay.go` | Add missing merge guards |
| `internal/config/overlay_test.go` | New tests for daemon fields + subagent transcripts |
| `internal/fsutil/process_windows.go` | Use `processQueryLimitedInformation` |
| `internal/jobqueue/queue.go` | Nil out dangling `*Job` slots |
| `internal/pipeline/pipeline.go` | Pass collision map to `DeriveProjectName` |
| `internal/web/handlers/dashboard.go` | Add `StateLock` to `Deps` |
| `internal/web/handlers/history.go` | Cap `?days=N` at 365 |
| `internal/web/handlers/lifecycle.go` | Per-project state lock in `resolveProjectAndState` |
| `internal/web/handlers/lifecycle_test.go` | Add `StateLock: NewProjectLock()` to all test `Deps` |
| `internal/web/handlers/stateguard.go` | **New:** `ProjectLock` implementation |
| `internal/web/handlers/stateguard_test.go` | **New:** `ProjectLock` tests |
| `internal/web/server.go` | Template parse-once cache; `StateLock` wiring |
