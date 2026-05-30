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

---

# Phase 4: Web Handler Input Validation

Examined: 2026-05-30
Files: `internal/web/handlers/jobs.go`, `internal/web/handlers/settings.go`, `internal/web/handlers/lifecycle.go`, `internal/web/handlers/chats.go`, `internal/web/handlers/events.go`, `internal/web/handlers/fs.go`, `internal/web/handlers/dashboard.go`, `internal/web/handlers/findings.go`, `internal/web/handlers/history.go`, `internal/web/handlers/logs.go`, `internal/web/handlers/projects.go`, `internal/web/handlers/providers.go`, `internal/web/handlers/settings.go`, `internal/web/csrf.go`

---

### [MEDIUM] chats.go: missing http.MaxBytesReader on DELETE handlers
- **Location:** internal/web/handlers/chats.go:186, 238
- **Description:** `deleteProjectChat` (line 186) and `bulkDeleteProjectChats` (line 238) decode JSON bodies via `json.NewDecoder(r.Body)` without applying `http.MaxBytesReader`. All other body-reading handlers (JobCreate, JobEdit, JobPreview at jobs.go:402,532,625; settingsPut at settings.go:101) enforce a body cap. The chat DELETE handlers accept `{"path": "..."}` or `{"paths": [...]}`, which are small by design, but a loopback caller could stream a multi-gigabyte payload to exhaust daemon memory.
- **Impact:** OOM on the daemon process from an authenticated loopback caller. Low practical risk (CSRF + loopback required), but inconsistent with the pattern in all other mutation handlers.
- **Reproduction:** `curl -X DELETE http://localhost:7777/api/projects/test/chats -H 'Content-Type: application/json' -d '{"path":"'$(python -c "print('x'*100*1024*1024)")'"}'`

---

### [LOW] lifecycle.go: hash parameter not validated as hex
- **Location:** internal/web/handlers/lifecycle.go:123
- **Description:** The hash extracted from the URL path is lowercased with `strings.ToLower(hash)` but never validated as a hex string. `parseProjectHashTransition` (line 46) only checks path shape (6 segments), not hash content. Non-hex values (e.g., `../etc`, Unicode) are harmlessly rejected via map-miss against `st.Findings[hash]`, returning 404. The `findingMarkerRe` in `findings.go:37` only produces hex hashes, so non-hex input can never match a real finding.
- **Impact:** Defense-in-depth gap. Non-hex hashes produce 404 anyway, but explicit validation would give better error messages and reject invalid input at the boundary.

---

### [LOW] events.go: SSE event type field is unsanitized
- **Location:** internal/web/handlers/events.go:48
- **Description:** `fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)` writes `e.Type` directly into the SSE stream without sanitization. If `e.Type` contained a newline, it would inject extra SSE fields. All event types are compile-time constants in `pipeline/events.go:12-27` (e.g., `"run.start"`, `"finding.applied"`) — none contain newlines. The `data` field is JSON-marshaled, so embedded newlines are escaped.
- **Impact:** Currently safe because all event types are constants. If custom event types are ever added from user input, this becomes an injection vector. Defense-in-depth: sanitize `e.Type` by stripping newlines.

---

## Greps Summary

**json.NewDecoder(r.Body) — body size limits:**
- `jobs.go:402` (JobCreate) — MaxBytesReader 64 KiB ✓
- `jobs.go:532` (JobEdit) — MaxBytesReader 64 KiB ✓
- `jobs.go:625` (JobPreview) — MaxBytesReader 64 KiB ✓
- `settings.go:101` (settingsPut) — MaxBytesReader 256 KiB ✓
- **Missing ✗**: `chats.go:186` (deleteProjectChat) — no MaxBytesReader
- **Missing ✗**: `chats.go:238` (bulkDeleteProjectChats) — no MaxBytesReader

**r.URL.Query().Get — query parameter validation:**
- All parameters validated or used as read-only filters ✓

