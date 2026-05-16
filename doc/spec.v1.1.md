# dreamer v1.1 — System Specification

Delta on top of `doc/spec.md` (v1). When v1.1 conflicts with v1, v1.1 wins.
Sections not mentioned here are unchanged.

Status legend: **[locked]** = implemented and shipped. **[provisional]** =
design adopted but pending real-world validation.

---

## 1. Summary

v1.1 adds **Codex** as a first-class analyzer provider platform alongside
Copilot, Claude, Gemini, and Kiro. Two new provider ids ship:

| Platform | Primary (native CLI) | Fallback (ACP)   |
|----------|----------------------|------------------|
| Codex    | `codex-cli`          | `codex-acp`      |

Codex remains a **chat-discovery source** as before (`~/.codex/sessions/**`
+ `~/.codex/archived_sessions/**`). v1.1 keeps that reader untouched.

---

## 2. CLI surface — unchanged

No new flags. `--provider` now accepts `codex-cli` and `codex-acp` in
addition to the v1 ids.

---

## 3. Configuration system

### 3.1 Resolution order — unchanged

### 3.2 Global config schema — additive

```yaml
providers:
  # ... v1 entries unchanged ...

  codex-cli:
    # OpenAI Codex CLI via `codex exec --json --sandbox read-only`.
    command: ["codex", "exec", "--json", "--sandbox", "read-only"]
    # model: ""                # optional --model override; empty = codex default
    # env: {}                  # extra environment for the subprocess

  codex-acp:
    # Codex via an ACP bridge supplied by the operator. The default
    # command is a placeholder; set this to your bridge binary.
    command: ["codex-acp"]
    # env: {}
```

Both blocks honour the v1 `ProviderBlock` schema (§3.2). `model` is read by
`codex-cli`; `command` and `env` are common across all CLI/ACP providers.

### 3.3 Per-project config — unchanged

Project overrides at `<project>/.dreamer/config.yaml` work for the new
provider ids the same way they do for the v1 ids.

### 3.4 Per-category rule config — unchanged

---

## 4. Provider system

### 4.1 Strategy — extended

| Platform | Primary               | Fallback        |
|----------|-----------------------|-----------------|
| Copilot  | `copilot-sdk`         | `copilot-acp`   |
| Claude   | `claude-cli`          | `claude-acp`    |
| Gemini   | `gemini-sdk` → `gemini-cli` | `gemini-acp` |
| Kiro     | `kiro-acp`            | n/a             |
| **Codex** | **`codex-cli`**      | **`codex-acp`** |

Rules from v1 §4.1 still apply: never scrape, never use reverse-engineered
clients. `codex-cli` shells out to the official OpenAI Codex binary;
`codex-acp` delegates to an operator-supplied ACP bridge.

**Out of scope for v1.1.** Cursor remains excluded (same reasoning as v1).
No `codex-sdk` provider — the official Codex SDK targets Python and is not
shipped as a Go module that dreamer can vendor cleanly.

### 4.2 Selection rule — unchanged

If the codex binary or operator-supplied bridge fails its startup check,
dreamer hard-fails with the remediation messages in §13.2 (extended below).

### 4.3 Interfaces — unchanged

Both new providers implement the v1 `analyzer.Provider` and `analyzer.Session`
interfaces unmodified.

### 4.4 ACP adapter — unchanged

`codex-acp` uses the same `internal/analyzer/providers/acpcore/` stdio
JSON-RPC 2.0 transport as the other `*-acp` providers. The wire protocol,
capability set, and permission flow are identical to v1 §4.4.

### 4.5 Read-only permission handler — unchanged

The generic `DecidePermission` handler from v1 (`internal/analyzer/permission.go`)
applies to both new providers. `codex-cli` additionally defaults to
`--sandbox read-only`, giving a second layer of protection at the codex
binary level.

---

## 5. Discovery system — unchanged

Codex chat-source discovery (`~/.codex/sessions/**` + `archived_sessions`)
was already present in v1. No changes.

---

## 6. Redaction — unchanged

---

## 7. Analysis pipeline — unchanged

Both new providers participate in the standard two-phase pipeline (§7.2).
The `codex-cli` provider pre-bakes prompts into one shot per
`session.Run` call, matching the `claude-cli` shape.

---

## 8. Toolchain detection — unchanged

---

## 9. Rule packs — unchanged

---

## 10. Lint-rule allow-list — unchanged

---

## 11. Output — unchanged

---

## 12. State & incremental runs — unchanged

`state.json:provider_usage` gains keys for `codex-cli` and `codex-acp` when
either is used. The struct is open-ended (`map[string]ProviderUsage`), so no
schema change is required.

---

## 13. Auth & error handling

### 13.1 Startup checks — unchanged

### 13.2 Per-provider remediation — extended

`internal/config/providers.go` now ships these additional messages:

