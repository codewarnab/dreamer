# Test Coverage Improvement Plan

## Context

The codebase has 35 packages, 97 test files, and 16,973 lines of test code (40% of total). Overall test suite passes cleanly with 0 failures. However, 8 packages sit below 55% coverage, and 4 more are in the 55-70% range. This plan prioritizes easy wins (pure functions, untested error paths) over integration tests that require external processes.

## Sequencing Note

**Execute the concurrency-fixes plan FIRST.** The `detectPort` signature change (`io.Reader` → `io.ReadCloser`) will break Phase 7 tests if done in wrong order. The sandbox reaper timeout (concurrency Phase 4) should land before Phase 2 sandbox tests.

## Current Coverage

| Tier | Package | Coverage | Gap |
|------|---------|----------|-----|
| CRITICAL | `providers/acpcore` | 7% | 93% untested |
| CRITICAL | `sandbox` | 17% | 83% untested |
| CRITICAL | `providers/claudeacp` | 0% | 100% untested |
| CRITICAL | `providers/codexacp` | 0% | 100% untested |
| CRITICAL | `providers/copilotacp` | 0% | 100% untested |
| CRITICAL | `providers/geminiacp` | 0% | 100% untested |
| CRITICAL | `providers/kiroacp` | 0% | 100% untested |
| CRITICAL | `categories` | 0% | 100% untested |
| LOW | `cmd` | 30% | 70% untested |
| LOW | `providers/claudecli` | 39% | 61% untested |
| LOW | `providers/codexcli` | 36% | 64% untested |
| LOW | `providers/copilotsdk` | 39% | 61% untested |
| LOW | `providers/geminicli` | 40% | 60% untested |
| LOW | `providers/openclaudecli` | 37% | 63% untested |
| MEDIUM | `providers/opencodehttp` | 55% | 45% untested |
| MEDIUM | `config` | 56% | 44% untested |
| MEDIUM | `chat` | 61% | 39% untested |
| MEDIUM | `output` | 63% | 37% untested |
| MEDIUM | `chat/readers` | 65% | 35% untested |
| MEDIUM | `analyzer/toolchain` | 66% | 34% untested |

## Target

Bring all packages to 60%+ with a stretch goal of 75%+ for core packages (config, pipeline, output, errs, fsutil, chat).

---

## ⚠️ IMPORTANT: Execute concurrency-fixes plan FIRST

The concurrency plan changes `detectPort` signature (breaks Phase 7 tests), sandbox reaper (affects Phase 2 tests), and CLI provider stderrBuf (Phase 4 should test SafeBuffer). No merge conflicts (different files) but sequencing matters.

---

## Phase 0: ACP Wrappers + categories — Batch Easy Wins (NEW — GAP)

6 ACP wrapper packages and `categories` sit at 0% coverage. They're thin init-registration wrappers delegating to `acpcore.New`.

### Easy wins

- Validate provider registration (`ProviderFor()` returns non-nil)
- Validate default command string
- Validate env injection
- Test `categories` package constants and any parsing logic

### Files
- **NEW** `internal/analyzer/providers/claudeacp/claudeacp_test.go`
- **NEW** `internal/analyzer/providers/codexacp/codexacp_test.go`
- **NEW** `internal/analyzer/providers/copilotacp/copilotacp_test.go`
- **NEW** `internal/analyzer/providers/geminiacp/geminiacp_test.go`
- **NEW** `internal/analyzer/providers/kiroacp/kiroacp_test.go`
- **NEW** `internal/categories/categories_test.go`

### Estimated coverage gain: 0% → 60-80% (thin wrappers)

---

## Phase 1: acpcore — Pure Function Tests + Transport Pipes (7% → ~35%)

The acpcore package is the ACP JSON-RPC transport layer. Most of its 984 lines are transport/session code that requires an external process. But several pure functions are untested.

### Easy wins (no mocking needed)

**`selectPermissionOptionID`** (line 744) — picks the best permission option from a map. Pure logic, no dependencies.

**`translatePermissionRequest`** (line 832) — converts raw JSON map to `analyzer.PermissionRequest`. Pure transformation.

**`extractSessionID`** (line 888) — extracts session ID from JSON-RPC response. Pure JSON parsing.

**`pickAvailableModelID`** (line 909) — selects model from available options with fallback. Pure logic.

**`pickReadOnlyModeID`** (line 940) — picks read-only mode option from response. Pure logic.

**`extractStopReason`** (line 962) — extracts stop reason from JSON response. Pure JSON.