**CSRF on GET endpoints:**
- All GET handlers are read-only, no state changes via GET ✓

---

# Phase 5: Background Jobs State Machine

Examined: 2026-05-30
Files: `internal/backgroundjobs/runner.go`, `internal/backgroundjobs/store.go`, `internal/backgroundjobs/schedule.go`, `internal/backgroundjobs/reconcile.go`, `internal/backgroundjobs/health.go`, `internal/backgroundjobs/audit.go`, `internal/backgroundjobs/job.go`, `internal/backgroundjobs/runs.go`, `internal/web/handlers/jobs.go`

---

### [HIGH] runner.go: run record lost on provider panic
- **Location:** internal/backgroundjobs/runner.go:193-213
- **Description:** The `Run` struct is created with `Status: RunStatusRunning` at line 157 (in memory only), but is not persisted to `RunStore` until line 213 (`e.RunStore.Append(run)`). If `e.executeJob` at line 193 panics, the deferred `release()` at line 180 correctly releases the lock, but lines 196-213 never execute. No run record is written. The audit log has a `job.run.claim` event (line 183) but no `job.run.finish` event.
- **Impact:** Silent data loss. Operators cannot distinguish "job never ran" from "job ran and the provider crashed." The only trace is an audit event with no matching run record.

---

### [HIGH] job.go: RunStatusTimedOut is dead code
- **Location:** internal/backgroundjobs/job.go:60, internal/backgroundjobs/runner.go:200-210
- **Description:** `RunStatusTimedOut` is defined at job.go:60 and listed as a terminal status in tests. The doc comment at runner.go:104 says "Marks run completed/failed/timed_out." However, the error handling at lines 200-210 only distinguishes `ctx.Err() != nil` (→ `RunStatusCancelled`) and all other errors (→ `RunStatusFailed`). No code path ever sets `RunStatusTimedOut`. The timeout is detected as a context cancellation, not a distinct status.
- **Impact:** Monitoring and health checks that key on `RunStatusTimedOut` will never see it. All timeout-caused failures appear as `RunStatusFailed` or `RunStatusCancelled`, making it impossible to distinguish "increase the timeout" from "fix the prompt/provider."

---

### [HIGH] health.go: no timeout vs failure distinction
- **Location:** internal/backgroundjobs/health.go:130-138
- **Description:** `checkJob` inspects `job.LastRunAt` for staleness (>7 days) but does not examine `job.Health.RunState` to distinguish failed from completed from timed-out. Combined with the `RunStatusTimedOut` dead-code issue, timeout and failure are indistinguishable in the health system.
- **Impact:** The health API cannot tell an operator whether a job is failing due to timeouts (increase timeout) vs logic errors (fix prompt). The health check only cares about "did it run recently" and "is the OS schedule installed."

---

### [MEDIUM] runner.go: no panic recovery for OS-triggered runs
- **Location:** internal/backgroundjobs/runner.go:107-238
- **Description:** `Executor.Run` has no `recover()`. A panic in `executeJob` (provider creation, session creation, session.Run) propagates to the caller. The web handler goroutine at jobs.go:997-1004 recovers panics, but the OS-triggered invocation path (via `cmd/jobs.go`) does not. A provider panic in a cron-triggered run crashes the `dreamer jobs run` process.
- **Impact:** OS-triggered runs have no panic safety net. The OS scheduler eventually restarts the process, but the run is silently lost (combined with the run-record-on-panic issue above).

---

### [MEDIUM] schedule.go: parseTimeOfDay error silently defaults to midnight
- **Location:** internal/backgroundjobs/schedule.go:95
- **Description:** In `NextRun`, the `parseTimeOfDay` error is discarded with `_`. The comment at lines 92-94 says "ValidateSchedule rejects invalid TimeOfDay before NextRun is ever called." However, if a store is corrupted or hand-edited with an invalid `TimeOfDay`, `NextRun` silently defaults to hour=0, min=0 (midnight). The same pattern appears in all three platform scheduler builders (Windows, Linux, macOS).
- **Impact:** A corrupted `time_of_day` value silently schedules the job at midnight instead of producing an error. Applies to both NextRun calculations and OS scheduler installation.

