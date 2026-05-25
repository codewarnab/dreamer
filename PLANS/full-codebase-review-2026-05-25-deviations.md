# Deviations — Full Codebase Review 2026-05-25

## All 19 items implemented

| # | Item | Commit(s) | Notes |
|---|------|-----------|-------|
| 1.1 | State mutation race | `b4d200e` | New stateguard.go; resolveProjectAndState returns unlock func |
| 1.2 | Template parse-once cache | `b4d200e` | initTemplates at startup; renderPage uses map lookup |
| 1.3 | Log tmpl.Execute errors | `b4d200e` | Logger.Error instead of discarding |
| 1.4 | Overlay merge missing fields | `67364a0` | MaxConcurrentJobs/MaxAnalysisDuration/JobHistoryRetention in mergeOverlay; IncludeSubagentTranscripts in mergeAnalyzer |
| 1.5 | gemini-cli nil context | `bdd5dca` | Return ErrNilContext instead of context.Background() |
| 1.6 | codex-cli stream error | `bdd5dca` | Removed assembled.Len() == 0 guard |
| 1.7 | status.go overlay merge | `67364a0` | Use LoadConfigWithOverlay |
| 1.8 | add.go duplicate detection | `23b4221` | ExpandUserHome + filepath.Abs on both sides of comparison |
| 2.1 | VS Code file:// URL decode | `4028941` | url.PathUnescape + Windows drive-letter strip |
| 2.2 | Windows reserved names | `fe1c908` | isWindowsReservedName: CON/PRN/AUX/NUL/COM1-9/LPT1-9 |
| 2.3 | Parallel summary check | `ae3bd3e` | Empty summary warning in parallel result loop |
| 2.4 | start.go flag forwarding | `6f7c50b` | registerAnalyzerFlags + forward --parallel/--jobs/--chunk-size |
| 2.5 | deriveProjectName collision | `a86fada` | Collision map from config projects (auto-applied by linter) |
| 3.1 | isProcessAlive access mask | `a86fada` | processQueryLimitedInformation (auto-applied by linter) |
| 3.2 | openclaudecli log.Printf | `a86fada` | Removed (auto-applied by linter) |
| 3.3 | BuildCodebaseContext unused param | `a86fada` | Removed toolchain.Toolchain param (auto-applied by linter) |
| 3.4 | acpcore NormalizeRootPath error | `a86fada` | Capture and deny on failure (auto-applied by linter) |
| 3.5 | PruneHistory pointer leak | `a86fada` | Nil out dangling *Job slots (auto-applied by linter) |
| 3.6 | History days cap | `a86fada` | Cap at 365 (auto-applied by linter) |

## Verification

- `go build -trimpath ./...` — pass
- `go test ./...` — all 28 packages pass (no cache)
- `go vet ./...` — pass
- `gofmt -l .` — clean

## Files changed (18 modified + 3 new)

| File | Change |
|------|--------|
| `cmd/add.go` | Expand paths before duplicate comparison |
| `cmd/add_test.go` | New test for tilde-expanded duplicate detection |
| `cmd/start.go` | Register analyzer flags, forward to daemon child |
| `cmd/status.go` | Use LoadConfigWithOverlay |
| `internal/analyzer/codebase_context.go` | Remove unused toolchain.Toolchain param |
| `internal/analyzer/orchestrator_chunked.go` | Empty summary warning in parallel loop |
| `internal/analyzer/providers/acpcore/acpcore.go` | Capture NormalizeRootPath error |
| `internal/analyzer/providers/codexcli/codexcli.go` | Always return error on streamErr |
| `internal/analyzer/providers/geminicli/geminicli.go` | Return ErrNilContext |
| `internal/analyzer/providers/openclaudecli/openclaudecli.go` | Remove log.Printf |
| `internal/chat/source_vscode.go` | decodeVSCodePath with URL unescape |
| `internal/chat/source_vscode_test.go` | **New:** decodeVSCodePath tests |
| `internal/config/loader.go` | isWindowsReservedName in ValidateProjectName |
| `internal/config/loader_test.go` | New tests for reserved names |
| `internal/config/overlay.go` | Merge guards for daemon fields + subagent transcripts |
| `internal/config/overlay_test.go` | New overlay merge tests |
| `internal/fsutil/process_windows.go` | processQueryLimitedInformation |
| `internal/jobqueue/queue.go` | Nil out dangling *Job slots |
| `internal/pipeline/pipeline.go` | Collision map for DeriveProjectName |
| `internal/web/handlers/dashboard.go` | StateLock in Deps |
| `internal/web/handlers/history.go` | Cap ?days at 365 |
| `internal/web/handlers/lifecycle.go` | Per-project state lock in resolveProjectAndState |
| `internal/web/handlers/lifecycle_test.go` | StateLock in all test fixtures |
| `internal/web/handlers/stateguard.go` | **New:** ProjectLock |
| `internal/web/handlers/stateguard_test.go` | **New:** ProjectLock tests |
| `internal/web/server.go` | Template cache; StateLock wiring |