**`copyStringMap`** (line 975) — deep-copies a string map. Trivial.

**`resolveACPConfigDir`** (line 507) — resolves config directory from env vars. Needs env var setup but no external process.

**`acpWritableDirs`** (line 480) — builds writable directory list. Already has 1 test, needs more cases.

**`sessionStream.append` / `sessionStream.text`** (lines 323-336) — concurrent-safe stream buffer. Trivial to test.

**`transport.isClosed` / `transport.markClosed`** (lines 617-628) — already has 1 idempotency test, could add race test.

**`jsonrpcError.Error`** (line 368) — error string formatting. Trivial.

### REQUIRED for 35%+ (pipe-based transport tests — not optional)

**`transport.call`** (line 521) — the JSON-RPC call method. Test with a mock stdin/stdout pair using `io.Pipe`.

**`transport.send`** (line 566) — writes JSON-RPC to stdin. Testable with `io.Pipe`.

**`transport.readLoop`** (line 582) — reads JSON-RPC from stdout. Testable with a pipe feeding canned responses.

**`transport.handlePermissionRequest`** (line 681) — handles permission RPC. Testable by driving the readLoop with a mock.

### Files
- **EDIT** `internal/analyzer/providers/acpcore/acpcore_test.go` — add ~15 test functions (pure + pipe-based)

### Estimated coverage gain: 7% → 35-40% (requires both pure function + pipe-based transport tests)

---

## Phase 2: sandbox — Platform-Gated Tests (17% → ~35%)

The sandbox package is heavily Windows-specific (`windows_job.go`, `windows_token.go`, `windows_sid.go`, `windows_acl.go`). The `windows_arm64.go` and `none.go` stubs return `Available() = false`.

### Easy wins

**`BuildConfig` edge cases** — test with empty project dir, invalid characters, relative paths.

**`PostStartOrKill` with nil cmd** — should return error gracefully.

**`Prepare` with ModeOn when Available()=false** — already tested in `sandbox_unavailable_test.go`, could add more.

**`Config.WritableDirs`** — verify directory list construction for different modes.

### Medium effort

**`createCapabilitySID` / `generateRandomSID`** — Windows-only SID generation. Testable on Windows CI only.

**`writeCapabilitySIDFile`** — file I/O with SID string. Testable with temp files.

**ACL operations** (`setAllowWriteACL`, `acquireWritableACL`, `snapshotDACL`, `restoreDACL`) — all Windows-specific, need Windows CI.

### Files
- **EDIT** `internal/sandbox/sandbox_test.go` — add edge cases for `BuildConfig`, `PostStartOrKill`
- **EDIT** `internal/sandbox/sandbox_windows_test.go` — add SID/ACL tests (Windows-only)

### ⚠️ Coverage warning — recent code churn (GAP T3)

May 25 commit added ~881 lines: `windows_acl.go` (+90), `windows_sid.go` (+76), `windows_job.go` (+18), `windows_token.go` (+24). Admin-gated tests will `t.Skip` in CI. Realistic target: 25-30% on Windows CI, lower elsewhere.

### Estimated coverage gain: 17% → 25-30% (admin-gated tests suppress gains)

---

## Phase 3: cmd — Command-Level Tests (30% → ~45%)

The `cmd` package has 8 test files but many commands are only tested for basic flag validation. The `executeRootCommand` helper (line 246) is the key test infrastructure.

### Easy wins

**`resolveConfigPath`** — test with empty, relative, absolute, and `~` paths.

**`resolveAddPath`** — already has 3 tests, add nonexistent dir and tilde expansion.

**`checkJobConflict`** — pure function, needs test with mock config.

**`commandContext`** — context extraction from cobra command.

**`printBox`** — output formatting, verify stdout content.

**`valueOr` / `intOr`** — trivial helper functions in configtemplate.go.

### Medium effort

**`newAnalyzeCommand` error paths** — test with invalid `--output-dir`, `--since` flag parsing.

**`newDaemonCommand` flag validation** — test `--frequency` with invalid values.

**`newSetupCommand`** — interactive TUI, hard to unit test. Skip.

**`newWebCommand`** — test with missing port file.

**`newMCPServerCommand`** — test flag parsing.

**`startConfigWatcher`** — already has 2 tests. Could add overlay path test.

**`enqueueMissingJobs`** — needs mock queue and config.