---

### [MEDIUM] reconcile.go: ListOwn failure silently skips orphan detection
- **Location:** internal/backgroundjobs/reconcile.go:105-108
- **Description:** If `r.Scheduler.ListOwn(ctx)` fails (e.g., `systemctl` unavailable, `schtasks.exe` permission denied), the error is logged as a warning and the function continues with whatever actions were computed so far. Orphaned OS schedules are silently missed. No error is propagated to the caller.
- **Impact:** Orphan detection is silently skipped on transient OS scheduler failures. `ReconcileResult` shows zero orphans, and `CheckHealth` also misses them.

---

### [MEDIUM] audit.go: AuditWriter lacks cross-process locking
- **Location:** internal/backgroundjobs/audit.go:39-68
- **Description:** `AuditWriter.Write` uses `sync.RWMutex` for in-process serialization but does NOT use file locking (unlike `RunStore.Append` and `Store.Update`). When multiple OS-triggered `dreamer jobs run` processes write audit events concurrently, they append to the same `audit.jsonl` without cross-process coordination. While `O_APPEND` on most platforms provides atomic appends for small writes, this is not guaranteed on all filesystems.
- **Impact:** Corrupted audit log entries under heavy concurrent execution. Most OS filesystems handle small `O_APPEND` writes atomically, but this is platform-dependent.

---

### [LOW] store.go: Store.Load bypasses locks
- **Location:** internal/backgroundjobs/store.go:76-78
- **Description:** `Store.Load()` calls the private `load()` directly without acquiring either the mutex or the file lock. A reader can see a partially-written `jobs.json` if another process is mid-`save()`. Mitigated by `fsutil.WriteFileAtomic` (temp + rename), which provides atomic replacement on most filesystems.

---

### [LOW] schedule.go: weekly schedule silently defaults to Sunday on corrupt data
- **Location:** internal/backgroundjobs/schedule.go:102
- **Description:** `targetDay := validDayOfWeek[strings.ToLower(s.DayOfWeek)]` — if `DayOfWeek` is not in the map, Go returns the zero value `time.Sunday` (0). `ValidateSchedule` catches this, but `NextRun` does not guard against corrupt store data.

---

### [LOW] reconcile.go: disable/remove race on concurrent run
- **Location:** internal/backgroundjobs/reconcile.go:91-99
- **Description:** If a job is disabled while a run is in progress, `ReconcileSchedules` will schedule a `ReconcileRemoveDisabled` action. The `Remove` may succeed while the job is still running. The per-job lock prevents concurrent execution within Dreamer, but the OS schedule removal doesn't check the lock.

---

### [LOW] health.go: RunState not cross-validated against RunStore
- **Location:** internal/backgroundjobs/health.go:94-147
- **Description:** `checkJob` reads `job.Health.RunState` but never verifies it against the actual latest run in the RunStore. If `updateJobAfterRun` fails, `job.Health.RunState` could be stale while the RunStore has a newer record.

---

### [LOW] runner.go: RunState can drift on updateJobAfterRun failure
- **Location:** internal/backgroundjobs/runner.go:358-373
- **Description:** `updateJobAfterRun` sets `j.Health.RunState = run.Status` at line 368. If this `Store.Update` call fails (logged at line 372), the persisted `HealthState.RunState` will be stale. The next successful run corrects it.

---

### [LOW] jobs.go: stale guard entry on theoretical panic
- **Location:** internal/web/handlers/jobs.go:964-982
- **Description:** If `tryAcquireRun` succeeds at line 964 but an unexpected panic occurs before the explicit `releaseRun` calls at lines 972/979, the guard remains locked. There is no `defer releaseRun(jobID)` covering the early error paths. Probability is near-zero.

---

