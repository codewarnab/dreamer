# dreamer — bugs

Generated 2026-05-18. Findings come from a six-agent parallel audit plus first-hand verification of the most load-bearing files. Each entry: severity, location, observed behaviour, why it matters. Items the audit raised but verification disproved are listed at the end under "Rejected claims".

Severity legend: **high** = data loss, security, or silent wrong answers in normal operation. **med** = wrong answer under realistic but non-default conditions, or unbounded resource growth. **low** = robustness gap that only fires under pathological input or a future change.

---

## High

### B1. `state.ChatHashes` is clobbered when a chat file fails to hash
[internal/pipeline/pipeline.go:429](internal/pipeline/pipeline.go#L429) (also [:274](internal/pipeline/pipeline.go#L274))

The loop at [pipeline.go:213-231](internal/pipeline/pipeline.go#L213-L231) skips a source on hash error (`continue` at line 218) without adding its prior cache key to `cacheKeys`. Line 429 then does `currentState.ChatHashes = cacheKeys`, replacing the entire map. Every previously-cached entry for a temporarily-unhashable file is permanently lost — on the next run the file is treated as "fresh" and re-analyzed even if its content has not changed. This is partly self-healing (`cacheUnchanged` will see the shortened key set and re-run) but the prior `existing == key` evidence is gone.

Fix: union prior `currentState.ChatHashes` into `cacheKeys` for paths that failed to hash this run before assigning, or only assign per-key.

### B2. Read-only shell enforcement trusts the upstream classifier
[internal/analyzer/permission.go:65-81](internal/analyzer/permission.go#L65-L81)

`shellRequestReadOnly` only consults `req.HasWriteFileRedirection` and `req.Commands[i].ReadOnly` — both populated by the provider SDK. The Copilot SDK's redirection detector handles `>` / `>>` but does not parse heredocs (`<<EOF … EOF`), process substitution (`>(cmd)`), `tee`, piped writers (`cmd | dd of=…`), or `bash -c 'inline write'`. A prompt-injected chat transcript can plausibly nudge the analyzer into running a write disguised as a read. The decision layer has zero independent syntax check; if the SDK ever miscategorises, the sandbox is open.

Fix: add a second-stage regex/grammar pass on the command string itself (deny on common write idioms even when SDK says "read-only"), or restrict shell to a known allow-list rather than allow-by-default.

### B3. `todos.md` writes are non-atomic and unlocked
[internal/output/generator.go:67](internal/output/generator.go#L67)

`os.WriteFile` truncates then writes. A crash mid-write leaves a half-written `todos.md` — a file the daemon then reads back and merges against on the next cycle, propagating corruption into `extractExistingFindingHashes`. Compare with `state.Save` at [internal/state/tracker.go:132-139](internal/state/tracker.go#L132-L139), which does temp-file + `os.Rename` correctly. The daemon also runs projects sequentially but the discovery cache is shared across cycles; if a second `dreamer analyze` invocation is launched while the daemon is mid-cycle (entirely plausible — same binary, same output root), two writers can interleave.

Fix: write to `todos.md.tmp` + `os.Rename`, and consider `flock` on `<outputRoot>/<project>/` for cross-process safety.

---

## Med

### B4. Daemon has no project-name collision detection
[internal/config/loader.go:284-306](internal/config/loader.go#L284-L306), [internal/pipeline/projectname.go:41](internal/pipeline/projectname.go#L41)

`validateConfig` rejects empty names but does not reject two `projects[]` entries that resolve to the same `state.json` path. Two configs like `{name: foo, path: /a/x}` and `{name: foo, path: /b/x}`, or two distinct names pointing at the same absolute path, share state and todos. `deriveProjectName` accepts a `nil` usedNames map (per the audit) so collision-suffixing never engages from the daemon loop.

Fix: in `validateConfig` build the set of resolved `<outputRoot>/<project>/` directories and reject duplicates. Alternatively in `runDaemonCycle` ([cmd/daemon.go:116-159](cmd/daemon.go#L116-L159)) track used names and re-derive uniquely.

### B5. `ProviderBoundaryHeadroom == 0` cannot disable the headroom check
[internal/config/loader.go:258-260](internal/config/loader.go#L258-L260)

```go
if chunk.ProviderBoundaryHeadroom == 0 {
    chunk.ProviderBoundaryHeadroom = DefaultProviderBoundaryHeadroom
}
```

The field's documented range is `[0.0, 1.0)`. The "unset" sentinel and the valid disable value collide. A user who legitimately sets `provider_boundary_headroom: 0` silently gets `0.20`.

Fix: change the field to `*float64`, or pick a sentinel that is not a legal value (e.g. `< 0`).

### B6. `FindingHashes` grows without bound
[internal/state/tracker.go:47](internal/state/tracker.go#L47), [pipeline.go:430](internal/pipeline/pipeline.go#L430)

`state.json` accumulates every finding hash ever emitted, forever. Long-lived projects (months of daemon runs, dozens of findings per run) will push `state.json` into the MB range. The slice is rewritten in full on every `Save`, so I/O cost scales with history.

Fix: cap to a rolling window (e.g. last N runs or last 30 days), or move finding hashes out of `state.json` into an append-only file the pipeline reads via streaming dedup.

### B7. `state.json` lacks fsync after rename
[internal/state/tracker.go:136](internal/state/tracker.go#L136)

`os.Rename` is metadata-atomic, but the parent directory entry can be lost on power-loss until fsync'd. Same gap exists for `todos.md`. Low probability in practice, but the codebase treats state as durable.

Fix: open parent dir, `(*os.File).Sync()` after the rename. Or accept the gap and document it.

### B8. OpenCode reader leaks `*sql.Rows` if a panic occurs between `Query` and the loop close
[internal/chat/readers/opencode.go:96-115](internal/chat/readers/opencode.go#L96-L115)

`database.Query` returns rows at line 96; rows are closed manually at line 110 (error path) and line 115 (happy path). There is no `defer rows.Close()`. Any panic — OOM, unexpected scan return, future edit — leaks the rows handle for the rest of the process. `rows.Err()` is also never checked after the read loop, so iteration errors (driver-level) are silently dropped.

Fix: `defer rows.Close()` after `Query` succeeds; replace manual `rows.Close()` calls; add `if err := rows.Err(); err != nil { return nil, … }` after the loop.

### B9. Kiro assistant turns have no timestamp
[internal/chat/readers/kiro.go:185-200](internal/chat/readers/kiro.go#L185-L200)

`kiroAssistantTurnMessage` returns `ChatMessage{Role: "assistant", Content: text}` with the zero `Timestamp`, while `kiroUserTurnMessage` populates it from `userRecord["timestamp"]`. Downstream `--since` filtering and source-sort logic that touches `Timestamp` will treat every assistant turn as "from epoch".

Fix: extract a timestamp from the assistant turn (often a sibling key in the JSON), or fall back to the paired user-turn timestamp.

### B10. SQLite timestamp parse errors silently zero out `ModifiedTime`
[internal/chat/readers/opencode.go:69](internal/chat/readers/opencode.go#L69), [internal/chat/readers/sqlite.go:~69](internal/chat/readers/sqlite.go), [internal/chat/readers/kiro.go:68](internal/chat/readers/kiro.go#L68)

`modified, _ := parseTimestamp(updated)` drops the error. Rows with unparseable `time_updated` get `time.Time{}`, which then breaks discovery sort (everything epoch-old sinks to the end) and `--since` filtering (treated as far past).

Fix: log a warning on parse failure; consider falling back to the file's mtime when the row's timestamp is unparseable.

### B11. JSONL parse errors silently swallowed in discovery probes
[internal/chat/probe.go:108-109](internal/chat/probe.go#L108-L109)

Per the audit, the probe loop does `if err := json.Unmarshal(...); err != nil { continue }` without logging. Corrupt JSONL inputs silently get partial probe results, which the scoping decision then trusts. The blast radius is bounded (worst case a non-matching chat gets included or excluded), but invisible failure makes diagnosis painful.

Fix: log at debug level on parse failure; track a per-source `probe_errors` counter.

### B12. State schema has no migration story
[internal/state/tracker.go:24,42-56,105](internal/state/tracker.go#L24)

`StateVersion = 1` is hardcoded; `Load` calls `json.Unmarshal` (not strict) so unknown fields are silently dropped and missing fields default. There is no version-check branch, no `state.json.bak`, no upgrade hook. Whenever the schema gains a required field, every existing user's state will load as zero-valued for that field — by the time anyone notices it has already been written back.

Fix: gate `Load` on `current.Version <= StateVersion`, refuse newer versions, run an `upgradeStateInPlace` step for older. At minimum, write `state.json.v1.bak` before overwriting.

### B13. Documented entry point in `CLAUDE.md` no longer exists
[CLAUDE.md:30,68](CLAUDE.md#L30) vs reality

`CLAUDE.md` describes `cmd/analyze_pipeline.go:executeAnalyze` as the analysis core. The actual entry is `pipeline.Run` in [internal/pipeline/pipeline.go:140](internal/pipeline/pipeline.go#L140); `cmd/analyze.go` is a thin Cobra wrapper. Same drift for `mergeRulePacks`/`applyRuleToggles`. Not a runtime bug, but it leads humans (and future agents reading `CLAUDE.md`) into a file that does not exist.

Fix: rewrite the relevant `CLAUDE.md` paragraphs to point at `internal/pipeline/`.

### B14. Orchestrator confidence threshold filter is inverted at the boundary
[internal/analyzer/orchestrator.go:172](internal/analyzer/orchestrator.go#L172) (per audit; line drift possible)

Per the analyzer-subsystem audit, the filter admits items with `confidence == 0` (treats them as "no signal" rather than "below threshold") and items `>= threshold`, but excludes `(0, threshold)`. The intent appears to be "drop low-confidence noise", but LLM outputs that emit `confidence: 0` are passed through. NaN slips through `> 0 && < threshold` too.

Fix: classify the missing/0 case explicitly (probably drop), use `>=` consistently, and reject NaN with `math.IsNaN`.

### B15. ACP permission handler swallows JSON unmarshal errors
[internal/analyzer/providers/acpcore/acpcore.go:~531](internal/analyzer/providers/acpcore/acpcore.go) (per audit)

`_ = json.Unmarshal(...)` for the permission request body. Malformed params produce an empty struct, which then takes the default branch and approves. A misbehaving provider can effectively bypass permission checks by sending invalid JSON.

Fix: return a denial on unmarshal failure.

### B16. ACP-core context downgrade silently drops cancellation
[internal/analyzer/providers/copilotsdk/copilotsdk.go:85,144](internal/analyzer/providers/copilotsdk/copilotsdk.go#L85) (per audit)

`if ctx == nil { ctx = context.Background() }` swallows what should be a programmer error and re-anchors the new ctx outside the caller's cancellation tree. Subsequent provider calls cannot be cancelled by the daemon's signal handler.

Fix: panic or return an error; do not silently replace nil contexts.

---

## Low

### B17. `gemini-cli` path filter is case-sensitive and OS-naïve
[internal/chat/source_gemini_cli.go:45](internal/chat/source_gemini_cli.go#L45)

`strings.Contains(path, sep+"chats"+sep)` will miss case variants and assumes the OS uses one separator. Falls through to wider discovery, not a crash, but the scoping decision becomes wrong.

### B18. Manual rotation absent for `dreamer.log`
[internal/logging/logger.go:80](internal/logging/logger.go#L80)

`logger.New` opens the file with `O_APPEND` and never rotates. A daemon left running for weeks will grow `dreamer.log` without bound. Most ops setups will external-log-rotate, but the README does not mention this.

### B19. Logger always mirrors to stderr
[internal/logging/logger.go:86](internal/logging/logger.go#L86)

`io.MultiWriter(file, os.Stderr)` is hard-coded. For a daemon launched by `schtasks.exe` or systemd this is fine; for users running `dreamer daemon` interactively while reading `todos.md` it is noisy and there is no way to silence it short of `2>/dev/null`.

### B20. `provider.go`-style `init()`-registration with no synchronization
[internal/chat/provider.go:15-20](internal/chat/provider.go#L15-L20) (per audit)

A global `registeredProviders` slice is appended to by package `init()` functions. Go guarantees `init()` runs serially within a package and across imports of that package, so this is actually safe today — but it is the kind of pattern that becomes a race the moment someone adds dynamic registration.

### B21. `ChatCacheKey` uses single-byte separator, not length-prefixed
[internal/state/tracker.go:206-214](internal/state/tracker.go#L206-L214)

`path \x00 fileHash \x00 repoHeadSHA` then SHA-256. Collisions require a null byte in `path` or `fileHash`, which the OS forbids and the hex output disallows. The audit's concern is theoretical for this construction.

### B22. `cmd/helpers.go` re-implements home expansion
[cmd/helpers.go:55](cmd/helpers.go#L55) vs [internal/config/loader.go:320](internal/config/loader.go#L320)

Two `~`-expansion functions exist. Drift risk only.

### B23. `todo.txt` is stale
[todo.txt:1-2](todo.txt#L1-L2)

"add a option for returning null when nothing relevent" / "implement logging" — first is misspelled and unscoped, second has been done. Delete.

### B24. Windows-only `schtasks.exe` invocation only escapes double-quotes
[cmd/startup.go:120-122](cmd/startup.go#L120-L122)

`executablePath` comes from `os.Executable()` and `configPath` from `--config`, so the threat model is bounded, but escapes for `^`, `&`, `|`, backticks are missing. Low risk because the inputs are not attacker-controlled; raised because the surface exists.

### B25. `Logger.Close` may be called on a nil receiver in command teardown
([cmd/analyze.go:57](cmd/analyze.go#L57), [cmd/daemon.go:42-44](cmd/daemon.go#L42-L44))

Both defer `_ = logger.Close()` — if `logging.New` returns `(nil, err)` and the caller forgets to check, `Close()` would panic. Today the code does check, but the `_ =` swallows future regressions.

---

## Rejected claims

Items the sub-agents flagged that verification disproved or downgraded:

- **"`loader.go:299` rejects `~/foo` as not absolute"** — the code expands at line 294, *then* `IsAbs`-checks at line 299. Order is correct.
- **"`opencode.go` double-closes rows"** — line 110 (error path) and line 115 (happy path) are mutually exclusive; there is no `defer`. The real bug is the *missing* defer (B8), not a double-close.
- **"Discovery cache mtime equality could mask same-mtime edits"** — true in theory, but the cache is keyed by SHA256 hash, not mtime; the mtime check is the *fast path* before hashing. Not a correctness gap.