### Files
- **EDIT** `cmd/commands_test.go` — add more command-level tests
- **EDIT** `cmd/helpers_test.go` — **NEW** test file for `resolveConfigPath`, `checkJobConflict`, `printBox`
- **EDIT** `cmd/configtemplate_test.go` — add `valueOr`/`intOr` tests
- **EDIT** `cmd/daemon_test.go` — add `enqueueMissingJobs` test

### Estimated coverage gain: 30% → 40-45% (platform-gated code limits gains)

---

## Phase 4: CLI Providers — readStreamJSON and Option Tests (36-40% → ~55%)

All 4 CLI providers (claudecli, codexcli, geminicli, openclaudecli) follow the same pattern: they have `readStreamJSON` tests, `New` option tests, and sandbox flag tests. The gap is in the `Run` method (which requires an external process) and error-path handling.

### Easy wins (same pattern across all 4)

**`readStreamJSON` edge cases** — add tests for:
- Empty input (0 lines)
- Single valid JSON line
- Mixed valid/invalid lines (partial success)
- Very long lines (>64KB)
- Lines with embedded newlines in JSON strings
- Result event with empty text
- Rate limit detection in stderr

**`New` option validation** — test with:
- Empty command (should default)
- Empty model (should use provider default)
- Custom env vars

**`session.buildCommand` / prompt construction** — test:
- System message prepending
- RunID marker injection
- Input byte cap (codex)

### Medium effort

**Error classification** — test `IsRateLimitMessage` integration with provider error paths. Create mock processes that output specific stderr patterns.

**`readStreamJSON` with real JSONL fixtures** — build fixture files with actual Claude/Codex/Gemini output formats and test parsing.

### Files
- **EDIT** `internal/analyzer/providers/claudecli/claudecli_test.go`
- **EDIT** `internal/analyzer/providers/codexcli/codexcli_test.go`
- **EDIT** `internal/analyzer/providers/geminicli/geminicli_test.go`
- **EDIT** `internal/analyzer/providers/openclaudecli/openclaudecli_test.go`

### Estimated coverage gain: 36-40% → 50-55%

---

## Phase 5: copilotsdk — SDK Mock Tests (39% → ~65%)

The copilotsdk provider already has good test infrastructure (`fakeSDKClient`, `fakeSDKSession`). The gap is in the `Run` method and error handling paths.

### Easy wins

**`session.Run` happy path** — the fakeSDKSession returns "ok", but no test actually calls `Run` through the full provider → session chain.

**`session.Run` with SDK error** — test what happens when `SendAndWait` returns an error.

**`session.Run` with timeout** — test context cancellation during `SendAndWait`.

**`provider.Start` with SDK error** — test what happens when the SDK client's `Start` fails.

**`provider.NewSession` with SDK error** — test when `CreateSession` fails.

**Options validation** — test with empty CopilotHome, custom CLIURL, UseLoggedInUser variations.

### Files
- **EDIT** `internal/analyzer/providers/copilotsdk/copilotsdk_test.go`

### Estimated coverage gain: 39% → 60-65%

---

## Phase 6: config — Validation Edge Cases (56% → ~70%)

### Easy wins

**`ExpandUserHome`** — test with `~`, `~user`, empty string, already-absolute path.

**`ProjectConfigPath` / `ProjectRulesPath`** — pure path construction.

**`GlobalConfigPath` / `ConfigDirBase` / `UserConfigRoot`** — test with env var overrides.

**`LoadProjectFileConfig`** — test with missing file, empty file, valid YAML.

**`mergeProviderBlocks`** — test merge semantics: which fields override, which default.

**`ResolveMaxDuration`** — test with project-specific and default durations.

**`validateConfig`** — test more validation paths: non-loopback host, invalid port, empty output_root, relative output_root.

### Medium effort

**`LoadConfigWithOverlay` edge cases** — test with:
- Overlay with empty providers map
- Overlay with nil analyzer rules
- Overlay with redaction patterns
- Both files missing

**`applyDefaults`** — test all default paths for new config fields.

### Files
- **EDIT** `internal/config/loader_test.go`
- **EDIT** `internal/config/overlay_test.go`

### Estimated coverage gain: 56% → 68-72%

---

## Phase 7: opencodehttp — detectPort and Error Paths (55% → ~70%)

### Easy wins

**`detectPort` timeout** — test with a reader that never produces a port line. Verify timeout fires and error is returned.

**`detectPort` with immediate port** — test with a reader that produces a port on first read.

**`detectPort` with partial lines** — test port split across multiple reads.

