# dreamer — bugs

Generated 2026-05-18, re-verified 2026-05-19. Findings come from a six-agent parallel audit plus first-hand verification of the most load-bearing files. Every entry below has been re-checked against current source. Items the audit raised but verification disproved are listed at the end under "Rejected claims".

Severity legend: **high** = data loss, security, or silent wrong answers in normal operation. **med** = wrong answer under realistic but non-default conditions, or unbounded resource growth. **low** = robustness gap that only fires under pathological input or a future change.

Triage legend: **fix-now** = ship before next release (data loss, security, silent wrong answers). **fix-later** = correctness under odd inputs, unbounded growth, hygiene. **skip** = theoretical or low-risk polish — fold in when adjacent code changes.

---

## Triage matrix

| ID | Severity | Triage | Area | One-line |
|----|----------|--------|------|----------|
| B1  | high | **fix-now**   | state.json | `ChatHashes` clobbered when a chat file fails to hash |
| B2  | high | **fix-now**   | sandbox     | Read-only shell trusts SDK classifier; no second-pass |
| B3  | high | **fix-now**   | output      | `todos.md` writes non-atomic and unlocked |
| B4  | med  | **fix-now**   | config      | Daemon has no project-name collision detection |
| B5  | med  | **fix-now**   | config      | `ProviderBoundaryHeadroom == 0` cannot disable headroom |
| B6  | med  | **fix-now**   | state.json  | `FindingHashes` grows without bound |
| B7  | med  | **fix-now**   | state.json  | `state.json` lacks fsync after rename |
| B12 | med  | **fix-now**   | state.json  | State schema has no migration story |
| B14 | med  | **fix-now**   | analyzer    | Confidence-threshold filter admits 0 and NaN |
| B15 | med  | **fix-now**   | sandbox     | ACP permission handler swallows JSON unmarshal errors |
| B16 | med  | **fix-now**   | runtime     | ACP-core silently replaces nil ctx with Background |
| B21 | low  | **fix-now**   | state.json  | `ChatCacheKey` single-byte separator (theoretical only) |
| B26 | med  | **fix-now**   | state.json  | `ChatHashes` keeps entries for deleted chat files |
| B27 | med  | **fix-now**   | state.json  | `normalizeState` silently rewrites zero `Version` on load |
| B28 | low  | **fix-now**   | state.json  | `LastRunPerCategory` never prunes removed categories |
| B29 | low  | **fix-now**   | state.json  | `ProviderUsage` never prunes unused provider ids |
| Bx  | med  | **fix-now**   | sandbox     | `decideFilesystem` allow-by-default when root is empty |
| B8  | med  | **fix-later** | readers     | OpenCode `ReadMessages` leaks rows on panic, no `rows.Err()` |
| B9  | med  | **fix-later** | readers     | Kiro assistant turns have no timestamp |
| B10 | med  | **fix-later** | readers     | SQLite timestamp parse errors silently zero `ModifiedTime` |
| B11 | med  | **fix-later** | discovery   | JSONL parse errors silently swallowed in probes |
| B13 | med  | **fix-later** | docs        | Documented entry point in `CLAUDE.md` no longer exists |
| B22 | low  | **fix-later** | cmd         | `cmd/helpers.go` re-implements home expansion |
| B23 | low  | **fix-later** | docs        | `todo.txt` is stale |
| B17 | low  | **skip**      | discovery   | gemini-cli path filter case-sensitive / OS-naïve |
| B18 | low  | **skip**      | logging     | `dreamer.log` has no rotation |
| B19 | low  | **skip**      | logging     | Logger always mirrors to stderr |
| B20 | low  | **skip**      | discovery   | `init()`-registration with no synchronization (safe today) |
| B24 | low  | **skip**      | startup     | `schtasks.exe` only escapes double-quotes |
| B25 | low  | **skip**      | cmd         | `Logger.Close` `_ =` swallow risks future nil-receiver |

---

## Fix now