- `codex-cli`: `Install the OpenAI Codex CLI (\`npm i -g @openai/codex\` or platform installer) and run \`codex login\`.`
- `codex-acp`: `Install an ACP bridge for Codex (e.g. set \`providers.codex-acp.command\` to your bridge binary) and verify it speaks JSON-RPC 2.0 over stdio.`

### 13.3 Daemon mode — unchanged

### 13.4 Mid-run errors — unchanged

---

## 14. Daemon — unchanged

---

## 15. Logging — unchanged

---

## 16. Directory & file layout — additive

```
internal/analyzer/providers/
  codexcli/                  # codex-cli implementation
    codexcli.go
  codexacp/                  # codex-acp wrapper around acpcore
    codexacp.go
```

`cmd/root.go` blank-imports both packages alongside the v1 providers so the
factory registry picks them up at process start.

---

## 17. End-to-end run sequence — unchanged

---

## 18. Provider-specific notes

### 18.1 `codex-cli` [locked]

- **Command shape (default):** `codex exec --json --sandbox read-only --cd <project> [--model <m>]`
- **Stdin:** the orchestrator writes the full prompt body (system message
  + phase-1 or phase-2 template) to stdin and closes it. Codex consumes
  stdin as the user message for the non-interactive turn.
- **Stdout:** JSON Lines per the `codex exec --json` schema. dreamer parses
  events tolerantly and accumulates text from any of:
  - `msg.type ∈ {agent_message, assistant_message, message, task_complete}` → wins as final text
  - `msg.type ∈ {agent_message_delta, assistant_message_delta}` → appended into a streaming buffer
  - Body fields tried in order: `msg.message`, `msg.text`, `msg.delta`, `msg.content` (string or `[{type, text}]` array)
- **Sandboxing:** `--sandbox read-only` is the default. Operators can
  override `command` to switch to `workspace-write` if they accept the
  trade-off, but dreamer's read-only permission handler (§4.5) still
  rejects any write/exec request that escapes the project root.
- **Auth:** standard `codex login` (OAuth) or `OPENAI_API_KEY` env. Failures
  surface during the first `session.Run` call.

### 18.2 `codex-acp` [provisional]

- **Command shape (default):** `["codex-acp"]` — placeholder. The OpenAI
  `codex` binary does not currently ship an ACP server. Operators must
  point `providers.codex-acp.command` at a bridge they trust.
- **Wire protocol:** identical to v1 §4.4 (stdio JSON-RPC 2.0,
  `initialize` + `session/new` + `session/prompt` + `session/end` +
  `shutdown`). The shared `acpcore` client drives the transport; the
  `codexacp` package only supplies the spawn command.
- **When to graduate to [locked]:** the OpenAI Codex CLI ships a first-party
  ACP server, **or** an open-source ACP bridge for Codex reaches a stable
  v1 release that we can recommend by name.

---

## 19. Open issues

### I1 — Copilot SDK session timeout — unchanged

### I2 — Codex JSON event schema drift [provisional]

**Symptom.** `codex-cli: no assistant content emitted` despite the codex
process exiting cleanly.

**Cause.** The `codex exec --json` event stream is not yet versioned. Future
codex releases may rename event types (e.g. `agent_message` →
`assistant_response`). The tolerant parser in §18.1 keeps us afloat for
most schema drift but cannot recover from a complete rename.

**v1.1 mitigation.**

1. Log the raw codex stderr alongside the empty-output error.
2. Track upstream codex releases; add a new event type to the parser
   accumulator when a drift is detected.

**Owner.** Implementer of the codex provider work.

---

## 20. Acceptance criteria — additive

A v1.1 build is acceptable when, on this repository, in addition to v1
§20.1–9:

10. `dreamer analyze --path /home/ani/dev/fun/dreamer --provider codex-cli`
    writes ≥ 1 finding to `<UserConfigDir>/dreamer/dreamer/todos.md`, with
    each finding citing at least one real symbol from the repo.
11. Re-running the same command immediately produces zero new findings and
    zero provider calls (cache hit), matching the v1 §20.2 behaviour.
12. `dreamer analyze --path /home/ani/dev/fun/dreamer --provider codex-acp`
    against an operator-supplied bridge produces the same output shape with
    no source changes (provisional pending a recommended bridge).
13. `dreamer analyze` against a project whose configured provider is
    `codex-cli` and the codex binary is missing exits `1` with the
    remediation message from §13.2 on stderr and full detail in the log.

---

## 21. Glossary — additive

- **Codex CLI** — the official OpenAI Codex command-line tool
  (`codex exec`, `codex login`, `codex mcp-server`, …).
- **ACP bridge** — a third-party stdio JSON-RPC 2.0 server that translates
  between an upstream agent's native protocol and the Agent Client Protocol
  used by dreamer's `acpcore` transport.
