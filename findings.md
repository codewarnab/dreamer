# Phase 1: Concurrency Safety

Examined: 2026-05-30
Files: `internal/analyzer/providers/acpcore/transport.go`, `cmd/jobs.go`, `internal/state/cache.go`, `internal/pipeline/discovery_cache.go`, `internal/web/handlers/stateguard.go`, `internal/analyzer/sessionpool.go`, `internal/sandbox/windows_acl.go`, `internal/sandbox/sandbox.go`, `internal/pipeline/events.go`, `internal/web/handlers/jobs.go`, `internal/analyzer/providers/opencodehttp/opencodehttp.go`, `internal/jobqueue/queue.go`

---

### [HIGH] SessionPool: factory panic permanently deadlocks pool
- **Location:** internal/analyzer/sessionpool.go:69-71
- **Description:** `Acquire` increments `p.live` (line 65), acquires `p.factoryMu` (line 69), then calls `p.factory()` (line 70). Neither lock uses `defer`. If the factory panics, `factoryMu.Unlock()` (line 71) never runs, permanently deadlocking all subsequent `Acquire` calls. Additionally, `p.live` is never decremented, so even if the panic is recovered upstream, the pool's capacity is permanently reduced.
- **Impact:** Any panic in a provider's `NewSession` (network call, nil dereference on unexpected API response, subprocess spawn failure) permanently kills the session pool for the entire pipeline run. The orchestrator's Phase 1 and Phase 2 pools share this pattern.
- **Reproduction:** Trigger a panic in any provider factory (e.g., nil dereference on unexpected response shape). All subsequent `Acquire` calls block forever.

---

### [MEDIUM] transport.go: unchecked type assertion on sync.Map value
- **Location:** internal/analyzer/providers/acpcore/transport.go:223
- **Description:** `ch := chRaw.(chan jsonrpcResponse)` uses a bare type assertion without the two-value ok form. The `pending` sync.Map stores only `chan jsonrpcResponse` (from `call()` line 148), so the type is correct by construction. However, if a bug elsewhere ever stores a wrong type, this panics in the readLoop goroutine, crashing the entire transport.
- **Impact:** A corrupted pending map entry causes a panic in the readLoop goroutine, which kills the transport and all in-flight calls. The panic is unrecoverable since it's in a goroutine without defer-recover.
- **Reproduction:** Theoretical — only reachable if `pending` is corrupted externally. Low probability but high consequence.

---

### [MEDIUM] StateCache returns mutable pointers without copy
- **Location:** internal/state/cache.go:41-96 (GetState), internal/state/cache.go:102-154 (GetHistory)
- **Description:** `GetState` and `GetHistory` return cached `*State` and `*History` pointers directly (lines 52-53, 115-116). The struct comment (line 26) warns "callers must treat returned values as read-only," but nothing enforces this. A web handler that mutates the returned struct (e.g., appending to a slice, modifying a map) silently corrupts the cache for all concurrent readers.
- **Impact:** Silent data corruption if any handler mutates the returned state. The web dashboard reads state concurrently from multiple SSE connections and API handlers.
- **Reproduction:** A handler modifies a field on the returned `*State` — all subsequent `GetState` calls return the mutated value until `Invalidate` is called.

---

### [MEDIUM] sandbox PostStartOrKill: goroutine leak on process exit timeout
- **Location:** internal/sandbox/sandbox.go:311-325
- **Description:** `PostStartOrKill` spawns a goroutine that calls `cmd.Wait()` with a 10-second timeout. If the process doesn't exit after `Kill()`, the goroutine blocks on `<-done` indefinitely (the `select` falls through to the warn log, but the outer goroutine never exits because `done` is never closed). On Windows, `KILL_ON_JOB_CLOSE` eventually cleans up, but on POSIX a zombie/D-state child leaks the goroutine permanently.
- **Impact:** One leaked goroutine per failed sandbox PostStart. Bounded by the number of sandbox launch failures, but accumulates over long daemon runs.
- **Reproduction:** On Linux, trigger a PostStart error with a process that ignores SIGKILL (e.g., D-state). The goroutine leaks.

---