### B1. `state.ChatHashes` is clobbered when a chat file fails to hash — **high**
[internal/pipeline/pipeline.go:429](internal/pipeline/pipeline.go#L429) (also [:274](internal/pipeline/pipeline.go#L274))

The loop at [pipeline.go:213-231](internal/pipeline/pipeline.go#L213-L231) skips a source on hash error (`continue` at line 218) without adding its prior cache key to `cacheKeys`. Line 429 then does `currentState.ChatHashes = cacheKeys`, replacing the entire map. Every previously-cached entry for a temporarily-unhashable file is permanently lost — on the next run the file is treated as "fresh" and re-analyzed even if its content has not changed. This is partly self-healing (`cacheUnchanged` will see the shortened key set and re-run) but the prior `existing == key` evidence is gone.

Fix: union prior `currentState.ChatHashes` into `cacheKeys` for paths that failed to hash this run before assigning, or only assign per-key. **Coordinate with B26**: deleted-file pruning must remain intentional after the union fix.

### B2. Read-only shell enforcement trusts the upstream classifier — **high**
[internal/analyzer/permission.go:65-81](internal/analyzer/permission.go#L65-L81)

`shellRequestReadOnly` only consults `req.HasWriteFileRedirection` and `req.Commands[i].ReadOnly` — both populated by the provider SDK. The Copilot SDK's redirection detector handles `>` / `>>` but does not parse heredocs (`<<EOF … EOF`), process substitution (`>(cmd)`), `tee`, piped writers (`cmd | dd of=…`), or `bash -c 'inline write'`. A prompt-injected chat transcript can plausibly nudge the analyzer into running a write disguised as a read. The decision layer has zero independent syntax check; if the SDK ever miscategorises, the sandbox is open.

Fix: add a second-stage regex/grammar pass on the command string itself (deny on common write idioms even when SDK says "read-only"), or restrict shell to a known allow-list rather than allow-by-default.

### B3. `todos.md` writes are non-atomic and unlocked — **high**
[internal/output/generator.go:67](internal/output/generator.go#L67)

`os.WriteFile` truncates then writes. A crash mid-write leaves a half-written `todos.md` — a file the daemon then reads back and merges against on the next cycle, propagating corruption into `extractExistingFindingHashes`. Compare with `state.Save` at [internal/state/tracker.go:132-139](internal/state/tracker.go#L132-L139), which does temp-file + `os.Rename` correctly. The daemon also runs projects sequentially but the discovery cache is shared across cycles; if a second `dreamer analyze` invocation is launched while the daemon is mid-cycle (entirely plausible — same binary, same output root), two writers can interleave.

Fix: extract a shared `WriteFileAtomic(path, data, perm)` helper, use it from both writers, and consider `flock` on `<outputRoot>/<project>/` for cross-process safety.

### B4. Daemon has no project-name collision detection — **med**
[internal/config/loader.go:284-306](internal/config/loader.go#L284-L306), [internal/pipeline/projectname.go:41](internal/pipeline/projectname.go#L41)

`validateConfig` rejects empty names but does not reject two `projects[]` entries that resolve to the same `state.json` path. Two configs like `{name: foo, path: /a/x}` and `{name: foo, path: /b/x}`, or two distinct names pointing at the same absolute path, share state and todos. `deriveProjectName` accepts a `nil` usedNames map (per the audit) so collision-suffixing never engages from the daemon loop.

Fix: in `validateConfig` build the set of resolved `<outputRoot>/<project>/` directories and reject duplicates. Alternatively in `runDaemonCycle` ([cmd/daemon.go:113-163](cmd/daemon.go#L113-L163)) track used names and re-derive uniquely.

### B5. `ProviderBoundaryHeadroom == 0` cannot disable the headroom check — **med**
[internal/config/loader.go:258-260](internal/config/loader.go#L258-L260)

```go
if chunk.ProviderBoundaryHeadroom == 0 {
    chunk.ProviderBoundaryHeadroom = DefaultProviderBoundaryHeadroom
}
```

The field's documented range is `[0.0, 1.0)`. The "unset" sentinel and the valid disable value collide. A user who legitimately sets `provider_boundary_headroom: 0` silently gets `0.20`.

Fix: change the field to `*float64`, or pick a sentinel that is not a legal value (e.g. `< 0`).

### B14. Orchestrator confidence threshold filter is inverted at the boundary — **med**
[internal/analyzer/orchestrator.go:166-178](internal/analyzer/orchestrator.go#L166-L178) (same pattern in `validateFindings` at [:184](internal/analyzer/orchestrator.go#L184))

`if m.Confidence > 0 && m.Confidence < threshold { continue }` — items with `confidence == 0` pass through (treated as "no signal" rather than "below threshold"); NaN also slips through because `NaN > 0` and `NaN < threshold` both return false. The intent is "drop low-confidence noise"; today the orchestrator emits anything with a missing-or-zero confidence and anything with a NaN confidence.

Fix: classify the missing/0 case explicitly (drop or pass-through, but decide), use `>=` consistently against the threshold, and reject NaN with `math.IsNaN`.

### B15. ACP permission handler swallows JSON unmarshal errors — **med**
[internal/analyzer/providers/acpcore/acpcore.go:529-547](internal/analyzer/providers/acpcore/acpcore.go#L529-L547)

`_ = json.Unmarshal(envelope.Params, &params)` for the permission request body, followed by `approved := true` as the post-default. Malformed params produce a nil map, which then takes the default branch and approves. A misbehaving provider can effectively bypass permission checks by sending invalid JSON.

Fix: return a denial on unmarshal failure.

### B16. ACP-core context downgrade silently drops cancellation — **med**
[internal/analyzer/providers/copilotsdk/copilotsdk.go:85,101,144](internal/analyzer/providers/copilotsdk/copilotsdk.go#L85)

Three call sites all `if ctx == nil { ctx = context.Background() }`. This swallows what should be a programmer error and re-anchors the new ctx outside the caller's cancellation tree. Subsequent provider calls cannot be cancelled by the daemon's signal handler.

Fix: panic or return an error; do not silently replace nil contexts.

### Bx. `decideFilesystem` allow-by-default when root is empty — **med**
[internal/analyzer/permission.go:36-39](internal/analyzer/permission.go#L36-L39)

```go
if normalizedRoot == "" {
    return PermissionDecision{Approved: true}
}
```

Today the pipeline always populates `WorkingDirectory`, but a future caller that forgets to set it would unwittingly get an unrestricted analyzer with read access anywhere on disk. Fail-open at a security chokepoint is the wrong default.

Fix: fail closed when `normalizedRoot == ""`. Require callers to set the root explicitly; deny otherwise.

### B6. `FindingHashes` grows without bound — **med**
[internal/state/tracker.go:47](internal/state/tracker.go#L47), [pipeline.go:430](internal/pipeline/pipeline.go#L430)

`state.json` accumulates every finding hash ever emitted, forever. Long-lived projects (months of daemon runs, dozens of findings per run) will push `state.json` into the MB range. The slice is rewritten in full on every `Save`, so I/O cost scales with history.

Fix: cap to a rolling window (e.g. last N runs or last 30 days), or move finding hashes out of `state.json` into an append-only file the pipeline reads via streaming dedup.

### B7. `state.json` lacks fsync after rename — **med**
[internal/state/tracker.go:136](internal/state/tracker.go#L136)

`os.Rename` is metadata-atomic, but the parent directory entry can be lost on power-loss until fsync'd. Same gap exists for `todos.md` once B3 is fixed. Low probability in practice, but the codebase treats state as durable.

Fix: open parent dir, `(*os.File).Sync()` after the rename. Or accept the gap and document it.

### B12. State schema has no migration story — **med**
[internal/state/tracker.go:24,42-56,92-110](internal/state/tracker.go#L24)

`StateVersion = 1` is hardcoded; `Load` calls `json.Unmarshal` (not strict) so unknown fields are silently dropped and missing fields default. `normalizeState` then rewrites `Version` to current (B27). There is no version-check branch, no `state.json.bak`, no upgrade hook. Whenever the schema gains a required field, every existing user's state will load as zero-valued for that field — by the time anyone notices it has already been written back.

Fix: gate `Load` on `current.Version <= StateVersion`, refuse newer versions, run an `upgradeStateInPlace` step for older. At minimum, write `state.json.v1.bak` before overwriting.

### B21. `ChatCacheKey` uses single-byte separator, not length-prefixed — **low**
[internal/state/tracker.go:206-214](internal/state/tracker.go#L206-L214)

`path \x00 fileHash \x00 repoHeadSHA` then SHA-256. Collisions require a null byte in `path` or `fileHash`, which the OS forbids and hex output disallows. Theoretical, but trivial to harden alongside the other state.json work.

Fix: length-prefix each field (`len(path)|path|len(fileHash)|fileHash|len(repoHeadSHA)|repoHeadSHA`) before hashing.

### B26. `ChatHashes` keeps entries for deleted chat files — **med**
[internal/pipeline/pipeline.go:429](internal/pipeline/pipeline.go#L429)

Currently the wholesale `currentState.ChatHashes = cacheKeys` assignment effectively prunes entries for files that no longer exist (they're absent from this run's `cacheKeys`). This is correct-by-accident: the same line is also B1's data-loss bug. Once B1 is fixed by unioning prior keys for hash-failed paths, **the deleted-file case must remain explicit** — otherwise stale paths from years ago will linger forever and the map will grow without bound.

Fix: in the B1 union, distinguish "file present this run but hash failed" (keep prior key) from "file absent this run" (drop). Prune absent paths explicitly.

### B27. `normalizeState` silently rewrites zero `Version` on load — **med**
[internal/state/tracker.go:155-157](internal/state/tracker.go#L155)

```go
if s.Version == 0 {
    s.Version = StateVersion
}
```

A `state.json` with `version: 0` or no `version` field is silently promoted to the current version *on read*, before any save. This masks two legitimate signals: (a) a corrupted/truncated state file, and (b) the "old format predating versioning" case. Combined with B12 (no migration gate), every future schema bump will see existing files arrive as "current version" with missing fields zeroed.

Fix: distinguish "no version field" (treat as 0, run upgrader) from "explicit version=0" (refuse, write `.bak`). Couple with B12.

### B28. `LastRunPerCategory` never prunes removed categories — **low**
[internal/state/tracker.go:55](internal/state/tracker.go#L55), [pipeline.go:431-436](internal/pipeline/pipeline.go#L431-L436)

Map gains an entry per category completed; never loses one. Disabling a rule category in config or renaming a `RuleCategory` constant leaves the old key in state forever. Low impact (map stays small) but contributes to long-term cruft.

Fix: on `Save`, drop keys that aren't in the currently-loaded rule pack set.

### B29. `ProviderUsage` never prunes unused provider ids — **low**
[internal/state/tracker.go:48](internal/state/tracker.go#L48)

Same shape as B28: switch providers and the old provider id's `runs/total_tokens/failures` counters live on. Useful for one-shot historical reporting; not useful long-term. Trivial size, but pairs with B6 for "state.json grows forever" hygiene.

Fix: optional `--prune` command, or drop entries with `LastSuccessUTC` older than N days.

---

## Fix later

### B8. OpenCode reader leaks `*sql.Rows` and ignores iteration errors — **med**
[internal/chat/readers/opencode.go:96-115](internal/chat/readers/opencode.go#L96-L115)

`ListSessions` is fine (has `defer rows.Close()` + `rows.Err()` check). `ReadMessages` is the offender: rows are closed manually at line 110 (error path) and line 115 (happy path); there is no `defer rows.Close()` and no `rows.Err()` after the loop. Any panic between `Query` and the manual close leaks the handle; driver-level iteration errors are silently dropped.

Fix: `defer rows.Close()` after `Query` succeeds; replace manual `rows.Close()` calls; add `if err := rows.Err(); err != nil { return nil, … }` after the loop.

### B9. Kiro assistant turns have no timestamp — **med**
[internal/chat/readers/kiro.go:185-200](internal/chat/readers/kiro.go#L185-L200)

`kiroAssistantTurnMessage` returns `ChatMessage{Role: "assistant", Content: text}` with the zero `Timestamp`, while `kiroUserTurnMessage` populates it from `userRecord["timestamp"]`. Downstream `--since` filtering and source-sort logic that touches `Timestamp` will treat every assistant turn as "from epoch".

Fix: extract a timestamp from the assistant turn (often a sibling key in the JSON), or fall back to the paired user-turn timestamp.

### B10. SQLite timestamp parse errors silently zero out `ModifiedTime` — **med**
[internal/chat/readers/opencode.go:69](internal/chat/readers/opencode.go#L69), [internal/chat/readers/sqlite.go:~69](internal/chat/readers/sqlite.go), [internal/chat/readers/kiro.go:68](internal/chat/readers/kiro.go#L68)

`modified, _ := parseTimestamp(updated)` drops the error. Rows with unparseable `time_updated` get `time.Time{}`, which then breaks discovery sort (everything epoch-old sinks to the end) and `--since` filtering (treated as far past).

Fix: log a warning on parse failure; consider falling back to the file's mtime when the row's timestamp is unparseable.

### B11. JSONL parse errors silently swallowed in discovery probes — **med**
[internal/chat/probe.go:107-110](internal/chat/probe.go#L107-L110)

`if err := json.Unmarshal(...); err != nil { continue }` without logging. Corrupt JSONL inputs silently yield partial probe results, which the scoping decision then trusts. The blast radius is bounded (worst case a non-matching chat gets included or excluded), but invisible failure makes diagnosis painful.

Fix: log at debug level on parse failure; track a per-source `probe_errors` counter. Same pattern in [opencode.go:170,213](internal/chat/readers/opencode.go#L170), [kiro.go](internal/chat/readers/kiro.go), and the ACP-core stream handler.

### B13. Documented entry point in `CLAUDE.md` no longer exists — **med**
[CLAUDE.md:30,68](CLAUDE.md#L30) vs reality

`CLAUDE.md` describes `cmd/analyze_pipeline.go:executeAnalyze` as the analysis core. The actual entry is `pipeline.Run` in [internal/pipeline/pipeline.go:140](internal/pipeline/pipeline.go#L140); `cmd/analyze.go` is a thin Cobra wrapper. Same drift for `mergeRulePacks` / `applyRuleToggles`, which live in [internal/pipeline/rules.go:19,36](internal/pipeline/rules.go#L19). Not a runtime bug, but it leads humans (and future agents reading `CLAUDE.md`) into a file that does not exist.

Fix: rewrite the relevant `CLAUDE.md` paragraphs to point at `internal/pipeline/`.

### B22. `cmd/helpers.go` re-implements home expansion — **low**
[cmd/helpers.go:54](cmd/helpers.go#L54) vs [internal/config/loader.go:320](internal/config/loader.go#L320)

Two `~`-expansion functions exist (`expandHomePath` vs `ExpandUserHome`). Same logic, drift risk only.

Fix: delete the cmd-layer copy; call `config.ExpandUserHome` directly.

### B23. `todo.txt` is stale — **low**
[todo.txt:1](todo.txt#L1)

Single line `add a option for returning null when nothing relevent` — misspelled, unscoped, predates the current pipeline.

Fix: delete.

---

## Skip / theoretical

### B17. `gemini-cli` path filter is case-sensitive and OS-naïve — **low**
[internal/chat/source_gemini_cli.go:45](internal/chat/source_gemini_cli.go#L45)

`strings.Contains(path, sep+"chats"+sep)` will miss case variants and assumes the OS uses one separator. Falls through to wider discovery, not a crash, but the scoping decision becomes wrong on case-insensitive filesystems.

### B18. Manual rotation absent for `dreamer.log` — **low**
[internal/logging/logger.go:80](internal/logging/logger.go#L80)

`logger.New` opens the file with `O_APPEND` and never rotates. A daemon left running for weeks will grow `dreamer.log` without bound. Most ops setups will external-log-rotate; README does not mention this.

### B19. Logger always mirrors to stderr — **low**
[internal/logging/logger.go:86](internal/logging/logger.go#L86)

`io.MultiWriter(file, os.Stderr)` is hard-coded. For daemons launched by `schtasks.exe` or systemd this is fine; for users running `dreamer daemon` interactively while reading `todos.md` it is noisy and there is no way to silence it short of `2>/dev/null`.

### B20. `provider.go`-style `init()`-registration with no synchronization — **low**
[internal/chat/provider.go:15-21](internal/chat/provider.go#L15-L21)

Global `registeredProviders` slice is appended to by package `init()` functions. Go guarantees `init()` runs serially within a package and across imports, so this is safe today — but the pattern becomes a race the moment someone adds dynamic registration.

### B24. Windows-only `schtasks.exe` invocation only escapes double-quotes — **low**
[cmd/startup.go:120-122](cmd/startup.go#L120-L122)

`executablePath` comes from `os.Executable()` and `configPath` from `--config`, so the threat model is bounded. Escapes for `^`, `&`, `|`, backticks are missing. Low risk because inputs are not attacker-controlled.

### B25. `Logger.Close` may be called on a nil receiver in command teardown — **low**
[cmd/analyze.go:57](cmd/analyze.go#L57), [cmd/daemon.go:42-44](cmd/daemon.go#L42-L44)

Both defer `_ = logger.Close()` — if `logging.New` returns `(nil, err)` and the caller forgets to check, `Close()` would panic. Today the code does check, but `_ =` swallows future regressions.

---

## Rejected claims

Items the sub-agents flagged that verification disproved or downgraded:

- **"`loader.go:299` rejects `~/foo` as not absolute"** — code expands at line 294, *then* `IsAbs`-checks at line 299. Order is correct.
- **"`opencode.go` double-closes rows"** — line 110 (error path) and line 115 (happy path) are mutually exclusive; there is no `defer`. Real bug is the *missing* defer (B8), not a double-close.
- **"`ListOpenCodeSessions` leaks rows"** — has `defer rows.Close()` + `rows.Err()`. Fine. Only `ReadMessages` is affected (B8).
- **"Discovery cache mtime equality could mask same-mtime edits"** — true in theory, but cache is keyed by SHA256 hash; mtime is the fast-path check before hashing. Not a correctness gap.
