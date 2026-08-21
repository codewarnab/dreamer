# Dreamer Security

This document describes the trust model, attack surfaces, and defence-in-depth
layers in the dreamer system.  It is authoritative — treat it as the starting
point for any security-relevant change.

---

## 1. Design Philosophy

Dreamer's security posture follows three principles from
[docs/vision.md](vision.md):

- **Local-first, no cloud backend** — secrets never transit a dreamer-operated
  server.  All analysis happens locally; findings are written to a local file.
- **Read-only by default** — the LLM provider subprocess is given read access
  to the project tree and may not write to it.  This is enforced at two layers:
  the policy layer (permission handler) and the OS sandbox (kernel-level ACLs /
  mount namespaces).
- **Proposes, does not apply** — dreamer writes to `todos.md` in an output
  directory, not inside the project tree.

---

## 2. Trust Boundaries

```
TRUSTED                     PARTIALLY TRUSTED           UNTRUSTED
────────────────────────────────────────────────────────────────
dreamer process             provider subprocess          network
  internal packages         (claude, gemini, codex…)    LLM provider API
  config files              LLM-generated content        chat transcript files
  output/state dirs                                      prompt injection content
────────────────────────────────────────────────────────────────
```

The provider subprocess is "partially trusted" — it is a first-party binary
running under the user's account, but it is driven by LLM output that may
contain adversarial instructions embedded in the analysed chat transcripts.
Once a prompt injection causes the subprocess to deviate from expected
behaviour, it must be treated as untrusted.

---

## 3. Secrets in Chat Transcripts

### 3.1 Redaction pipeline (`internal/analyzer/redaction.go`)

Before sending any transcript content to the LLM provider, dreamer runs a
multi-pattern redactor across the prompt text.  Matched content is replaced
with `[REDACTED:<type>]` markers.

**Built-in patterns:**

| Pattern name | Detects |
|---|---|
| `aws-access-key-id` | `AKIA…`, `ASIA…`, `AIDA…` etc. (20-char) |
| `aws-secret-key` | `aws_secret_key = <40-char>` forms |
| `github-pat` | `ghp_…` and `github_pat_…` tokens |
| `gitlab-pat` | `glpat-…` tokens |
| `jwt` | `eyJ….eyJ….sig` three-part base64 |
| `bearer` | `Bearer <token>` header values |
| `slack-token` | `xox[abps]-…` tokens |
| `pem-private-key` | PEM `BEGIN … PRIVATE KEY` headers |
| `password` | `password: <value>` or `password=<value>` |
| `secret` | `secret: <value>` |
| `api-key` | `api_key: <value>` |
| `token` | `token: <value>` |
| `env-line` | Full environment variable assignment lines containing `SECRET`, `TOKEN`, `KEY`, `PASSWORD`, `CREDENTIAL`, `AUTH`, `PRIVATE`, `API`, `URL`, `URI`, `DSN`, `CONN` |

User-supplied regex strings from `config.yaml` (`redaction.extra_patterns`)
are compiled and appended as `custom` patterns.

### 3.2 Redaction limitations

- **Regex-based, not AST-based** — secrets in unusual formats (e.g. Base64
  without standard prefix, hex-encoded keys) are not matched.
- **Applied only at the Go boundary** — the redactor runs on the prompt text
  before it is handed to the provider process.  It does not intercept secrets
  that the provider reads directly from disk via its own `read_file` tool.
- **`RedactionResult` is logged, not the content** — hit counts per pattern
  are surfaced in the run log so the user can verify that redaction fired.
  Matched content is never recorded.

### 3.3 Provider config-home write gap

The sandbox intentionally makes `~/.claude`, `~/.gemini`, `~/.codex` writable
because the provider CLI needs them to function.  A prompt-injected agent can
overwrite these config files with malicious hooks that affect future sessions
**outside** dreamer.  See the `KNOWN LIMITATION` comment in
`internal/sandbox/sandbox.go` (`Config.WritableDirs` documentation) for the
planned mitigation (per-session config isolation).

---

## 4. Web Dashboard Security (`internal/web/csrf.go`)

The web dashboard is a loopback-only HTTP server.  It enforces two independent
security layers.

### 4.1 DNS-rebinding protection (Layer 1)

All requests (including `GET`) are checked for a loopback `Host` header.
Any request whose `Host` resolves to a non-loopback address is rejected with
`403 Forbidden`.

- Accepted hosts: `127.0.0.1`, `localhost`, `::1`, and any IP in the
  `127.0.0.0/8` range (to support operator-configured non-default loopback
  IPs).
- IPv6 brackets are stripped for comparison.
- The opaque Origin `null` (sandboxed iframes, `file://`) is treated as
  non-loopback and rejected.

This closes the DNS-rebinding **read** surface: even if an attacker resolves
their domain to `127.0.0.1`, the browser sends `Host: attacker.com`, which is
rejected at Layer 1.