### [LOW] events.go: Subscribe/Unsubscribe safe but fragile pattern
- **Location:** internal/pipeline/events.go:49-64
- **Description:** `Subscribe` (line 51-53) and `Unsubscribe` (line 58-64) both hold `b.mu` during their operations, and `Publish` also holds `b.mu` during iteration. This is correct — no race exists. However, `Subscribe` uses manual Lock/Unlock without defer (line 51-53), which is safe today (only `append`, can't panic) but fragile if the method grows.
- **Impact:** None today. Future modifications to `Subscribe` that add operations between Lock and Unlock could introduce a lock leak.
- **Reproduction:** N/A — no current bug.

---

### [LOW] runGuards sync.Map accumulates entries for active jobs
- **Location:** internal/web/handlers/jobs.go:188
- **Description:** `runGuards` is a package-level `sync.Map` that stores `*atomic.Bool` per job ID. Entries are only cleaned up on job deletion (line 931), not on run completion. For active jobs that are run repeatedly, entries accumulate permanently. The values are small (`*atomic.Bool`), so memory impact is negligible, but the map never shrinks for the daemon's lifetime.
- **Impact:** Unbounded map growth proportional to the number of unique job IDs ever created in a daemon session. Practically negligible (each entry is ~40 bytes).
- **Reproduction:** Create and run 10,000 unique jobs without deleting them. `runGuards` has 10,000 entries.

---

### [LOW] windows_acl.go: Lock without defer
- **Location:** internal/sandbox/windows_acl.go:104-116
- **Description:** `acquireWritableACL` calls `writableACLState.Lock()` at line 104 and manually `Unlock()` at lines 109 (error) and 116 (success). Both paths are covered, and `snapshotDACL` is unlikely to panic. However, if `snapshotDACL` or `setAllowWriteACL` panics, the lock is never released.
- **Impact:** A panic in the Windows ACL manipulation permanently deadlocks all sandbox writable-ACL operations. Low probability — Windows API calls return errors rather than panicking.
- **Reproduction:** Theoretical — requires a panic in `snapshotDACL` or `setAllowWriteACL`.

---

## Greps Summary

**sync.Map usage (4 sites):**
- `transport.go:31` (pending) — ok-form on Load at line 222, bare type assertion at line 223
- `transport.go:38` (streams) — ok-form on Load at line 106, ok-form on type assertion at line 110 ✓
- `jobs.go:188` (runGuards) — bare type assertion at line 192 (safe by construction, only `*atomic.Bool` stored via LoadOrStore)

**Lock without defer (non-test, significant):**
- `transport.go:162,168` — manual lock/unlock in `call()` for permHandler scope; safe (only assignment between lock/unlock)
- `transport.go:279-285` — `markClosed()` manual lock; safe (only bool assignment + early return)
- `transport.go:308-310` — `drainStderr()` manual lock; safe (only Write between lock/unlock)
- `sessionpool.go:48` — `Acquire()` long lock with manual unlock on multiple paths; complex but correct
- `windows_acl.go:104-116` — manual lock/unlock; fragile (see finding above)
- `events.go:51-53` — `Subscribe()` manual lock; safe today
- `sessionpool.go:69-71` — `factoryMu` manual lock; **BUG on panic** (see HIGH finding)

**go func patterns (non-test):**
- `transport.go:331` — `cmd.Wait()` in goroutine for close timeout; error captured via channel ✓
- `transport.go:352` — `goroutines.Wait()` with close signal; safe ✓
- `transport.go:473` — `cmd.Wait()` error discarded; acceptable (process already killed) ✓
- `sandbox.go:311-325` — `cmd.Wait()` with 10s timeout; **goroutine leak risk** (see MEDIUM finding)
- `daemon.go:301,343,413,453` — daemon lifecycle goroutines; all properly scoped to context ✓
- `cliharness.go:233` — stdin writer goroutine; error captured via stderrBuf ✓
- `opencodehttp.go:162,292` — port detection and stderr drainer; both properly managed ✓
- `sessionpool.go:98` — watchdog goroutine for ctx cancellation; cleaned up via channel close ✓

---

# Phase 2: Error Handling & Silent Swallows

Examined: 2026-05-30
Files: `internal/chat/readers/opencode.go`, `internal/chat/readers/kiro.go`, `internal/chat/probe.go`, `cmd/stop.go`, `internal/pipeline/pipeline.go`, `internal/chat/readers/jsonl.go`, `internal/chat/readers/vscode.go`, `internal/chat/readers/gemini.go`, `internal/chat/readers/claude_tool_fold.go`, `internal/chat/readers/protobuf.go`, `internal/chat/readers/sqlite.go`

---

### [HIGH] opencode.go: missing rows.Err() after message iteration loop
- **Location:** internal/chat/readers/opencode.go:174-182
- **Description:** `ReadMessages` iterates `rows.Next()` at line 174 to collect message rows, then calls `rows.Close()` at line 182 — but never checks `rows.Err()`. If `rows.Next()` returns false due to a transient SQLite error (disk I/O, locked DB, corrupted page), the error is silently lost and the function returns a partial message list as if it were complete.
- **Impact:** Partial chat transcripts fed to the analyzer without any indication of truncation. The analysis runs on incomplete data, potentially missing mistakes that occurred in the unread portion of the conversation.
- **Reproduction:** Open an opencode DB, start reading messages, then cause a transient SQLite error mid-iteration (e.g., `PRAGMA lock_status` from another connection during WAL checkpoint). The reader returns whatever it collected before the error.

---

### [MEDIUM] probe.go: missing scanner.Err() in probeJSONLForCWD
- **Location:** internal/chat/probe.go:100-136
- **Description:** `probeJSONLForCWD` reads JSONL lines with a `bufio.Scanner` and returns the first extracted match. The loop at line 114 terminates when `scanner.Scan()` returns false, but the function returns `""` at line 135 without checking `scanner.Err()`. If the scanner encounters a read error (truncated file, I/O error), the function silently returns no match.
- **Impact:** Chat sources that are partially readable (e.g., truncated by a crash) are silently excluded from discovery. The user sees no warning that a file was skipped due to an error — it simply appears as if the file contained no CWD evidence.
- **Reproduction:** Truncate a Claude JSONL file mid-line. `probeJSONLForCWD` returns `""` instead of probing whatever was readable.

---

### [MEDIUM] probe.go: missing scanner.Err() in containsDreamerMarker
- **Location:** internal/chat/probe.go:223-246
- **Description:** `containsDreamerMarker` scans the first 10 lines for the DreamerMarker string. Like `probeJSONLForCWD`, it never checks `scanner.Err()` after the loop (line 245). A read error causes the function to return `false`, meaning the file is NOT skipped during discovery — potentially including a dreamer-created session in the analysis input.
- **Impact:** If a dreamer marker line is unreadable due to a file I/O error, the file passes the filter and gets included in discovery. This could feed dreamer's own analysis output back into the analyzer, creating a feedback loop.
- **Reproduction:** Create a JSONL with the DreamerMarker on line 3, then make line 2 unreadable (e.g., corrupt UTF-8 that triggers a scanner error). The marker check bails early and returns false.

---

### [MEDIUM] kiro.go: assistant turns have no timestamp
- **Location:** internal/chat/readers/kiro.go:282-297
- **Description:** `kiroAssistantTurnMessage` returns `ChatMessage{Role: "assistant", Content: text}` without setting `Timestamp` (line 296). The corresponding `kiroUserTurnMessage` does parse timestamps (lines 275-278). The Kiro conversation JSON stores timestamps on the user record but not the assistant record, so assistant messages always have a zero `time.Time`.
- **Impact:** When the pipeline merge-sorts messages by timestamp, all Kiro assistant messages cluster at the Unix epoch (1970-01-01). This breaks chronological ordering when messages from multiple sources are interleaved, and makes `--since` lookback filtering discard the wrong messages.
- **Reproduction:** Read a Kiro conversation with both user and assistant turns. All assistant messages have `Timestamp.IsZero() == true`.

---

### [MEDIUM] opencode.go + kiro.go: silent timestamp zeroing on parse failure
- **Location:** internal/chat/readers/opencode.go:95,136,198 and internal/chat/readers/kiro.go:68
- **Description:** All four sites use `modified, _ := parseTimestamp(value)`, discarding the boolean success indicator. When `parseTimestamp` returns `(time.Time{}, false)` — e.g., for an unexpected timestamp format or a nil value — the session/message gets a zero timestamp silently.
- **Impact:** Sessions with unparseable timestamps sort to the end of the merge-sorted discovery list (zero time is "oldest"). If a session has a corrupted or non-standard timestamp field, it may be excluded from the lookback window (`--since`) or sorted incorrectly relative to other sessions.
- **Reproduction:** Set `time_updated` to a non-standard format like `"yesterday"` in an opencode DB. The session gets `ModifiedTime = time.Time{}` and sorts last.

---

### [LOW] stop.go: overlay path error ignored
- **Location:** cmd/stop.go:24
- **Description:** `overlayPath, _ := config.GlobalOverlayPath()` discards the error. `GlobalOverlayPath` only errors when `UserConfigRoot()` fails (e.g., `$HOME`/`$USERPROFILE` unset). The empty overlay path is then passed to `LoadConfigWithOverlay`, which treats `""` as "no overlay" (the `os.ReadFile("")` error matches `os.IsNotExist`).
- **Impact:** None today — the behavior is correct by accident. If `LoadConfigWithOverlay` ever changes to error on an empty path, this would break silently.
- **Reproduction:** Unset `HOME` and `USERPROFILE`, then run `dreamer stop`. The overlay is silently skipped.

---

### [LOW] pipeline.go: provider.Close() errors swallowed on all paths
- **Location:** internal/pipeline/pipeline.go:537,622,830
- **Description:** `provider.Close()` return values are discarded with `_ =` on all three call sites. Line 537 (Start failure) and 622 (orchestrator failure) are error-recovery paths where the provider may be in an unknown state. Line 830 is the success-path defer — a Close failure here means the provider subprocess may linger.
- **Impact:** On the success path (line 830), a Close failure means the provider subprocess (e.g., a CLI provider child process) may not be terminated. The next pipeline run starts a new subprocess, so this is a resource leak rather than a correctness issue.
- **Reproduction:** On Windows, trigger a provider Close after the child process has already been killed externally. The Close error is swallowed.

---

### [INFO] JSONL readers: silent JSON parse skips are by design
- **Location:** jsonl.go:56-57, vscode.go:60-61, gemini.go:41-42, claude_tool_fold.go:67-68, protobuf.go:59-60
- **Description:** All JSONL readers silently `continue` on `json.Unmarshal` errors. This is standard JSONL behavior — chat log files frequently contain non-JSON lines (metadata, sentinel records, partial writes). Logging every malformed line would be noisy.
- **Impact:** None — this is the correct approach for JSONL parsing. Malformed lines are not actionable.

---

## Greps Summary

**_ = err patterns (categorized):**
- Cleanup/defer (acceptable): `logger.Close()`, `watcher.Close()`, `lockFile.Close()`, `file.Close()`, `stdin/stdout/stderr.Close()`, `tmp.Close()`, `dir.Close()`, `provider.Close()`
- Process wait (acceptable): `cmd.Wait()` at transport.go:473, opencodehttp.go:153,176
- Meaningful swallows (flagged above): `GlobalOverlayPath()` at stop.go:24, `parseTimestamp()` at opencode.go:95,136,198 and kiro.go:68

**scanner.Err() verification (non-test):**
- Checked ✓: jsonl.go:60, vscode.go:65, gemini.go:63, claude_tool_fold.go:74, protobuf.go:65, openclaudecli.go:140, geminicli.go:133, codexcli.go:157, claudecli.go:124, runs.go:176,214, validation.go:342, audit.go:96
- **Missing ✗**: probe.go `probeJSONLForCWD` (line 135), probe.go `containsDreamerMarker` (line 245)

**rows.Err() verification (non-test):**
- Checked ✓: sqlite.go:75,120, opencode.go:104,144,316,397, kiro.go:75,176
- **Missing ✗**: opencode.go `ReadMessages` message loop (line 182)

**rows.Close() without defer:**
- opencode.go:177,182 — manual Close on error/after loop; `rows.Err()` missing (see finding)

---

# Phase 3: Sandbox Containment & Security

Examined: 2026-05-30
Files: `internal/sandbox/sandbox.go`, `internal/sandbox/linux.go`, `internal/sandbox/linux_bwrap.go`, `internal/sandbox/darwin.go`, `internal/sandbox/darwin_seatbelt.go`, `internal/sandbox/windows.go`, `internal/sandbox/windows_acl.go`, `internal/sandbox/windows_token.go`, `internal/sandbox/windows_job.go`, `internal/sandbox/windows_arm64.go`, `internal/sandbox/seccomp.go`, `internal/analyzer/permission.go`, `internal/web/handlers/fs.go`, `internal/mcpserver/validation.go`, `internal/fsutil/path.go`

---

### [MEDIUM] fs.go: symlink traversal in FSExists endpoint
- **Location:** internal/web/handlers/fs.go:22-31
- **Description:** `FSExists` uses `filepath.Clean` (line 27) but NOT `filepath.EvalSymlinks` before checking containment via `fsPathAllowed`. The `fsPathAllowed` function (line 55) calls `fsutil.PathWithinRoot`, which does a string prefix comparison. A symlink created at an allowed path (e.g., `/project/link -> /etc/ssh`) passes the `Clean`+`PathWithinRoot` check because `/project/link` is under `/project`, but `os.Stat` (line 32) follows the symlink and returns metadata about the target outside the project root. Contrast with `apply.go:255` which correctly uses `fsutil.ResolveSymlinks` before the same containment check.
- **Impact:** An attacker who can create symlinks within a configured project directory can probe whether arbitrary files/directories exist anywhere on the system via the `/api/fs/exists` endpoint. The response includes `exists` and `is_dir` boolean fields, enabling filesystem enumeration. The attack requires write access to a project directory (e.g., via a malicious dependency or build script).
- **Reproduction:** `ln -s /etc/ssh /project/link`, then `GET /api/fs/exists?path=/project/link` returns `{"exists": true, "is_dir": true}`.

---

### [MEDIUM] windows_arm64.go: silent sandbox degradation without user warning
- **Location:** internal/sandbox/windows_arm64.go:15
- **Description:** `Available()` returns `false` on ARM64 Windows due to `unsafe.Pointer` alignment issues in `getLogonSID`. The callers (`Prepare`/`PostStart` in sandbox.go) correctly handle this: `ModeOn` returns an error, `ModeAuto` silently falls through to a no-op. However, there is no user-visible log warning when `ModeAuto` silently degrades. The user configured `sandbox.mode: auto` expecting protection; they get none, and the only indication is the absence of sandbox behavior. The code comment on line 13 explains the limitation, but nothing is emitted at runtime.
- **Impact:** ARM64 Windows users running with the default `ModeAuto` get no sandbox protection and no warning. Providers that check `ShouldUseNative` fall back to policy-only flags (e.g., `--permission-mode plan`), which are weaker than kernel-enforced restrictions. A user who never reads the source code would not know their sandbox is inactive.
- **Reproduction:** Run `dreamer daemon` on ARM64 Windows with default sandbox config. No log line warns that the sandbox is unavailable.

---

### [LOW] darwin_seatbelt.go: hardcoded /private/tmp bypasses TMPDIR containment
- **Location:** internal/sandbox/darwin_seatbelt.go:98
- **Description:** `buildSeatbeltProfile` hardcodes `(subpath "/private/tmp")` as an always-writable path. On macOS, `os.TempDir()` normally returns `/tmp` (which symlink-resolves to `/private/tmp`), so this duplicates the dynamic `WRITABLE_N` entry for the temp dir. However, if the daemon is launched with `TMPDIR` set to a different location (e.g., `$TMPDIR=/Users/foo/tmp`), the profile grants writes to TWO temp locations: the dynamic one from `os.TempDir()` AND the hardcoded `/private/tmp`. An attacker who can write to `/private/tmp` (which is world-writable on macOS) can create files inside the sandboxed process's writable space.
- **Impact:** The sandboxed child process has an extra writable directory that was not intended by the configuration. On multi-user macOS systems, `/private/tmp` is shared, so other users can pre-create files or symlinks there. Low severity because the sandbox's read-only root mount and `(deny file-write*)` base policy limit what can be done.
- **Reproduction:** Set `TMPDIR=/Users/foo/custom-tmp` before launching the daemon. The seatbelt profile allows writes to both `/Users/foo/custom-tmp` and `/private/tmp`.

---

### [LOW] permission.go: DNS rebinding SSRF is documented but architecturally unfixed
- **Location:** internal/analyzer/permission.go:104-131
- **Description:** `validateURL` resolves DNS once at permission time (line 112) and stores the first resolved IP in `ApprovedIP` (line 130). However, the actual HTTP request is made by the external SDK/agent subprocess, which re-resolves the hostname independently. A malicious DNS server with a short TTL can return a safe IP at permission time and a restricted IP (e.g., 169.254.169.254) at dial time. The code documents this limitation (lines 63-68, 107-111) and notes that closing it requires a dreamer-controlled forward proxy — which does not exist.
- **Impact:** An attacker-controlled hostname approved by `validateURL` can be DNS-rebound to target cloud metadata services or internal network endpoints. The `ApprovedIP` field is stored but not consumed by any caller (lines 126-129), making it dead code today.
- **Reproduction:** Set up a DNS server that returns `93.184.216.34` (example.com) on first query and `169.254.169.254` on subsequent queries. Request permission for `http://attacker.example.com/`. Permission is approved; the agent's HTTP request hits the metadata service.

---

### [LOW] mcpserver/validation.go: os.TempDir() follows TMPDIR on all platforms
- **Location:** internal/mcpserver/validation.go:220, internal/sandbox/sandbox.go:273
- **Description:** `ValidateOutputPath` resolves `os.TempDir()` with `filepath.EvalSymlinks` (line 220) and checks containment (line 231). This is correctly mitigated against symlink attacks. However, `os.TempDir()` follows the `TMPDIR` env var (Linux/macOS) or `TMP`/`TEMP` (Windows), so the validation boundary is defined by the caller's environment, not a fixed path. If an attacker controls the environment of the MCP child process (e.g., via a `.env` file or shell injection before process spawn), they can redirect the temp directory to an arbitrary location. The `ValidateOutputPath` check still holds (the path must be under whatever `os.TempDir()` returns), but the "safe zone" is now attacker-controlled.
- **Impact:** Low — the validation is still correct relative to the resolved temp dir. The concern is that the temp dir itself is environment-dependent, so the security boundary shifts with the environment. This is standard behavior but worth noting for defense-in-depth.
- **Reproduction:** Set `TMPDIR=/attacker/controlled` before spawning an MCP child. Finding files are written under `/attacker/controlled/`, which the attacker can read.

---

## Greps Summary

**Available() callers (non-test):**
- `sandbox.go:177` (ShouldUseNative) — returns false, correct ✓
- `sandbox.go:225` (Prepare) — ModeOn errors, ModeAuto no-op, correct ✓
- `sandbox.go:249` (PostStart) — same pattern, correct ✓
- `cliharness/harness.go:147` — uses ShouldUseNative, correct ✓
- No external callers found that ignore the unavailable case.

**filepath.EvalSymlinks / fsutil.ResolveSymlinks usage:**
- Correct (containment-checked): `apply.go:58,102,146,152,255`, `permission.go:247`, `linux.go:42`, `darwin.go:62,103`, `sandbox.go:277,286`, `linux_bwrap.go:395,404,422`, `mcpserver/validation.go:220,227`
- **Missing ✗**: `web/handlers/fs.go:27` — uses `filepath.Clean` only, no EvalSymlinks

**os.TempDir() usage:**
- `sandbox.go:273` (BuildConfig) — writable dir, validated against project overlap ✓
- `linux_bwrap.go:400` (resolveAndValidateWritableDirs) — resolved + EvalSymlinks ✓
- `mcpserver/validation.go:220` — resolved + EvalSymlinks + containment check ✓
- `cliharness/harness.go:166` — writable dir, passed to BuildConfig ✓
- `cmd/daemon.go:501,502` — cleanup patterns, not containment ✓
