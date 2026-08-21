# Dreamer Sandbox

The `internal/sandbox` package wraps every LLM provider child process in
OS-level containment so that a compromised or prompt-injected agent cannot
mutate the project tree, exfiltrate secrets through files, or outlive its
session.  This document covers the threat model, the strategy on each
platform, the explicit limits of each approach, and what tests are required
when touching the package.

---

## 1. Threat Model

Dreamer operates in a read-only analysis role — it feeds chat transcripts to a
provider, reads the response, and writes findings to an output directory
outside the project tree.  The LLM provider (claude, gemini, codex, copilot,
…) runs as a subprocess with access to the local filesystem.

### Attacker surface

An adversary who has injected text into a chat transcript that dreamer will
read and send to a provider subprocess can:

- **Write to the project tree** — silently corrupting source files,
  `package.json`, CI YAML, or `git` hooks.
- **Persist across sessions** — installing cron jobs, systemd timers, launchd
  plists, or Windows scheduled tasks that run after dreamer exits.
- **Exfiltrate secrets** — writing API keys, tokens, or private-key material
  captured from the chat transcript (which dreamer redacts but only at the
  Go boundary, not inside the provider process) to a network endpoint or
  to a file reachable by the attacker after the session.
- **Outlive the session** — spawning grandchild processes not attached to the
  sandbox lifecycle, making cleanup impossible.

### Trust boundaries

```
┌────────────────────────────────────────────────────────┐
│ dreamer process (trusted)                              │
│                                                        │
│  ┌──────────────────────────────────────────────────┐ │
│  │ provider child process (untrusted after start)   │ │
│  │                                                  │ │
│  │  Project dir: READ-ONLY (kernel-enforced)        │ │
│  │  Output/Temp/Config dirs: writable               │ │
│  │  Network: open by default, isolatable            │ │
│  │  Scheduled tasks: NOT blocked (known gap)        │ │
│  └──────────────────────────────────────────────────┘ │
│                                                        │
│  Redactor (Go)  →  strips secrets before LLM sees     │
│  Permission handler  →  approves/denies tool calls     │
└────────────────────────────────────────────────────────┘
```

The sandbox is the **last line of defence** after the permission handler layer
(`internal/analyzer/permission.go`).  The permission layer enforces
read-only tool calls at the policy level; the OS sandbox enforces it at the
kernel level.  Neither is sufficient alone.

---

## 2. Sandbox Modes

The sandbox is controlled by `Config.Mode` and the helpers `ParseMode` /
`ShouldUseNative`.

| Mode value | Parsed from | Behaviour |
|------------|-------------|-----------|
| `"auto"` (default) | empty string or `"auto"` | Use OS sandbox when available; fall back to provider-native policy flags otherwise. |
| `"true"` / `"on"` / `"require"` | `"true"`, `"on"`, `"require"` | Require OS sandbox; error if unavailable. |
| `"false"` / `"off"` | `"false"`, `"off"`, `"disable"` | Disable sandbox entirely; user accepts the risk. All other knobs (network, seccomp, resources) are ignored. |

`ShouldUseNative(mode)` returns `true` when the OS sandbox is both available
and not explicitly disabled.  Providers use this to decide whether to pass
unrestricted flags (`--dangerously-skip-permissions`, `--yolo`) or their
native policy-only flags (`--permission-mode plan`, `--sandbox read-only`).

---

## 3. Platform Strategies

Platform backends are selected **at compile time via Go build tags**, not at
runtime.  Each file (`windows.go`, `linux_bwrap.go`, `darwin_seatbelt.go`,
`none.go`) carries a `//go:build` constraint.  Exactly one backend is compiled
into the binary per target OS.

### 3.1 Windows (`windows.go`, `windows_acl.go`, `windows_sid.go`, `windows_token.go`, `windows_job.go`)

**Mechanism:** WRITE_RESTRICTED restricted token + Job Objects.

**How it works:**