### [LOW] runner.go: stale doc comment re: selected_writes
- **Location:** internal/backgroundjobs/runner.go:100-102
- **Description:** Doc comment says "Rejects selected_writes and full_workspace (only read_only is supported)" but the actual code at lines 324-335 accepts `FileAccessSelectedWrites`. The comment was not updated when `selected_writes` support was added.

---

## Greps Summary

**RunStatus assignments:**
- `RunStatusRunning` — runner.go:157 (initial) ✓
- `RunStatusCompleted` — runner.go:210 (success) ✓
- `RunStatusFailed` — runner.go:168,206 (lock failure or execution error) ✓
- `RunStatusCancelled` — runner.go:203 (context cancellation) ✓
- `RunStatusSkipped` — runner.go:291 (disabled job) ✓
- **Dead code ✗**: `RunStatusTimedOut` — defined at job.go:60, never assigned

**defer release() patterns:**
- `store.go:57,61` — Store lock ✓
- `runs.go:48,52` — RunStore Append lock ✓
- `runs.go:124,128` — RunStore Prune lock ✓
- `runner.go:166,180` — Per-job execution lock ✓
- All acquisitions have matching defer release() ✓

---

# Phase 9: Platform-Specific & Untested Code

Examined: 2026-05-30
Files: `internal/analyzer/providers/cliharness/harness.go`, `cmd/proc_windows.go`, `cmd/proc_unix.go`, `cmd/startup.go`, `cmd/start.go`, `cmd/stop.go`, `cmd/workers.go`, `internal/backgroundjobs/scheduler_darwin.go`, `internal/backgroundjobs/scheduler_linux.go`, `internal/backgroundjobs/scheduler_windows.go`, `internal/fsutil/lock.go`, `internal/fsutil/process.go`, `internal/fsutil/process_other_unix.go`, `internal/fsutil/atomic.go`, `internal/analyzer/redaction.go`, `internal/sandbox/sandbox.go`

---

### [CRITICAL] scheduler_darwin.go: "already bootstrapped" check is dead code
- **Location:** internal/backgroundjobs/scheduler_darwin.go:225-227
- **Description:** `bootstrap()` calls `s.runCmd(ctx, "launchctl", "bootstrap", ...)` which returns `([]byte, error)` via `runExternalCommand` (scheduler.go:70-77). The `[]byte` return (containing stdout+stderr) is discarded with `_` at line 225. The code then checks `strings.Contains(string(err.Error()), "already bootstrapped")`. However, `cmd.Run()` returns an `*exec.ExitError` whose `.Error()` returns only `"exit status 1"` — the actual stderr message ("already bootstrapped") is in the discarded `[]byte` buffer. The check is always false, so the function always returns the error.
- **Impact:** On macOS, calling `Install()` for an already-bootstrapped agent always fails. This breaks idempotent `jobs install` and `jobs reconcile` operations — the first install succeeds, but any subsequent install for the same job fails with "bootstrap: exit status 1".

---

### [HIGH] lock.go: execPathsMatch is case-sensitive on Windows
- **Location:** internal/fsutil/lock.go:136-147
- **Description:** `execPathsMatch` performs a plain `==` comparison after resolving symlinks. On Windows, NTFS is case-insensitive but `QueryFullProcessImageNameW` returns a case-preserved path. If the lock file records `c:\program files\dreamer\dreamer.exe` and the live process reports `C:\Program Files\dreamer\dreamer.exe`, the comparison fails. The lock is then treated as "PID reused by different executable" and removed, allowing a second daemon instance to start. The `fsutil/path.go` correctly handles Windows case-insensitivity in `CanonicalPath` (line 45) and `PathWithinRoot` (line 118), but `execPathsMatch` does not follow this pattern.
- **Impact:** On Windows, a second daemon instance can start concurrently because the lock file from the first instance is incorrectly cleaned up. This breaks the single-instance guarantee.

---