### 4.2 CSRF protection (Layer 2)

State-changing methods (`POST`, `PUT`, `DELETE`, `PATCH`) additionally require:

1. **`X-Dreamer-CSRF` header** matching the process-lifetime token minted by
   `MintCSRFToken()` at daemon startup.  Comparison uses `crypto/subtle.ConstantTimeCompare`
   to prevent timing side-channels.
2. **Loopback `Origin` header** (or `Referer` fallback).  A missing `Origin` is
   rejected — it is not silently allowed — to prevent non-browser callers that
   have read the CSRF token from the SPA HTML from bypassing the origin check.

**Token lifecycle:**
- Minted once per process with `crypto/rand` (256 bits → 64 hex chars).
- Embedded in the SPA HTML at page load via a `<meta>` tag.
- Rotates on daemon restart.  The browser tab reloads automatically after a
  restart and receives the new token.
- Never sent to the LLM provider or written to disk.

### 4.3 What the web layer does NOT protect

- **No TLS** — the dashboard is HTTP-only and intended for loopback access.
  Adding TLS for non-loopback setups is out of scope for v1/v1.5.
- **No authentication** — anyone with loopback access can read the dashboard.
  This is intentional for the local-only model; see `docs/vision.md`.

---

## 5. Sandbox Containment

The OS sandbox is the primary kernel-enforced containment mechanism for
provider subprocesses.  See **[docs/SANDBOX.md](SANDBOX.md)** for the full
strategy, platform-by-platform breakdown, and known limitations.

**Summary of what is enforced:**

| Control | Windows | Linux | macOS |
|---------|---------|-------|-------|
| Project dir write block | Kernel ACL (capability SID) | Read-only bind mount (`--ro-bind / /`) | SBPL `(deny file-write*)` |
| Orphan process cleanup | Job Object `KILL_ON_JOB_CLOSE` | `--die-with-parent` + PID namespace | Process group exit |
| Network isolation | ❌ not supported | `--unshare-net` (opt-in) | `(deny network*)` (opt-in) |
| Syscall filter | ❌ | Seccomp BPF (`off`/`minimal`/`full`) | ❌ (SBPL handles this) |
| Resource limits | ❌ | `RLIMIT_AS`, `RLIMIT_NPROC`, `RLIMIT_NOFILE` | ❌ |
| Scheduled-task persistence | ❌ (planned) | ❌ (planned) | ❌ (planned) |

---

## 6. Permission Handler (`internal/analyzer/permission.go`)

The permission handler is the **policy layer** that intercepts tool calls from
the provider before they execute.  It is the second line of defence after the
sandbox.

### Decision rules

| Tool request kind | Decision |
|-------------------|----------|
| `read` | Approved if the resolved, symlink-expanded path is within the project root. Fail-closed: empty root → deny; null bytes → deny; URL schemes → deny. |
| `url` | Approved if `http`/`https` scheme, non-`localhost`, and **all** DNS-resolved IPs are non-restricted. Restricted IP ranges: loopback, link-local, private (RFC 1918), CGNAT (100.64.0.0/10), unspecified, multicast, NAT64 (64:ff9b::/96), 6to4 (2002::/16). |
| `shell` | Approved only when marked read-only by the SDK **and** the raw command text does not match `shellWriteIdiomRE` (catches `tee`, `dd`, `sed -i`, `bash -c`, `perl -i`, `>(...)`, etc.). |
| `mcp` / `custom_tool` | Approved only when `ReadOnly == true`. |
| anything else | Denied with the kind string in the reason. |

### URL check limitations

The URL check is **point-in-time** at permission grant.  The actual HTTP
request is made by the provider subprocess, which re-resolves DNS
independently.  DNS rebinding attacks (short-TTL record switching from safe IP
to restricted IP between permission grant and dial time) are **not prevented**
by the current implementation.  `ApprovedIP` is stored on the decision for a
future dreamer-controlled proxy that could pin the IP at dial time.

---

## 7. Background Job Run Tokens

Background jobs are triggered by OS schedulers (Windows Task Scheduler, Linux
cron/systemd, macOS launchd).  The scheduler invokes:

```
dreamer jobs run <id> --run-token-file <path>
```

The `--run-token-file` argument must point to a file containing a token that
matches `<store_dir>/run.token`.  The runner validates this token before
executing the job.  This prevents:

- Arbitrary job execution via a direct `dreamer jobs run` call from a
  non-scheduler context without the token.
- Unauthenticated execution if the scheduler is misconfigured or hijacked.

---

## 8. Dependency and Binary Supply Chain

- **`-trimpath` builds** — all release binaries are built with `-trimpath` so
  local filesystem paths are not embedded in stack traces.  The binary warns at
  startup if built without it.
- **`make vulncheck`** — runs `govulncheck` against known Go vulnerability
  advisories.  Should be run before every release.