1. **`Prepare()` (pre-fork):**
   - Calls `createCapabilitySID(projectDir, ...)` — a unique, project-scoped
     SID derived from the project path, stored in a cache file under `%TEMP%`
     with a configurable expiry (`SIDExpiryDays`, default 7 days).
   - Calls `createRestrictedToken(capSID)` — `CreateRestrictedToken` with
     `WRITE_RESTRICTED | LUA_TOKEN | DISABLE_MAX_PRIVILEGE`.  The kernel
     blocks writes to any object whose ACL does **not** explicitly mention the
     capability SID.
   - Calls `acquireWritableACL(dir, capSID)` for each `WritableDirs` entry —
     appends an Allow-Write ACE for the capability SID.  The release function
     (returned as cleanup) removes the ACE on completion.
   - Attaches the restricted token and `CREATE_NO_WINDOW` to
     `cmd.SysProcAttr`.

2. **`PostStart()` (post-fork):**
   - Creates a Win32 Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`.
   - Assigns the child process to the job.  When dreamer closes the job
     handle (after `cmd.Wait()`), all processes in the tree are terminated.

3. **Cleanup order (mandatory):**
   - Job Object handle closed → triggers `KILL_ON_JOB_CLOSE`.
   - ACL rollback removes the capability SID ACEs from writable dirs.
   - Restricted token closed.
   - **Caller must not call cleanup before `cmd.Wait()` returns.**

**`Available()`:** Always `true` on `windows && !arm64`.

**Network isolation:** NOT supported on Windows.  Setting
`sandbox.network: isolated` returns an error at `prepare()` time.

**What IS sandboxed:**
- File writes to any path not in `WritableDirs` (project dir, arbitrary paths).
- Privilege escalation — `LUA_TOKEN` strips most privileges.

**What is NOT sandboxed (known gaps):**
- **Scheduled task persistence** — `schtasks.exe`, `Register-ScheduledTask`
  (COM via `taskschd.dll`), `at.exe`, WMI `process call create`.  The
  WRITE_RESTRICTED token only restricts writes; it does not block `CreateProcess`
  or COM calls.  See the `KNOWN LIMITATION` comment in `windows.go`.
  Planned mitigations: deny `FILE_GENERIC_EXECUTE` for scheduling binaries,
  then AppContainer (`NtCreateLowBoxToken`) for full isolation.
- **Job Object breakaway** — tools that spawn via WMI or the Service Control
  Manager create new process trees outside the current job.

---

### 3.2 Linux (`linux.go`, `linux_bwrap.go`, `seccomp.go`)

**Mechanism:** bubblewrap (`bwrap`) with user+PID namespace isolation,
read-only root bind mount, optional seccomp BPF filter, optional rlimits.

**Requirements:** `bwrap` must be on `PATH`. Unprivileged user namespaces must
be enabled (`kernel/unprivileged_user_ns_clone ≠ 0`,
`user/max_user_namespaces ≠ 0`). WSL1 is detected and treated as
unavailable (WSL1 cannot create user namespaces).

**`Available()`:** `bwrap` on PATH **and** user namespaces enabled **and** not WSL1.

**`Prepare()` argument construction (`buildBwrapArgs`):**

| bwrap flag | Effect |
|------------|--------|
| `--new-session` | Prevents TIOCSTI terminal injection |
| `--die-with-parent` | Auto-cleans descendants when dreamer dies |
| `--unshare-user` | User namespace (required for unprivileged bwrap) |
| `--unshare-pid` | PID namespace (kills grandchildren on container exit) |
| `--ro-bind / /` | Entire host filesystem read-only (default-deny writes) |
| `--dev /dev` | Minimal device tree |
| `--proc /proc` | PID namespace `/proc` |
| `--size 536870912 --tmpfs /tmp` | 512 MiB in-memory `/tmp` |
| `--symlink /tmp /var/tmp` | `/var/tmp` → sandbox tmpfs (when host `/var/tmp` is a symlink or missing) |
| `--tmpfs /var/tmp` | Fresh tmpfs over `/var/tmp` (when host ships it as a real directory, e.g. containers) |
| `--unshare-net` | Network isolation (only when `cfg.Network == "isolated"`) |
| `--rlimit RLIMIT_AS ...` | Virtual memory cap (bwrap ≥ 0.12.0 only) |
| `--rlimit RLIMIT_NPROC ...` | Process count cap |
| `--rlimit RLIMIT_NOFILE ...` | File descriptor cap |
| `--seccomp <fd>` | BPF filter (written to memfd, passed as fd) |
| `--bind <wdir> <wdir>` | Writable bind mount for each entry in `WritableDirs` |
| `--chdir <projectDir>` | CWD pinned inside sandbox |

**Seccomp profiles** (`seccomp.go`, amd64-only):

| Profile | Rules |
|---------|-------|
| `off` | No filter |
| `minimal` (default) | Block `ptrace` (prevents child→child inspection) |
| `full` | Block `ptrace` + privilege escalation syscalls |

Seccomp on arm64 is not yet implemented; see the contributor notes at the top
of `seccomp.go` for guidance.  The stub `seccomp_stub.go` is compiled on all
other platforms and returns a no-op.

**Resource limits (`ResourceLimits`):**

| Field | Default | Min | Kernel mechanism |
|-------|---------|-----|-----------------|
| `MemoryMB` | 2048 | 64 | `RLIMIT_AS` |
| `Processes` | 256 | 1 | `RLIMIT_NPROC` |
| `FDs` | 256 | 16 | `RLIMIT_NOFILE` |

Note: `RLIMIT_NPROC` is per-UID, not per-process.  It counts all processes
running as the current user, including the dreamer daemon, web server, and all
provider siblings.  256 provides headroom for multi-provider analysis runs.

**What IS sandboxed:**
- All file writes to the project dir and any path not in `WritableDirs`.
- Network access (when `network: isolated`).
- Ptrace-based inspection of sibling provider processes.
- Process tree lifecycle — all children killed when bwrap exits.
- Resource exhaustion (memory, processes, file descriptors).

**What is NOT sandboxed (known gaps):**
- **Scheduled task persistence** — `crontab -e`, `at`, `systemctl --user enable`.
  bwrap mounts the host filesystem read-only but does not filter `execve`.
  The child can execute any scheduling binary visible through the read-only
  root.  Planned mitigation: shadow-bind scheduling binaries to `/dev/null`
  (`--ro-bind /dev/null /usr/bin/crontab`, etc.).  See the `KNOWN LIMITATION`
  comment in `linux_bwrap.go`.
- **`/tmp` paths in `WritableDirs`** — paths under `/tmp` or `/var/tmp` are
  silently skipped in `buildBwrapArgs`; they are already covered by the
  in-sandbox tmpfs and a `--bind` would override it.

---

### 3.3 macOS (`darwin.go`, `darwin_seatbelt.go`)

**Mechanism:** `sandbox-exec(1)` with a dynamically generated SBPL profile.

> **⚠ Deprecation warning:** `sandbox-exec` is deprecated by Apple and may
> be removed in a future macOS version.  If that happens, this backend must
> gracefully fall back to provider-native policy flags (ModeAuto behaviour).
> The `--seatbelt-profile` flag used here is not part of the public
> `sandbox-exec` API.

**`Available()`:** `/usr/bin/sandbox-exec` exists on disk.

**SBPL profile structure (`buildSeatbeltProfile`):**

```scheme
(version 1)
(allow default)           ; permissive base — only explicit denials take effect

