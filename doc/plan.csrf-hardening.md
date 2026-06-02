# CSRF System Hardening Plan

Status: implemented
Scope: `internal/web/` (CSRF middleware, server wiring, templates, tests)
Author: analysis pass, June 2026
Reference design rules: `.claude/skills/dreamer-design/SKILL.md`

## Background

The web UI protects state-changing requests with a custom-header + Origin-pinning
scheme: a per-process 256-bit token (`MintCSRFToken`) is rendered into the SPA shell
and echoed back in the `X-Dreamer-CSRF` header, while `CSRFMiddleware` additionally
requires a loopback `Origin`/`Referer`. The scheme is sound and well-tested for
state-changing requests. This plan addresses gaps found during a deep review,
ordered by severity.

Verified-good (no action needed): every mutating frontend call sends the header;
every write handler independently guards its HTTP method; SSE type is CR/LF
sanitized; opaque origins are rejected; token compare is constant-time.

---

## Items

### 1. [HIGH] Add `Host` header validation to close the DNS-rebinding read surface

**Problem.** `csrf.go` claims the Origin check defends against DNS rebinding, but
GET requests bypass the middleware entirely and nothing validates the `Host`
header. Under a rebind (`attacker.com -> 127.0.0.1`), the attacker page becomes
same-origin and can read unauthenticated GET endpoints: `/api/logs/tail`,
`/api/projects`, `/api/projects/{name}/findings`, `/api/projects/{name}/history`,
and the `/api/events` SSE stream. (`/api/settings` is already redacted.) The
existing comment therefore overstates the protection actually provided.

**Fix.**
- In `CSRFMiddleware`, validate `r.Host` against `isLoopbackHost` for **all**
  methods (including GET), before the method switch. Reject non-loopback Host
  with `403`.
- Strip the port from `r.Host` before the hostname check (reuse the same parsing
  approach as the Origin check; `net.SplitHostPort` with a fallback for the
  no-port case).
- Update the misleading comment so it accurately describes Host + Origin layering.

**Acceptance.**
- A GET with `Host: attacker.com` returns 403.
- A GET with `Host: 127.0.0.1:7777` (and `localhost`, `[::1]`) passes.
- `isLoopbackHost` accepts the full `127.0.0.0/8` block (e.g. `127.0.0.2`), matching
  `config.validateWebHost`, so any operator-accepted `web.host` cannot be rejected at
  request time and silently 403 the UI.
- All existing CSRF tests still pass.

**Design rules:** #4 Fail Fast at boundaries, #8 Design by Contract, #1 accurate comments.

---

### 2. [MEDIUM] Add missing test cases (tests as living documentation)

**Problem.** Unit tests only exercise `POST`. No coverage for `PUT`/`DELETE`
(the methods settings-write and deletes actually use), the `HEAD`/`OPTIONS`
bypass branch, or the new Host check.

**Fix.** Add to `csrf_test.go`:
- `PUT` and `DELETE` accepted with valid token + loopback Origin.
- `HEAD` and `OPTIONS` bypass (no token) -> pass through.
- Host-header table: loopback hosts pass, non-loopback Host rejected (covers item 1).
- Optional: a full-stack test through `Server.routes()` confirming a write is
  rejected without a token.

**Acceptance.** New tests pass; `go test ./internal/web/` green.

**Design rules:** Tests as Living Documentation.

---

### 3. [LOW] Extract the CSRF header name into a shared Go constant (DRY)

**Problem.** `"X-Dreamer-CSRF"` is a literal in `csrf.go` and repeated across
templates. The protocol contract is duplicated *knowledge*.

**Fix.**
- Add `const csrfHeader = "X-Dreamer-CSRF"` in `csrf.go` and use it in the
  middleware.
- Templates cannot import the Go const; leave them as-is but add a one-line
  comment in `csrf.go` noting the templates must match this header name.

**Acceptance.** No behavior change; `go build`/tests green.

**Design rules:** #6 DRY (duplication of knowledge).

---

### 4. [LOW] Name the token-size magic number

**Problem.** `make([]byte, 32)` is an unnamed magic number.

**Fix.** `const csrfTokenBytes = 32 // 256-bit CSRF token` and use it in
`MintCSRFToken`.

**Acceptance.** No behavior change.

**Design rules:** Magic Numbers rule.

---

### 5. [LOW] Document token-lifetime / rotation tradeoff

**Problem.** The token is valid for the whole daemon process lifetime and never
rotates. Low risk on loopback, but undocumented.

**Fix.** Add a comment on `MintCSRFToken` / the `csrfToken` field stating the
token rotates only on process restart and why that is acceptable for the
loopback-only v1.5 model.

**Acceptance.** Comment only; no behavior change.

**Design rules:** #1 Document intent, #7 record the *why*.

---

### 6. [INFO] CSP `unsafe-inline`/`unsafe-eval` tradeoff (no change this pass)

Any XSS would read the meta-tag token and defeat CSRF. Already acknowledged in
`server.go`'s CSP comment as an accepted v1.5 tradeoff pending Alpine CSP build
migration. Tracked here for visibility; **out of scope** for this plan.

---

## Suggested execution order

1. Item 1 (Host check + comment) — the only behavior/security change.
2. Item 2 (tests) — lock in item 1 and fill coverage gaps.
3. Items 3, 4, 5 (constants + comments) — mechanical cleanups.

Single PR is fine; items 3-5 are trivial and low-risk. Run
`go test ./internal/web/...` after each step.

## Out of scope

- Token rotation mechanism (only documentation, per item 5).
- CSP tightening / Alpine CSP-build migration (item 6).
- Changing the scheme to double-submit cookies — the current header+Origin model
  is appropriate for a same-origin loopback SPA.