**Session error paths** — test `Run` when server returns non-200 status.

**Session error paths** — test `Run` when server returns malformed JSON.

**`healthCheck`** — test with unreachable server (already covered), test with unhealthy response.

### Files
- **EDIT** `internal/analyzer/providers/opencodehttp/opencodehttp_test.go`

### Estimated coverage gain: 55% → 65-70%

---

## Phase 8: output — Untested Rendering and Deduplication (63% → ~80%)

The output package has the highest ratio of untested pure logic per line of code. The existing tests cover `MergeTodos` happy paths but leave most internal rendering functions completely untested.

### Easy wins — untested pure functions

**`categoryHeading()`** (line 373) — converts snake_case/hyphenated category to Title Case. Branches: empty/whitespace → "Uncategorized", underscore/hyphen replacement, already-capitalized input.

**`normalizeCategory()`** (line 388) — replaces `_` and `-` with spaces, normalizes whitespace.

**`normalizeText()`** (line 393) — lowercases, trims, collapses whitespace.

**`snippetLanguage()`** (line 299) — maps tool names to markdown language identifiers. 3 branches: known tool name (golangci-lint, eslint, ruff, etc.), file extension suffix (.yaml, .json, .toml), unknown tool (returns "").

**`projectTitle()`** (line 366) — with and without override.

**`renderMistakeLine()`** (line 257) — builds mistake text with optional guardrail annotation. Branches: no guardrail, tool-only, rule-only, tool+rule.

**`renderEvidenceLine()`** (line 323) — formats evidence as backtick-quoted path with optional lines and symbol.

**`renderFinding()`** (line 230) — the full finding renderer. Branches: unverified flag, config snippet present, evidence present, hash comment.

### Medium effort — untested deduplication and merge paths

**`filterNewFindings()`** (line 146) — tested once via integration. Individual branches: finding with empty Mistake (skip), finding with pre-set Hash (use it), case-insensitive hash matching, in-batch duplicate detection.

**`extractExistingFindingHashes()`** (line 136) — tested only indirectly. Untested: empty content, malformed markers (no hash after prefix), mixed-case hex hashes.

**`renderRunSection()`** (line 171) — with/without RunID, multiple categories, empty findings list.

**`renderWarningsSection()`** (line 193) — with/without RunID, empty warnings list, blank warning strings (should be skipped).

**`groupByCategory()`** (line 212) / **`sortedCategoryHeadings()`** (line 221) — deterministic alphabetical ordering.

**`mergeContent()`** (line 340) — branches: existing content with trailing newline vs without, empty existing (new file), multiple sections.

**`renderSnippet()`** (line 280) — language detection for various tools, single-line snippets, trailing newlines.

### Edge case tests for MergeTodos

- Empty findings → no run section
- Nil existing content → fresh file
- Very long finding descriptions → formatting
- Special characters → markdown injection prevention
- Deduplication across categories → same description, different category = different finding
- Warnings with no findings → warnings-only section
- No findings and no warnings → empty merged content
- RunID with warnings

### Files
- **EDIT** `internal/output/generator_test.go`

### Estimated coverage gain: 63% → 75-80%

---

## Phase 9: chat — Utility Functions and Probe Logic (61% → ~75%)

The chat tests are heavily focused on discovery integration. The gap is concentrated in utility functions, the recursive JSON probe logic, and error paths.

### Easy wins — untested pure functions

**`PrependMarker()`** in `marker.go` — two branches: with runID and without. Zero tests.

**`normalizeDiscoveryKey()`** in `probe.go` — lowercases, strips underscores and hyphens. Pure function.

**`valueForNormalizedKey()`** in `probe.go` — iterates a map matching normalized keys.

**`stringValueForNormalizedKey()`** in `probe.go` — adds type assertion + trim check.

**`splitDiscoveryField()`** in `probe.go` — parses `key: value` lines, trims quotes. Used by antigravity text probe.

**`sqliteReaderAvailable()`** in `types.go` — pure boolean logic with three branches (custom open hook, non-default driver, driver registered).

**`SplitSQLiteSourcePath()`** in `types.go` — has one integration test but the "no separator" fallback path is not tested.

**`codebuffProjectMatches()`** in `source_codebuff.go` — case-insensitive basename matching.

**`isJSONLExtension()`** in `probe.go` — case-insensitive extension check.

### Medium effort — untested probe and recursive logic