;; Network isolation (only when cfg.Network == "isolated")
(deny network*)
(deny network-outbound)
(deny network-inbound)

;; Deny all file writes (default-deny for mutation)
(deny file-write*)
(deny file-link)          ; prevents hard-linking project files into writable dirs

;; Re-allow writes to parameterised writable directories
(allow file-write*
  (subpath (param "WRITABLE_0"))
  (subpath (param "WRITABLE_1"))
  ...
  (subpath "/private/tmp")
  (literal "/dev/null")
  (literal "/dev/stdout")
  (literal "/dev/stderr"))
```

Parameters are passed as `-D WRITABLE_N=<path>` flags to `sandbox-exec`.
The profile is generated at session time and is not written to disk.

SBPL path metacharacter validation (`validateSBPLPath`) rejects paths
containing `(`, `)`, `"`, `\`, `\n`, `\r`, `;`, or `\x00` to prevent profile
injection.

**What IS sandboxed:**
- File writes (and hard-link creation) to any path not in `WritableDirs`.
- Network access (when `network: isolated`).

**What is NOT sandboxed (known gaps):**
- **Scheduled task persistence** — `launchctl load`, `crontab -e`, `at`,
  `osascript` Calendar automation.  The base `(allow default)` permits
  process execution.  Planned mitigation: SBPL `(deny process-exec ...)` for
  scheduling binaries.  See the `KNOWN LIMITATION` comment in
  `darwin_seatbelt.go`.