- **`make lint`** — `golangci-lint` with rules including `gosec` for common
  security anti-patterns.
- **Pinned dependencies** — `go.sum` pins all transitive dependency checksums.
  No `go get -u` without a corresponding `make vulncheck` pass.

---

## 8. Daemon Stop-File Handshake

Windows cannot deliver SIGTERM to a detached daemon, and `taskkill /F`
terminates without state flushing. `dreamer stop` therefore writes the
sentinel file `<output_root>/dreamer.daemon.stop`; the daemon polls for it
(500ms), cancels its context, and runs the normal graceful shutdown path.

Security properties:

- The sentinel lives next to `dreamer.daemon.lock` inside `<output_root>`
  and requires the same local access level: any process that could write it
  could equally write or delete the lockfile, so **no new trust boundary** is
  introduced. A cross-user attack would already imply output-root compromise.
- The daemon removes the sentinel before triggering shutdown so an interrupted
  stop cannot poison a future daemon instance.
- Identity verification (PID + live executable image match) still gates the
  whole flow; the handshake is never sent to a PID we cannot attribute to
  dreamer.

---

## 9. Daemon Stop-File Handshake

Windows cannot deliver SIGTERM to a detached daemon, and `taskkill /F`
terminates without state flushing. `dreamer stop` therefore writes the
sentinel file `<output_root>/dreamer.daemon.stop`; the daemon polls for it
(500ms), cancels its context, and runs the normal graceful shutdown path.

Security properties:

- The sentinel lives next to `dreamer.daemon.lock` inside `<output_root>`
  and requires the same local access level: any process that could write it
  could equally write or delete the lockfile, so **no new trust boundary** is
  introduced. A cross-user attack would already imply output-root compromise.
- The daemon removes the sentinel before triggering shutdown so an interrupted
  stop cannot poison a future daemon instance.
- Identity verification (PID + live executable image match) still gates the
  whole flow; the handshake is never sent to a PID we cannot attribute to
  dreamer.

---

## 10. Known Gaps and Planned Mitigations

| Gap | Affected platforms | Planned mitigation |
|-----|-------------------|-------------------|
| Scheduled-task persistence via `schtasks`, `cron`, `launchctl` | All | Shadow-bind binaries to `/dev/null` (Linux/bwrap); SBPL `deny process-exec` for scheduling binaries (macOS); deny `FILE_GENERIC_EXECUTE` on scheduling binaries + AppContainer (Windows) |
| Provider config-home poisoning (`~/.claude`, `~/.gemini`) | All | Per-session config isolation (separate `CLAUDE_HOME`/`GEMINI_HOME` per job); make config dirs read-only for `read_only` profile |
| DNS rebinding at dial time | All | dreamer-controlled forward proxy that re-validates IP at connect time |
| ACP providers: no per-session project write block | All | Per-session sandboxing for ACP providers (project dir unknown at spawn time today) |
| Seccomp on arm64 | Linux arm64 | Add arch-aware syscall number constants; see `internal/sandbox/seccomp.go` contributor notes |
| Windows: network isolation not supported | Windows | No mitigation planned for v1; requires a different mechanism |
| Job Object breakaway via WMI/SCM | Windows | AppContainer (`NtCreateLowBoxToken`) as Layer 3 |

---

## 11. Security-Sensitive Files Reference

| File | What it protects |
|------|-----------------|
| [`internal/sandbox/sandbox.go`](../internal/sandbox/sandbox.go) | Public sandbox API, mode parsing, `BuildConfig` overlap guard |
| [`internal/sandbox/windows.go`](../internal/sandbox/windows.go) | Restricted token, capability SID, `KNOWN LIMITATION` for scheduled tasks |
| [`internal/sandbox/linux_bwrap.go`](../internal/sandbox/linux_bwrap.go) | bwrap argument construction, writable dir validation, `KNOWN LIMITATION` for cron |
| [`internal/sandbox/darwin_seatbelt.go`](../internal/sandbox/darwin_seatbelt.go) | SBPL profile builder, path metacharacter validation, `KNOWN LIMITATION` for launchd |
| [`internal/sandbox/seccomp.go`](../internal/sandbox/seccomp.go) | BPF filter compilation (Linux amd64 only) |
| [`internal/analyzer/redaction.go`](../internal/analyzer/redaction.go) | Secret regex patterns, redaction result counting |
| [`internal/analyzer/permission.go`](../internal/analyzer/permission.go) | Tool call policy, URL SSRF protection, shell write idiom detection |
| [`internal/web/csrf.go`](../internal/web/csrf.go) | CSRF token mint, `CSRFMiddleware` (Layer 1: Host, Layer 2: token+Origin) |

For the full sandbox threat model, platform strategies, and testing
requirements see **[docs/SANDBOX.md](SANDBOX.md)**.

For provider permission handler wiring and ACP session isolation see
**[docs/PROVIDERS.md](PROVIDERS.md)**.