**`recursiveExtract()` / `recursiveExtractWalker()`** in `probe.go` — the core tree-walking function used by all JSON-based evidence probes. No direct unit test. Branches: map iteration with matching key, recursive descent, array iteration, maxDepth exceeded, nil value.

**`extractPathValue()`** in `probe.go` — handles string, `[]byte`, map (with 8 different wrapper keys), array. Zero direct tests.

**`containsDreamerMarker()`** in `probe.go` — tested only indirectly. Needs direct tests for: marker found within first 10 lines, not found, file open error, lines beyond scan limit.

**`probeJSONLForCWD()`** in `probe.go` — file open error, empty lines, JSON parse errors, maxLines enforcement.

**`walkChatFiles()`** in `probe.go` — `os.Stat` error path (non-`IsNotExist`), root-is-not-a-dir, `walkErr` propagation, `entry.Info()` error.

### Error path tests

**`deleteSourceFile()`** in `deletion.go` — empty-path error, non-`IsNotExist` error, idempotent success (file already gone).

**`statSourceSize()`** in `deletion.go` — empty path returns (0, nil), non-`IsNotExist` stat error.

**`DefaultDiscoveryEnvironment()`** — env var fallback paths (APPDATA empty, XDG_DATA_HOME empty, CLAUDE_CONFIG_DIR set).

**`ProviderFor()`** — the "not found" return path.

### Files
- **EDIT** `internal/chat/discovery_test.go`
- **NEW** `internal/chat/paths_test.go` — `normalizeDiscoveryKey`, `splitDiscoveryField`, `isJSONLExtension`
- **NEW** `internal/chat/probe_test.go` — `recursiveExtract`, `extractPathValue`, `containsDreamerMarker`

### Estimated coverage gain: 61% → 70-75%

---

## Phase 10: toolchain — Untested Methods and Detection Rules (66% → ~80%)

The toolchain package is 100% pure logic — no external tool invocations, no network calls. Every function is testable with temp directories.

### Easy wins — untested methods on Toolchain struct

**`PrimaryLinter()`** (line 174) — iterates `Linters`, returns first non-empty tool name. Needs: populated linters, empty linters, linters with whitespace-only tool.

**`PrimaryTestFramework()`** (line 184) — iterates `TestFrameworks`, returns first non-empty. Same pattern.

**`Summary()`** (line 194) — builds semicolon-joined description. Four branches: languages present, linters present, test frameworks present, "no toolchain detected" fallback. Zero tests.

**`String()`** (line 267) — formats for logging. Trivial.

### Medium effort — untested detection rules

**`deriveJSTestFramework()`** (line 247) — priority cascade: vitest > jest > playwright > mocha > npm test. Only vitest and npm-test fallback are tested. jest, playwright, mocha branches are dead in tests.

**`biome` linter detection** — `biome.json` / `biome.jsonc` are listed as sentinel config files but no test writes them.

**`setup.cfg` as Python sentinel** — `.flake8` path is tested, but `setup.cfg` as a dual sentinel (trigger file AND linter config) is not.

**`golangci.yml`/`.yaml`/`.toml` variant names** — only `.golangci.yml` is tested; `.yaml` and `.toml` variants are untested.

**`appendUnique()`** with whitespace-only value — the `strings.TrimSpace` guard is never hit.

**`findFirst()`** when multiple configs exist — first-match-wins priority not tested.

### Edge cases

- Detect with config files but no source files (e.g., `.eslintrc` without `package.json`)
- Detect with multiple linter configs in same project
- Empty `package.json`
- Root path with trailing whitespace (the `strings.TrimSpace` guard at line 119)

### Files
- **EDIT** `internal/analyzer/toolchain/detect_test.go`

### Estimated coverage gain: 66% → 78-82%

---

## Phase 11: readers — Sanitizers, Timestamps, and Deep Parsing (65% → ~80%)

The readers package has 16 source files and 11 test files. The gap is concentrated in sanitizers without dedicated tests, timestamp parsing branches, and deep JSON/protobuf parsing.

### Easy wins — untested timestamp/role parsing

**`parseTimestamp()`** in `jsonl.go` — has extensive untested branches:
- `json.Number` type (Int64 and Float64 paths)
- String input: unix timestamp string, float string, RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05"
- `float64`, `int64`, `uint64` types including overflow guard (> max int64)
- `[]byte` type (recursive call)

**`unixTimestamp()`** in `jsonl.go` — four dispatch thresholds (nanoseconds, microseconds, milliseconds, seconds). Negative values (absolute value normalization) untested.