---

### 3.4 Other Platforms (`none.go`)

`Available()` returns `false`.  `prepare`, `postStart`, `postStartWithHandle`
are all no-ops.  On `ModeAuto`, providers fall back to their own policy flags.
On `ModeOn`, `Prepare` and `PostStart` return an error.

Windows ARM64 (`windows_arm64.go`) also uses the `none.go` fallback — the
restricted-token / Job Object implementation has not been validated on ARM64.

---

## 4. Public API

```
sandbox.Prepare(cmd, cfg)              → cleanup func, error   (pre-fork)
sandbox.PostStart(cmd, cfg)            → cleanup func, error   (post-fork)
sandbox.PostStartWithHandle(cmd, cfg)  → handle, cleanup, error  (post-fork, Windows Job Object)
sandbox.PostStartOrKill(...)           → cleanup func, error   (error → kill+wrap)
sandbox.PostStartWithHandleOrKill(...) → handle, cleanup, error  (error → kill+wrap)
sandbox.BuildConfig(projectDir, providerHome, rawMode) → Config, error
sandbox.ShouldUseNative(mode)          → bool
sandbox.Available()                    → bool  (build-tag-selected)
```

**Lifecycle contract** (mandatory for every CLI provider):

```
prepareCleanup, err := sandbox.Prepare(cmd, cfg)
// ... set up pipes ...
cmd.Start()
jobHandle, postCleanup, err := sandbox.PostStartWithHandleOrKill(cmd, cfg, stdin, stdout, id)
// ... run session ...
cmd.Wait()
postCleanup()      // Job Object handle closed → KILL_ON_JOB_CLOSE fires
prepareCleanup()   // token closed, ACLs rolled back
```

Use `PostStartOrKill` / `PostStartWithHandleOrKill` — they close pipes and kill
the child on error, which is the identical pattern across all CLI providers and
is easy to get wrong manually.

**`BuildConfig` overlap guard:** `BuildConfig` symlink-resolves both the
project dir and each writable dir and returns an error if any writable dir
contains or is contained by the project dir.  This prevents accidental
`WritableDirs` entries that would make the project tree writable.

---

## 5. Provider-Sandbox Interaction

### CLI providers (claudecli, openclaudecli, geminicli, codexcli)

CLI providers spawn a **short-lived** child process per `Session.Run()`.
The sandbox lifecycle matches the process lifetime:

1. `cliharness.NewSession` resolves `sbMode` and builds `sandbox.Config` with
   `{ProjectDir: workingDir, WritableDirs: [tmpDir, configDir, ...]}`.
2. `cliharness.Session.Run()` calls `sandbox.Prepare(cmd, cfg)` before
   `cmd.Start()`, then `sandbox.PostStartWithHandleOrKill(...)` after.
3. `defaultCommand(useNativeSandbox bool)` — when `useNativeSandbox` is true,
   the provider binary is invoked with unrestricted flags
   (`--dangerously-skip-permissions`, `--yolo`, `--full-auto`).  When false,
   provider-native read-only policy flags are used instead.

### ACP providers (claudeacp, geminiacp, codexacp, copilotacp, kiroacp)

