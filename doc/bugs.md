# dreamer — bugs

Generated 2026-05-18, re-verified 2026-05-19, updated 2026-05-26. Fixed bugs removed; remaining bugs verified against current source.

Severity legend: **high** = data loss, security, or silent wrong answers in normal operation. **med** = wrong answer under realistic but non-default conditions, or unbounded resource growth. **low** = robustness gap that only fires under pathological input or a future change.

Triage legend: **fix-now** = ship before next release (data loss, security, silent wrong answers). **fix-later** = correctness under odd inputs, unbounded growth, hygiene. **skip** = theoretical or low-risk polish — fold in when adjacent code changes.

---

## Triage matrix

| ID | Severity | Triage | Area | One-line |
|----|----------|--------|------|----------|
| B8  | med  | **fix-later** | readers     | OpenCode `ReadMessages` leaks rows on panic, no `rows.Err()` |
| B9  | med  | **fix-later** | readers     | Kiro assistant turns have no timestamp |
| B10 | med  | **fix-later** | readers     | SQLite timestamp parse errors silently zero `ModifiedTime` |
| B11 | med  | **fix-later** | discovery   | JSONL parse errors silently swallowed in probes |
| B22 | low  | **fix-later** | cmd         | `cmd/helpers.go` re-implements home expansion |
| B17 | low  | **skip**      | discovery   | gemini-cli path filter case-sensitive / OS-naïve |
| B18 | low  | **skip**      | logging     | `dreamer.log` has no rotation |
| B19 | low  | **skip**      | logging     | Logger always mirrors to stderr |
| B20 | low  | **skip**      | discovery   | `init()`-registration with no synchronization (safe today) |
| B24 | low  | **skip**      | startup     | `schtasks.exe` only escapes double-quotes |
| B25 | low  | **skip**      | cmd         | `Logger.Close` `_ =` swallow risks future nil-receiver |

### Fixed since 2026-05-18

B1–B7, B12–B16, B21, B23, B26–B29, Bx — all resolved. See `computeCacheKeys` (B1/B26), `shellWriteIdiomRE` (B2), `WriteFileAtomic` (B3), project-name collision check (B4), `*float64` pointer (B5), `MaxFindingHashes` cap (B6), fsync in `fsutil.WriteFileAtomic` (B7), state version gate + `.bak` backup (B12), `filterMistakesByThreshold` NaN/0 handling (B14), ACP permission decision before parse (B15), `ChatCacheKey` length-prefix (B21), `pruneLastRunPerCategory` (B28), `pruneProviderUsage` (B29).

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

### B22. `cmd/helpers.go` re-implements home expansion — **low**
[cmd/helpers.go:54](cmd/helpers.go#L54) vs [internal/config/loader.go:320](internal/config/loader.go#L320)

Two `~`-expansion functions exist (`expandHomePath` vs `ExpandUserHome`). Same logic, drift risk only.

Fix: delete the cmd-layer copy; call `config.ExpandUserHome` directly.

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