**`normalizeRole()`** in `jsonl.go` — "human", "prompt", "model", "ai", "copilot", "bot" variants untested.

**`rawRole()`** in `jsonl.go` — nested map role extraction.

**`roleFromRecord()`** in `jsonl.go` — `author` record with `role`/`type`/`kind` keys.

**`contentFromRecord()`** in `jsonl.go` — fallback keys: "text", "body", "message", "prompt", "response".

**`textFromValue()`** in `jsonl.go` — `[]byte` type, `json.RawMessage` type, `map[string]any` with "parts" key, nested map depth traversal.

### Medium effort — sanitizer dedicated tests

**`copilot_sanitizer.go`** — `SanitizeCopilotSessionMessages()` and `normalizeCopilotContent()` have no dedicated test file. Covered only by one integration test. Need: whitespace normalization, consecutive dedup, nil shouldDrop path.

**`codex_sanitizer.go`** — `SanitizeCodexMessages()`, `normalizeCodexContent()`, `shouldDropCodexMessage()` have no dedicated test file. Need: repo-instruction-bootstrap prefix, codex-runtime-bootstrap tokens.

**`sanitize_common.go`** — `sanitizeMessages()` loop never tested directly. Edge cases: nil `shouldDrop`, empty input, `maxMessages=0` (unlimited), consecutive duplicate dedup, maxMessages enforcement combined with drops.

### Medium effort — tool folding and VS Code

**`parseContentArray()`** in `claude_tool_fold.go` — unknown block types, string elements, non-structured blocks.

**`serializeToolInput()`** in `claude_tool_fold.go` — Read with "path" key, Edit/Write with "path", Grep/Glob patterns, generic tool fallback, truncation at maxToolInputChars.

**`formatToolCall()`** / **`truncateToolOutput()`** — empty input/output, rune boundary handling with multi-byte characters.

**VS Code reader** — `readVSCodeChatJSON()` malformed JSON, delta record parsing, `isNoisyVSCodeChatContent()` prefixes, `applyVSCodeResponseDelta()` `v` vs `value` key.

### Files
- **NEW** `internal/chat/readers/timestamp_test.go` — `parseTimestamp`, `unixTimestamp` all branches
- **EDIT** `internal/chat/readers/jsonl_test.go` — add `normalizeRole`, `contentFromRecord`, `textFromValue` tests
- **NEW** `internal/chat/readers/copilot_sanitizer_test.go` — dedicated sanitizer tests
- **NEW** `internal/chat/readers/codex_sanitizer_test.go` — dedicated sanitizer tests
- **EDIT** `internal/chat/readers/claude_tool_fold_test.go` — add `serializeToolInput`, `formatToolCall` tests
- **EDIT** `internal/chat/readers/vscode_test.go` — add error path and noise filtering tests

### Estimated coverage gain: 65% → 78-82%

---

## Gaps Found (Multi-Agent Review)

Twelve gaps identified during plan review:

### GAP T1: acpcore coverage math corrected (addressed above)

Phase 1 pure functions alone yield ~16-19%, not 35-40%. Pipe-based transport tests are **required**. Fixed inline above.

### GAP T2: 6 packages omitted entirely (MEDIUM)

These packages exist at 0% coverage but are never mentioned in any phase:

| Package | Lines | Notes |
|---------|-------|-------|
| `providers/claudeacp` | 35 | Thin init-registration wrapper → `acpcore.New` |
| `providers/codexacp` | 35 | Same pattern |
| `providers/copilotacp` | 29 | Same pattern |
| `providers/geminiacp` | 31 | Same pattern |
| `providers/kiroacp` | 29 | Same pattern |
| `categories` | 28 | Category constants/normalization |

**Action:** Add a Phase 0 or batch into Phase 1. Each ACP wrapper needs ~2 tests (validate registration, default command/env). `categories` needs constant-validation tests.

### GAP T3: Sandbox underestimates recent churn (MEDIUM)

Commit `f18718f` (May 25) added ~881 new lines across sandbox + CLI providers. New functions like `generateRandomSID`, `writeCapabilitySIDFile`, `acquireWritableACL` require admin privileges. Tests will `t.Skip` with NTSTATUS checks in CI, suppressing coverage gains.

**Action:** Adjust sandbox target from 30-35% to 25-30% (realistic with admin-gated skips), or add a note about Windows CI admin requirements.

### GAP T4: cmd/ 50% target is aggressive (MEDIUM)