ACP providers spawn a **long-lived** child process at `provider.Start()`.
Because the project directory is not known at spawn time (it is received
per-session in `session/new`), the sandbox `ProjectDir` is empty — the
kernel-level write block does **not** cover the project tree for ACP
providers.  Project write protection relies entirely on the permission handler
(`translatePermissionRequest` → `analyzer.DecidePermission`).

Privilege stripping (WRITE_RESTRICTED token) and orphan cleanup (Job Object)
are still applied at spawn time.

### Node.js extra writable dirs

Node.js-based CLIs (claude, openclaude, gemini, copilot, codebuff) require
additional writable directories on Windows to prevent `STATUS_HEAP_CORRUPTION`.
`cliharness.nodeJSExtraDirs` detects the binary name and appends them.

---

## 6. Configuration Reference

```yaml
# per-provider in config.yaml (or global under sandbox:)
sandbox:
  mode: auto          # auto | true | false
  network: open       # open | isolated
  seccomp: minimal    # off | minimal | full  (Linux/amd64 only)
  memory_mb: 2048     # virtual memory cap (RLIMIT_AS)
  processes: 256      # process count cap (RLIMIT_NPROC)
  fds: 256            # file descriptor cap (RLIMIT_NOFILE)
```

All fields default to safe values when absent.  `processes` and `fds` have
minimums enforced by `ResourceLimits.Validate()`.

---

## 7. Testing Approach

The test suite is split by build constraint so each platform tests its own
backend without stub confusion.

| Test file | What it covers | Platform |
|-----------|---------------|----------|
| `sandbox_test.go` | ParseMode, ParseNetwork, ParseSeccomp, ResourceLimits.Validate, BuildConfig, ModeOff no-ops, ShouldUseNative | Cross-platform (pure logic) |
| `sandbox_windows_test.go` | Restricted token creation, ACL grant/revoke, Job Object assignment | Windows (requires admin) |
| `linux_test.go` | `buildBwrapArgs`, `validateWritableDir`, `resolveAndValidateWritableDirs`, seccomp BPF compilation, WSL1/user-namespace detection | Linux |
| `linux_integration_test.go` | Real `bwrap` process — verify writes fail in project dir, succeed in writable dirs | Linux (requires bwrap in PATH) |
| `darwin_seatbelt_test.go` | `buildSeatbeltProfile`, `validateSBPLPath`, `buildSandboxArgs` | macOS |
| `darwin_integration_test.go` | Real `sandbox-exec` process — verify deny file-write | macOS |
| `sandbox_unavailable_test.go` | ModeOn errors when !Available() | Non-Windows |

### Rules for new sandbox tests

1. **Always test the `ModeOff` early-return path** — it must be a no-op even
   with a nil `cmd`.
2. **Gate platform-specific tests on `Available()`** — call `t.Skip` when the
   backend is unavailable rather than faking it.
3. **Use `t.TempDir()` for project dirs** in integration tests; never write to
   a real project path.
4. **Call cleanup in the right order** — `postCleanup()` before
   `prepareCleanup()`, and neither before `cmd.Wait()`.
5. **Do not rely on `RLIMIT_NPROC` being per-process** — tests that verify
   the process count cap must account for the per-UID semantics.
6. **Reset `bwrapCached` / `bwrapChecked`** by calling
   `sandbox.ResetBwrapPathCache()` between tests that modify `PATH`.

### Adding a new platform backend

The public API in `sandbox.go` requires no changes.  Create a new file with
the appropriate `//go:build` constraint and implement exactly four symbols:

```go
func Available() bool
func prepare(cmd *exec.Cmd, cfg Config) (cleanup func(), err error)
func postStart(cmd *exec.Cmd, cfg Config) (cleanup func(), err error)
func postStartWithHandle(cmd *exec.Cmd, cfg Config) (uintptr, func(), error)
```

`postStartWithHandle` should return `0` for the handle on platforms without
Job Objects.  See the sketch at the top of `sandbox.go` for the planned Linux
secondary-backend build-tag pattern.