### [HIGH] stop.go/proc_windows.go: PID reuse can kill wrong process; access-denied unhandled
- **Location:** cmd/stop.go:31-44, cmd/proc_windows.go:26-40
- **Description:** Two issues compound: (1) `stop` uses `fsutil.ReadLockPID` which discards the executable path from lock metadata. If the daemon's PID was reused by an unrelated program after a crash, `IsProcessAlive` returns true and `stop` calls `killDaemon(pid)` — killing the unrelated process. (2) `killDaemon` on Windows only checks for "not found" in taskkill output. "Access is denied" (different user session or AppContainer) propagates as a raw error, leaving the daemon running with no programmatic way to stop it.
- **Impact:** PID reuse after a daemon crash can cause `dreamer stop` to kill an unrelated process. Access-denied from a different user session makes the daemon unkillable via CLI.

---

### [HIGH] start.go/workers.go: ReadLockPID discards exec identity
- **Location:** cmd/start.go:45, cmd/workers.go:177-182
- **Description:** Both call sites use `fsutil.ReadLockPID` which explicitly discards the executable path from lock metadata. Consequences: (1) `start.go:45` — if PID was reused by an unrelated program, `IsProcessAlive` returns true and `start` refuses to start, printing "daemon is already running." (2) `workers.go:180` — if the stale PID was reused, `IsProcessAlive` returns true and `recoverStaleJobs` skips reaping, leaving zombie jobs. The lock file already stores the executable path (written at lock.go:47), but `ReadLockPID` discards it.
- **Impact:** PID reuse blocks daemon restart or leaves zombie jobs unrecovered. The fix is to expose a `ReadLockMetadata` function returning both PID and exec path, and use it in start/stop/workers.

---

### [MEDIUM] sandbox.go: PostStartOrKill goroutine leak on stuck processes
- **Location:** internal/sandbox/sandbox.go:311-325
- **Description:** When `PostStart` fails, the code kills the process and spawns a goroutine to wait for `cmd.Wait()`. If the process doesn't exit within 10 seconds (e.g., D-state on Linux, zombie), the goroutine leaks indefinitely. The 10-second timeout only logs a warning; it does not abandon the goroutine. The comment at lines 319-321 acknowledges this: "On POSIX, a zombie or D-state child leaks the goroutine and a process slot for the lifetime of the daemon."
- **Impact:** Under pathological conditions (NFS hang, kernel bug), each failed sandbox start permanently leaks a goroutine and a process slot.

---

### [MEDIUM] redaction.go: env-line pattern over-redacts non-secret variables
- **Location:** internal/analyzer/redaction.go:89
- **Description:** The pattern `(?m)^[A-Z][A-Z0-9_]+\s*=\s*[^\s].+$` matches any line starting with an uppercase identifier followed by `=` and a non-whitespace value. This includes `PATH=/usr/bin:/usr/local/bin`, `LANG=en_US.UTF-8`, `HOME=/root`, `TERM=xterm-256color`, `SHELL=/bin/bash`. Each is replaced with `[REDACTED:env-line]`.
- **Impact:** Legitimate non-secret env vars in chat logs are redacted, reducing debugging usefulness. The pattern cannot distinguish `SECRET_KEY=xxx` from `PATH=/usr/bin`.

---

### [MEDIUM] redaction.go: regexp.Compile on user-supplied patterns
- **Location:** internal/analyzer/redaction.go:44
- **Description:** `NewRedactor` compiles user-supplied regex strings from YAML config via `regexp.Compile`. Go's RE2 engine prevents catastrophic backtracking (ReDoS) during matching. However, there is no complexity or length limit on the pattern, so a very large pattern could cause a slow compile. No `redaction_test.go` file exists.
- **Impact:** Adversarial regex in config could cause slow startup. Runtime matching is safe due to RE2. No tests exist for the redaction package.

---

