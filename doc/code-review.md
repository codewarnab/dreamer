# dreamer — code structure, hygiene, and best-practices review

Companion to [bugs.md](bugs.md). That file lists defects; this one critiques shape. Six sub-agent audits plus first-hand verification of the load-bearing files. The codebase is genuinely well-structured for a CLI of this size — the issues below are mostly polish, not rot.

---

## 1. Overall verdict

**Strengths**

- Clear top-down layering: `cmd/` is a thin Cobra shell, `internal/pipeline/` is the orchestration core, every other `internal/` package is single-purpose and exports a narrow surface.
- Good separation between discovery (`internal/chat/`), decoding (`internal/chat/readers/`), and the analyzer; no reader peeks into orchestration state.
- Tests sit next to code, use `t.TempDir()`, and (per [CLAUDE.md](CLAUDE.md)) build real fixtures rather than mocking — that convention is honored almost everywhere.
- Errors are wrapped with operator-friendly context (`fmt.Errorf("verb noun %q: %w", …)`).
- State persistence is atomic (temp + rename in [internal/state/tracker.go:132-139](internal/state/tracker.go#L132-L139)). The output writer is not — see §3.
- Permission decisions go through a single chokepoint ([internal/analyzer/permission.go](internal/analyzer/permission.go)) instead of being scattered across providers.

**Weaknesses (developed below)**

1. The chat-source layer has visible duplication and a registration pattern that papers over a missing interface (§2).
2. Several "atomicity is documented" claims are half-true — `state.json` is atomic, `todos.md` is not (§3).
3. The provider/permission boundary is well-conceived but trusts the SDK in ways that should be defense-in-depth (§4).
4. `CLAUDE.md`, `plan.md`, and `todo.txt` have drifted from the code (§5).
5. Test coverage is uneven — some subsystems are well-tested, several have no tests at all (§6).
6. A handful of small ergonomic issues: silent error swallowing, unbounded growth, sentinel-vs-default collisions (§7).

---

## 2. Module shape and boundaries

### What works

- The `internal/pipeline/` split is right. `pipeline.Run` is the one entry that ties discovery → cache → redaction → analyzer → output → state, and both `cmd/analyze.go` and `cmd/daemon.go` go through it.
- `internal/chat/discovery.go` is a thin orchestrator over per-source providers, which is the correct shape for a fan-out-and-merge step.
- `internal/analyzer/permission.go` is a pure function over a `PermissionRequest`. Easy to test, easy to reason about.

### What chafes

**Source providers self-register via `init()` into a global slice.**
[internal/chat/provider.go:15-20](internal/chat/provider.go#L15-L20)

```go
var registeredProviders []ChatSourceProvider
func registerProvider(p ChatSourceProvider) { registeredProviders = append(registeredProviders, p) }
```

This works today because `init()` is serial, but it has the usual `init()` costs: order is undefined, providers cannot be excluded at build time without `_test`-tag tricks, and the audit could not find a test that verifies every provider is actually registered. Prefer an explicit `func DefaultProviders() []ChatSourceProvider` constructor called once from `discovery.go` — that gives you compile-time enumeration, test injection, and an obvious place to add a new provider.

**Source files duplicate path-scoping boilerplate.**
The eight `source_*.go` files all call `normalizeDiscoveryPathForComparison` + `pathWithinNormalizedRoot` themselves. The `ChatSourceProvider` interface doesn't enforce it. A new provider that forgets the call will leak unscoped sources. Either:

- Hoist scoping into the discovery orchestrator and pass the *normalized root* into each provider, or
- Add a helper `ScopedDiscover(env DiscoveryEnvironment, root string, walkFn …) []Source` that every provider must use.

**Three near-identical sanitizers** ([claude_sanitizer.go](internal/chat/readers/claude_sanitizer.go), [codex_sanitizer.go](internal/chat/readers/codex_sanitizer.go), antigravity equivalent) all dedupe + truncate. Extract a shared `SanitizeMessages(messages, opts)` taking provider-specific drop rules as a parameter. Each call site shrinks to one line.

**Sanitization ownership claim is no longer true.**
[CLAUDE.md:43-46](CLAUDE.md#L43-L46) says "`buildRedactedTranscript` stays provider-agnostic" because per-source sanitization lives in each provider's `ReadMessages`. Verify: the dispatch is in [internal/pipeline/transcript.go](internal/pipeline/transcript.go) and yes, redaction is post-sanitization. Good. But the dispatch helper is unexported (`readMessagesFromSource`) — `CLAUDE.md` describes it as if exported. Tighten the wording.

**`cmd/helpers.go` re-implements home expansion.**
[cmd/helpers.go:55](cmd/helpers.go#L55) vs [internal/config/loader.go:320](internal/config/loader.go#L320). One is `expandHomePath`, one is `ExpandUserHome`. Either delete the cmd-layer copy and re-export the config one, or move it into a shared `internal/paths` package. Drift between them is silent.

---

## 3. Durability and atomicity claims

`internal/state/tracker.go` does the atomic-write dance correctly (temp file → `os.Rename`). It is missing parent-directory `fsync` after rename, which is the canonical durability completion, but that's a low-grade omission.

`internal/output/generator.go:67` does **not** match the state writer's pattern. It calls `os.WriteFile` (truncate + write), which is not atomic on Linux either — a `SIGKILL` between truncate and final write leaves a half-written `todos.md` that the next merge pass reads. The two writers should share a `WriteFileAtomic(path, data, perm)` helper.

Neither writer takes a process-level lock. Two `dreamer analyze --project /x` invocations against the same project root will race. The daemon is single-process and serial across projects, but the binary doesn't prevent concurrent invocations. A `flock` (or `golang.org/x/sys/unix.Flock` on POSIX, `LockFileEx` on Windows) on `<outputRoot>/<project>/.lock` would close that hole.

---

## 4. Provider and permission boundary

The `analyzer.DecidePermission` chokepoint is the right architecture. Everything funnels through one switch on `PermissionKind`. Issues are about depth-of-defense, not shape:

- **Shell read-only-ness is a single-bit trust** in `shellRequestReadOnly` ([permission.go:65](internal/analyzer/permission.go#L65)). The Copilot SDK supplies `HasWriteFileRedirection`; if it ever ships with a parser gap (heredoc, `tee`, process substitution, `bash -c 'echo X > file'`), there is no second check. A small denylist regex on the command string would cost nothing and catch the realistic-bypass cases.
- **Filesystem allow-by-default when `normalizedRoot == ""`.** [permission.go:38](internal/analyzer/permission.go#L38) returns `Approved: true` if no root is set. The pipeline always sets one, but a future caller that forgets to populate `WorkingDirectory` (e.g. a one-off CLI utility) would unwittingly get a wide-open analyzer.
- **MCP/CustomTool decisions trust `req.ReadOnly`.** Same single-bit story.
- **ACP-core handlers silently approve on JSON parse failure** (per audit). That belongs in §B15 of `bugs.md`; flagged here because the *pattern* of "treat malformed input as benign" recurs.

The redaction system is built around a list of regexes (`RedactionConfig.Patterns`). Good. But the redaction pass runs *after* the source-level sanitizers and *before* chunking — if a sanitizer accidentally re-introduces content (rare but possible), the redactor catches it. Belt and braces; document that ordering invariant in `internal/pipeline/transcript.go`.

---

## 5. Documentation drift

- **Fixed (2026-05-26):** CLAUDE.md now correctly references `internal/pipeline/pipeline.go:Run` as the analysis core. `plan.md` and `todo.txt` have been deleted. Bug entries B13 and B23 are resolved.
- **Remaining:** CLAUDE.md still frames the analyzer around Copilot SDK when it now supports 13+ providers. Several internal packages (`jobqueue`, `sandbox`, `mcpserver`, `errs`, `fsutil`, `categories`) are undocumented. Commands `start`, `stop`, `status`, `mcp-server`, `record-finding` are undocumented. See CLAUDE.md for the authoritative architecture description.

---

## 6. Test coverage

### Well-tested

- `internal/state/tracker.go` — Save/Load round-trip, atomic-rename, normalization. Good.
- `internal/output/generator.go` — merge logic, dedupe via hash comments, evidence rendering. Good.
- `internal/chat/readers/` — JSONL, sanitizers, protobuf, sqlite-with-fixture-driver. The fixture-driver pattern keeps tests fast.
- `internal/analyzer/permission.go` — path traversal and symlink-escape cases exist in [permission_test.go](internal/analyzer/permission_test.go). Worth adding heredoc and `tee`-style shell cases per §4.
- `internal/pipeline/` — `cache_test.go`, `discovery_cache_test.go`, `chunker_test.go`, `projectname_test.go`, `lookback_test.go` all exist; the pipeline `acceptance_test.go` covers the end-to-end happy path.

### Coverage gaps the audits surfaced

- **`cmd/daemon.go`** — no test. Signal handling, ticker lifecycle, cycle-error aggregation are all untested. The package has `commands_test.go` but it only checks the "no projects configured" early-exit.
- **`cmd/analyze.go`** — no direct test. Flag resolution and `--output-dir` override logic are exercised only indirectly.
- **`cmd/ls_chats.go`** — no test.
- **`internal/analyzer/orchestrator.go`** — phase 1 and phase 2 loops have no unit test of their own. The chunked orchestrator has [orchestrator_chunked_test.go](internal/analyzer/orchestrator_chunked_test.go); the non-chunked path does not.
- **`internal/analyzer/redaction.go`** — not tested. Regex compilation errors, overlapping patterns, multi-byte boundaries — all untested.
- **`internal/chat/readers/sqlite.go` real-driver edge cases** — the existing tests use a `fixtureDriver` (synthetic), which is great for speed but misses corrupt-DB, locked-DB, and schema-drift behaviour. One opt-in test that exercises real `modernc.org/sqlite` would close the gap.
- **`--force` end-to-end** — no test verifies that `--force` bypasses both the discovery cache and the chat-hash cache (per the pipeline audit).
- **Hash-failure path** — no test exercises the "some sources fail to hash, others succeed" branch in [pipeline.go:213-231](internal/pipeline/pipeline.go#L213-L231). This is precisely the branch that triggers bug B1 in [bugs.md](bugs.md).

### Test smells

- **`cmd/startup_test.go:34-37`** — `runStartupCommand` is a mutable global swapped by `withStartupCommandRunner`. Safe today (Windows-only test path) but a race waiting to happen with `t.Parallel()`.
- **`cmd/commands_test.go`** — uses `setTestHome` to flip `HOME`/`USERPROFILE` via `t.Setenv`. Fine, but no assertion verifies the override actually reached the config resolver.
- **`internal/chat/readers/sqlite_test.go:196`** — writes a fixture to `t.TempDir()` and never reads it; the mocked driver ignores the path. Dead I/O.

### Test that should exist but doesn't

A regression test for B1: build state with `ChatHashes = {a: keyA, b: keyB}`, make `b` unhashable mid-run, verify `ChatHashes` after `Save` still contains `a` *and* preserves `b`'s prior key (or at least drops only `b`).

---

## 7. Smaller hygiene items

- **Silent `json.Unmarshal` swallows** appear in [probe.go](internal/chat/probe.go), [opencode.go:170](internal/chat/readers/opencode.go#L170), [opencode.go:213](internal/chat/readers/opencode.go#L213), Kiro reader, ACP core. Pattern: parse failure becomes "skip". A debug-level log line at each site would make this diagnosable without changing behaviour.
- **`parseTimestamp(updated)` ignored errors** at [opencode.go:69](internal/chat/readers/opencode.go#L69), [kiro.go:68](internal/chat/readers/kiro.go#L68), sqlite reader. Same shape: error becomes zero time. Fall back to file mtime where available; log otherwise.
- **`internal/errs/errs.go`** is well-structured (kind, details, unwrap) but under-used. `internal/config/loader.go` uses it; most other packages use bare `fmt.Errorf`. Either commit to typed errors across the codebase or delete the package.
- **`*bool` discrimination** is used in `ProviderBlock.UseLoggedInUser`/`AutoStart` to distinguish "unset" from "explicit false". Good. Apply the same pattern to `ProviderBoundaryHeadroom` to fix B5.
- **`Logger.New` writes to stderr always.** Add a `Quiet` option for daemon-from-systemd scenarios.
- **`go 1.25.0`** in `go.mod`. As of audit time, 1.26 is out and the patch level is two behind. Non-breaking bump.
- **`go.sum` has unused indirect noise** (OpenTelemetry pulled by `github/copilot-sdk/go`, never called). Cannot remove without forking; live with it.
- **`go vet ./...` and `gofmt -w .` are not wired into CI.** `CLAUDE.md` says "use directly" — fine for now, but `golangci-lint run` against the repo would catch several `_ =` error swallows on first run. Add a `make lint` once the bugs in `doc/bugs.md` are addressed.

---

## 8. Priority ranking

If you can only address a handful before the next release, in order:

1. **B1** (ChatHashes loss) — silent data-integrity bug in the happy path.
2. **§2 duplication** (sanitizers, path-scoping) — pay off once, simplify every future provider.
3. **§6 test gaps** — at minimum, write the regression test for B1.
4. Doc drift is resolved: CLAUDE.md now references `internal/pipeline/`, and `plan.md`/`todo.txt` have been deleted.

Everything else in this file is incremental.