Platform-gated code (`proc_unix.go`, `proc_windows.go`, `scheduler.go`, `workers.go`, `start.go`, `stop.go`, `status.go`) + 15 `t.Skip` calls. Without mock process management, 50% requires covering only flag-parsing paths.

**Action:** Lower cmd target to 40-45%, or add process-lifecycle mock infrastructure.

### GAP T5: promptbuilder.go untested (LOW)

`internal/analyzer/promptbuilder.go` — 311 lines, 14 functions, core to analysis pipeline. No dedicated tests. Package is already at 74.6% but this is a critical path.

**Action:** Add promptbuilder test coverage to Phase 1 or as a bonus phase.

### GAP T6: Web subsystem + pipeline/rules.go omitted (LOW)

`internal/web` at 77.6%, `internal/web/apply` at 70.5%. `runner.go`, `server.go`, `sse.go` have no direct tests. `pipeline/rules.go` (120 lines) tested only via integration.

**Action:** Acceptable — both above 60% target. No action needed unless stretch goal is pursued.

### GAP T7: No SafeBuffer concurrent test integration (LOW)

Concurrency plan creates `SafeBuffer` in `transport/syncwriter.go`. Test plan Phase 4 should verify CLI providers work correctly with SafeBuffer under concurrent stderr writes.

**Action:** Add concurrent stderr test to Phase 4's CLI provider tests (requires concurrency plan to land first).

### GAP T8: `internal/analyzer` completely omitted (MEDIUM)

The analyzer package sits at 74.6%, just below the 75% stretch goal. More critically, it's the most actively changed package: 5 files in the top-20 changed since May 12 — `orchestrator_chunked.go` (14 changes), `orchestrator.go` (14), `providers.go` (16), `promptbuilder.go` (13), `rules.go` (12). That's 69 changes with zero planned test work.

**Action:** Add a Phase 12 targeting analyzer orchestrator error paths, promptbuilder edge cases, and rules.go toggle logic. Or fold into Phase 1 acpcore work since they share a package parent.

### GAP T9: `internal/web/handlers` omitted — Windows skips hide gaps (MEDIUM)

Not in the plan. Four tests in `chats_test.go` silently skip on Windows (the dev platform) with `"HOME-based fixture is POSIX-flavored"`. `dashboard.go` and `findings.go` had 25 combined recent changes. The Windows skips mean reported coverage is inflated — those code paths never execute on the dev machine.

**Action:** Add Phase 12 or fold into a web subsystem test pass. At minimum, add non-POSIX fixture tests that run on Windows.

### GAP T10: Windows `t.Skip` masking — systemic blind spot (HIGH)

15+ `t.Skip` calls across 8 test files silently suppress coverage on the dev platform:
- `sandbox_windows_test.go`: 2-3 tests skip without admin
- `web/handlers/chats_test.go`: 4 tests skip (POSIX fixtures)
- `web/apply/apply_test.go`: 1 test skips (symlinks)
- `cmd/startup_test.go`: 2 tests skip (Linux-only)
- `analyzer/permission_test.go`: 2 tests skip (symlink semantics)
- `chat/discovery_test.go`: 2 tests skip (Windows containment)

This means actual exercised coverage on Windows is lower than reported. The Phase 2 sandbox target of "17% to 25-30%" may be unachievable without admin CI because the Windows-specific SID/ACL tests require elevated privileges.

**Action:** Add a `go test -cover` run with `-v` to verify which tests actually execute on Windows. Document which skips are permanent vs. CI-fixable. Consider a CI matrix with admin Windows runner for sandbox tests.

### GAP T11: `internal/web/apply` and `internal/mcpserver` omitted (LOW)

Both below 75% stretch goal: `web/apply` at 70.5%, `mcpserver` at 73.3%. Both have symlink-related skips on Windows.

**Action:** Acceptable for 60% target. Add to stretch goal list if pursuing 75%+ across all packages.

### GAP T12: `internal/pipeline` regression risk (LOW)

`pipeline.go` is the most-changed file (31 changes). Already above 75% (78.8%), so no coverage gap per se, but the plan doesn't flag it for regression test updates despite being the analysis core.

**Action:** Add a note to Phase 8+ verification: after all phases, run `go test -cover ./internal/pipeline/...` and confirm no regression.

---

### Execution Prerequisite

**Concurrency fixes plan must execute first.** The `detectPort` signature change (concurrency Phase 2) will break test plan Phase 7 tests if done in wrong order.

---

## Execution Order and Priority