### [MEDIUM] startup.go: % not escaped in schtasks argument
- **Location:** cmd/startup.go:139-142
- **Description:** `quoteWindowsCommandArgument` only escapes `"`. The `%` character is not escaped. When `schtasks.exe /TR` registers a command, some execution contexts may perform `%VAR%` environment variable expansion. A path containing a literal `%` could be misinterpreted.
- **Impact:** Low probability; affects only Windows startup task registration with `%` in paths.

---

### [MEDIUM] harness.go: zero test coverage for shared lifecycle
- **Location:** internal/analyzer/providers/cliharness/harness.go (entire file, 404 lines)
- **Description:** No test files exist in the `cliharness` package. The individual providers test their `ReadStreamJSON` callbacks, but the shared `Run()` lifecycle — stdin-write goroutine, sandbox integration, error precedence logic, timeout handling — is entirely untested at the unit level. Four providers depend on this code: openai-cli, gemini-cli, codex-cli, copilot-cli.
- **Impact:** Regressions in the shared lifecycle path would not be caught until integration testing. Any change to stdin handling, sandbox config, or error precedence risks breaking all 4 CLI providers simultaneously.

---

### [MEDIUM] process_other_unix.go: darwin/FreeBSD lock cleanup lacks exec identity
- **Location:** internal/fsutil/process_other_unix.go:16-18
- **Description:** On darwin and freebsd, `processExecutable` always returns `("", false)`. The lock acquisition code at lock.go:91-103 falls back to PID-only liveness. After a SIGKILL, if the OS reuses the PID for an unrelated process, the lock file is not cleaned up and the daemon refuses to start.
- **Impact:** On macOS, a daemon crash followed by PID reuse blocks restart until the lock file is manually deleted. The code comments acknowledge this gap.

---

### [LOW] proc_unix.go: no PID validation before negation
- **Location:** cmd/proc_unix.go:15-17
- **Description:** `syscall.Kill(-pid, syscall.SIGTERM)` with PID 0 sends SIGTERM to the entire process group. While `readLockMetadata` validates `pid <= 0` upstream, `killDaemon` itself has no guard. If called directly with PID 0 (e.g., from test code), it would kill all processes in the caller's group.

---

### [LOW] scheduler_*.go: unknown weekday silently defaults to Monday
- **Location:** `scheduler_windows.go:353`, `scheduler_linux.go:266`, `scheduler_darwin.go:700`
- **Description:** All three platform schedulers silently default unknown weekday names to Monday. No error is returned for misspelled or localized day names.

---

### [LOW] atomic.go: glob could match non-dreamer temp files
- **Location:** internal/fsutil/atomic.go:26
- **Description:** The cleanup glob `base + ".tmp*"` matches any file starting with `base.tmp`. If another tool created `config.yaml.tmp_backup` in the same directory, it would be deleted during cleanup. Extremely unlikely in practice.

---

### [LOW] scheduler_darwin.go: redundant string() conversion
- **Location:** internal/backgroundjobs/scheduler_darwin.go:227
- **Description:** `string(err.Error())` — `err.Error()` already returns a `string`; the `string()` conversion is a no-op. The entire check is dead code per the CRITICAL finding above.

---

## Greps Summary

**runtime.GOOS guards:**
- `cmd/startup.go` — guarded by `runtime.GOOS == "windows"` ✓
- `cmd/proc_windows.go` / `cmd/proc_unix.go` — build-tag separated ✓
- `internal/sandbox/windows.go` / `linux.go` / `darwin.go` — build-tag separated ✓
- `internal/fsutil/process_other_unix.go` — build-tag for darwin+freebsd ✓

**os.Executable() callers:**
- `lock.go:47` (write to lock file) — error handled ✓
- `lock.go:91,140` (read/compare) — error handled, falls back to PID-only ✓
- `backgroundjobs/install_id.go:95` — error handled ✓

**regexp.Compile on user input:**
- `redaction.go:44` — user-supplied patterns, RE2 mitigates runtime risk ✓
- No complexity/length limit — defense-in-depth gap noted above