| Phase | Package | Current | Target | Effort | Priority |
|-------|---------|---------|--------|--------|----------|
| 0 | ACP wrappers + categories (x6) | 0% | 60-80% | Low | **P0** — easy wins |
| 1 | acpcore | 7% | 35-40% | Medium | **P0** — largest gap |
| 2 | sandbox | 17% | 25-30% | Medium | **P0** — admin-gated |
| 3 | cmd | 30% | 40-45% | Medium | **P1** |
| 4 | CLI providers (x4) | 36-40% | 50-55% | Medium | **P1** |
| 5 | copilotsdk | 39% | 60-65% | Low | **P1** — good infra exists |
| 6 | config | 56% | 68-72% | Low | **P2** |
| 7 | opencodehttp | 55% | 65-70% | Low | **P2** |
| 8 | output | 63% | 75-80% | Low | **P2** — highest pure-logic gap |
| 9 | chat | 61% | 70-75% | Medium | **P2** |
| 10 | toolchain | 66% | 78-82% | Low | **P2** |
| 11 | readers | 65% | 78-82% | Medium | **P2** — sanitizer + timestamp gaps |
| 12 | analyzer | 74.6% | 80%+ | Medium | **P2** — most-changed package, stretch goal |
| 13 | web/handlers | 74.1% | 78%+ | Medium | **P2** — Windows skip masking, stretch goal |

## Commit Strategy

One commit per phase:
0. `Add tests for ACP wrappers and categories package`
1. `Add pure function and transport pipe tests for acpcore`
2. `Add sandbox config and edge case tests`
3. `Add cmd helper and command-level tests`
4. `Add readStreamJSON edge case tests for CLI providers`
5. `Add SDK mock tests for copilotsdk provider`
6. `Add config validation edge case tests`
7. `Add detectPort and error path tests for opencodehttp`
8. `Add output deduplication and edge case tests`
9. `Add chat discovery edge case tests`
10. `Add toolchain detection edge case tests`
11. `Add reader sanitizer, timestamp, and tool-fold tests`
12. `Add analyzer orchestrator and promptbuilder tests`
13. `Add web/handler tests with cross-platform fixtures`

## Verification

After each phase:
```bash
go test ./... 2>&1 | tail -40
go test -cover ./<package>/... 2>&1
go vet ./...
```

After all phases:
```bash
go test -cover ./... 2>&1 | grep -E "^ok|^FAIL" | awk '{print $1, $2, $5}'
```

## Files Summary

| Phase | Files | Type |
|-------|-------|------|
| 0 | `claudeacp/claudeacp_test.go`, `codexacp/codexacp_test.go`, `copilotacp/copilotacp_test.go`, `geminiacp/geminiacp_test.go`, `kiroacp/kiroacp_test.go`, `categories/categories_test.go` | NEW |
| 1 | `acpcore/acpcore_test.go` | EDIT |
| 2 | `sandbox/sandbox_test.go`, `sandbox/sandbox_windows_test.go` | EDIT |
| 3 | `cmd/commands_test.go`, `cmd/helpers_test.go` (NEW), `cmd/configtemplate_test.go`, `cmd/daemon_test.go` | EDIT/NEW |
| 4 | `claudecli/claudecli_test.go`, `codexcli/codexcli_test.go`, `geminicli/geminicli_test.go`, `openclaudecli/openclaudecli_test.go` | EDIT |
| 5 | `copilotsdk/copilotsdk_test.go` | EDIT |
| 6 | `config/loader_test.go`, `config/overlay_test.go` | EDIT |
| 7 | `opencodehttp/opencodehttp_test.go` | EDIT |
| 8 | `output/generator_test.go` | EDIT |
| 9 | `chat/discovery_test.go`, `chat/paths_test.go` (NEW), `chat/probe_test.go` (NEW) | EDIT/NEW |
| 10 | `toolchain/detect_test.go` | EDIT |
| 11 | `readers/timestamp_test.go` (NEW), `readers/jsonl_test.go`, `readers/copilot_sanitizer_test.go` (NEW), `readers/codex_sanitizer_test.go` (NEW), `readers/claude_tool_fold_test.go`, `readers/vscode_test.go` | EDIT/NEW |
| 12 | `analyzer/orchestrator_test.go`, `analyzer/promptbuilder_test.go`, `analyzer/rules_test.go` | EDIT |
| 13 | `web/handlers/chats_test.go`, `web/handlers/dashboard_test.go` | EDIT |
