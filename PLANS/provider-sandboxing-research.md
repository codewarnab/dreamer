# Provider Sandboxing & Read-Only Safety Report

> Generated 2026-05-20 by parallel research across 6 provider codebases + web docs.

---

## Executive Summary

| Provider | Read-Only Flag | OS Sandbox | Tool Whitelist | Network Isolation | Safety Rating |
|----------|---------------|------------|----------------|-------------------|---------------|
| **Codex** | `--sandbox read-only` | bubblewrap + Landlock + seccomp (Linux), Seatbelt (macOS), restricted tokens (Windows) | No (OS-level) | Full (`--unshare-net`) | **Best** |
| **Claude Code** | `--permission-mode plan` + `--tools "Read,Grep,Glob"` | bubblewrap (Linux), Seatbelt (macOS), none (Windows) | Yes (`--tools`) | Proxy allowlist | **Strong** |
| **OpenClaude** | `--permission-mode plan` + `--tools "Read,Grep,Glob"` | Same as Claude Code | Yes (`--tools`) | Proxy allowlist | **Strong** (but has known sandbox bypass CVE) |
| **Gemini CLI** | `--approval-mode plan` | Docker, gVisor, Seatbelt, icacls (Windows) | Policy Engine (TOML) | Seatbelt proxy only | **Strong** (but headless auto-exits to YOLO) |
| **OpenCode** | `permission: { "*": "deny" }` in config | **None** | Yes (permission config) | **None** | **Weak** |
| **Codebuff** | N/A (HTTP API is text-only) | **None** | Yes (`toolNames`) | **None** | **N/A** (current approach is safe) |

---

## Codex (OpenAI)

### Read-Only Mechanism
- **Flag**: `--sandbox read-only` (CLI) or `sandbox_mode = "read-only"` (config.toml)
- **What it does**: OS-level enforcement — model-generated commands **cannot write anywhere**, including `/tmp`. Reads allowed. The sandbox actively blocks write syscalls at the kernel level. Not a warning; hard rejection.

### Sandboxing
- **OS-level (Linux)**: bubblewrap (`bwrap`) for filesystem namespace isolation + Landlock LSM for filesystem access control + seccomp-bpf for network syscall filtering. `PR_SET_NO_NEW_PRIVS` prevents privilege escalation via setuid binaries. User/PID/network namespace isolation. Root filesystem mounted read-only via `--ro-bind / /` with selective writable overlays.
- **OS-level (macOS)**: Apple Seatbelt (`/usr/bin/sandbox-exec`) with deny-by-default policy. `.git/` and `.codex/` automatically protected as read-only within writable roots.
- **OS-level (Windows)**: Restricted process tokens via `codex-windows-sandbox` crate. Separate execution identities, OS-level network isolation, DPAPI-scoped credentials.
- **Language-level**: Process hardening runs before `main()` — anti-debug (`PT_DENY_ATTACH` on macOS, `PR_SET_DUMPABLE=0` on Linux), core dump prevention (`RLIMIT_CORE=0`), environment sanitization (removes `DYLD_*`/`LD_*` vars).
- **Tool execution sandbox**: Main Codex process runs **unsandboxed**. Only shell commands spawned for the AI are sandboxed. Each tool call spawns a child process inside the sandbox boundary.
- **Network sandbox**: `--unshare-net` completely isolates network in read-only mode. No DNS, no outbound connections.
- **Child process isolation**: Yes — child processes inherit sandbox boundaries. `--unshare-pid` prevents inside processes from seeing/signaling outside. `PR_SET_NO_NEW_PRIVS` prevents privilege escalation on exec.

### Tool Restrictions
- **Can disable tools**: No granular tool allowlist/denylist. The sandbox operates at the OS level, not tool level.
- **Dangerous tools**: `shell` (arbitrary commands), `apply_patch` (file editing), MCP tools. In `read-only` mode, writes from all of these are blocked by the OS sandbox.
- **Recommended restriction**: `--sandbox read-only` with `--ask-for-approval never`

### Command for Dreamer
```bash
codex exec --sandbox read-only --ask-for-approval never "your prompt here"
```

### Gaps / Risks
1. **Main process is unsandboxed**: Only child processes are sandboxed. If Codex's own code has a vulnerability, sandbox doesn't protect.
2. **CVE-2025-59532** (patched in 0.39.0): Model-generated `cwd` could bypass workspace boundary. **Ensure Codex >= 0.39.0**.
3. **`--full-auto` override**: `--full-auto` silently upgrades sandbox to `workspace-write`. Never combine these flags.
4. **`dangerously-bypass-approvals-and-sandbox`**: This flag disables ALL sandboxing. Ensure dreamer invocation is hardcoded with safe flags.

---

## Claude Code (Anthropic)

### Read-Only Mechanism
- **Flag**: `--permission-mode plan` (permission layer) + `--tools "Read,Grep,Glob"` (tool-level)
- **What it does**: `plan` mode blocks write tool calls at the permission layer. `--tools` removes write-capable tools from the model's context entirely — the model cannot even attempt actions that don't exist in its tool schema. This is the strongest defense.

### Sandboxing
- **OS-level**: macOS uses Seatbelt; Linux/WSL2 uses bubblewrap. **Windows: no sandbox** (planned).
- **Language-level**: None.
- **Tool execution sandbox**: Bubblewrap/Seatbelt creates an OS-level sandbox for Bash subprocesses only. Read/Edit/Write tools use the permission system directly, NOT the sandbox.
- **Network sandbox**: Proxy-based domain allowlist. Does NOT inspect TLS — domain fronting possible.
- **Child process isolation**: Yes — all child processes spawned by Bash inherit sandbox boundaries.

### Tool Restrictions
- **Can disable tools**: Yes, via `--tools` (whitelist) and `--disallowedTools` (denylist).
- **`--tools` flag**: Removes tools from model context entirely. Use `"Read,Grep,Glob"` for read-only.
- **`--disallowedTools` flag**: Deny rules. **KNOWN BYPASS**: `disallowedTools` only blocks named tools. If `Bash` remains available, model can modify files via `sed -i`, `echo >`, `cat >`, `tee`, `perl -i`, `python -c`, etc. ([issue #31292](https://github.com/anthropics/claude-code/issues/31292)).
- **Dangerous tools**: `Write`, `Edit`, `NotebookEdit`, `Bash`, `WebFetch` (exfiltration), MCP tools.

### Command for Dreamer
```bash
claude -p \
  --permission-mode plan \
  --tools "Read,Grep,Glob" \
  --strict-mcp-config '{}' \
  --no-session-persistence \
  --bare \
  "your prompt here"
```

### Gaps / Risks
1. **Bash bypass of disallowedTools**: Even with Write/Edit denied, Bash can modify files via shell redirects. Mitigated by using `--tools` whitelist instead of `--disallowedTools`.
2. **MCP servers bypass permissions**: MCP tools with file capabilities bypass `permissions.deny` rules ([issue #28595](https://github.com/anthropics/claude-code/issues/28595)). Mitigated by `--strict-mcp-config '{}'`.
3. **WebFetch data exfiltration**: Can send data to external URLs. Mitigated by excluding from `--tools`.
4. **Sandbox escape hatch**: `dangerouslyDisableSandbox` lets Claude retry failed commands outside sandbox. Mitigated by `"allowUnsandboxedCommands": false` in sandbox settings.
5. **No Windows sandbox**: On Windows, only the permission system provides isolation — no OS-level enforcement.
6. **Hooks bypass risk**: PreToolUse hooks can return "allow" to skip prompts, but deny rules still take precedence.

---

## OpenClaude

### Read-Only Mechanism
- **Flag**: `--permission-mode plan` + `--tools "Read,Grep,Glob"` (same as Claude Code)
- **What it does**: Identical to Claude Code — OpenClaude is a fork that swaps the Anthropic API for OpenAI-compatible providers while preserving the entire permission/sandbox/tool system.

### Sandboxing
- **OS-level**: Same as Claude Code — macOS Seatbelt, Linux bubblewrap, **no Windows sandbox**.
- **Language-level**: None.
- **Tool execution sandbox**: Same as Claude Code — Bash subprocesses sandboxed, file tools use permission system.
- **Network sandbox**: Proxy-based domain allowlist. Same limitations.
- **Child process isolation**: Yes — same as Claude Code.

### Tool Restrictions
- **Can disable tools**: Yes, via `--tools` (whitelist) and `--disallowedTools` (denylist). Same as Claude Code.
- **Same bypass risks as Claude Code** (Bash redirect, MCP servers, WebFetch).

### Command for Dreamer
```bash
openclaude -p \
  --permission-mode plan \
  --tools "Read,Grep,Glob" \
  --strict-mcp-config '{}' \
  --no-session-persistence \
  --bare \
  "your prompt here"
```

### Gaps / Risks
1. **Known sandbox bypass (CVSS 8.4)**: GHSA-m6rx-7pvw-2f73 in v0.1.7 — `checkPathConstraints()` skipped due to early-exit in `bashToolHasPermission()` when sandbox auto-allow is active and no explicit deny rule exists. Allows path traversal outside sandbox. **Fix**: add explicit deny rules so early-exit path is not taken.
2. **All Claude Code gaps apply**: Bash redirect bypass, MCP server bypass, WebFetch exfiltration, no Windows sandbox.
3. **`auto` mode risk**: If accidentally used, auto mode can auto-approve actions. Always use `plan` or `default`.

---

## Gemini CLI (Google)

### Read-Only Mechanism
- **Flag**: `--approval-mode plan`
- **What it does**: Enforces strict read-only at the tool level. Only read tools permitted: `read_file`, `list_directory`, `glob`, `grep_search`, `google_web_search`, `web_fetch`, `codebase_investigator`. Blocked: `run_shell_command`, `write_file`, `replace`.
- **Critical headless caveat**: In non-interactive mode, upon exiting Plan Mode, Gemini CLI **automatically switches to YOLO mode** so implementation proceeds without hanging. If the model calls `exit_plan_mode`, all restrictions vanish.

### Sandboxing
- **OS-level**: Multiple backends — Docker/Podman (cross-platform), macOS Seatbelt (6 profiles), gVisor/runsc (Linux, strongest), Windows icacls (integrity levels), LXC/LXD (Linux containers).
- **Language-level**: None.
- **Tool execution sandbox**: Yes — granular isolation for individual tool executions. Can be disabled via `security.toolSandboxing: false`.
- **Network sandbox**: Only via macOS Seatbelt proxy profiles. Docker doesn't restrict network by default (use `SANDBOX_FLAGS="--network=none"`).
- **Child process isolation**: Containers provide full process isolation. macOS Seatbelt restricts writes. Windows icacls restricts integrity level.

### Tool Restrictions
- **Can disable tools**: Yes, via Policy Engine (TOML files in `~/.gemini/policies/`). The deprecated `tools.exclude` is replaced by policy `deny` decisions.
- **Dangerous tools**: `run_shell_command`, `write_file`, `replace`, `shellBackgroundTools`.
- **Policy Engine**: TOML-based rules with conditions on tool name, arguments, command prefix/regex, approval mode. Priority tiers from Default (1) to Admin (5). `deny` decision removes tools from model's memory entirely.

### Command for Dreamer
```bash
# Option A: Plan mode only
gemini --approval-mode plan -p "your prompt here" --output-format json

# Option B: Plan mode + Docker sandbox (defense in depth)
gemini --approval-mode plan --sandbox -p "your prompt here" --output-format json

# Option C: Policy Engine deny-all-writes (works across ALL modes including yolo)
# Create ~/.gemini/policies/dreamer-read-only.toml:
# [[rule]]
# toolName = ["run_shell_command", "write_file", "replace"]
# decision = "deny"
# priority = 999
# modes = ["default", "auto_edit", "plan", "yolo"]
```

### Gaps / Risks
1. **Headless Plan Mode auto-exits to YOLO**: If model calls `exit_plan_mode`, restrictions vanish. **Mitigation**: Use Policy Engine deny rules that apply across ALL modes including yolo.
2. **`web_fetch` can access localhost/private networks**: Even in plan mode.
3. **MCP server bypass**: MCP tools could provide write capabilities. Use `--allowed-mcp-server-names` to restrict.
4. **No network sandbox by default**: Docker doesn't restrict network unless `--network=none`.
5. **RCE vulnerability history** (GHSA-wpqr-6v78-jr5g, CVSS 10.0, patched in v0.39.1): Prompt injection through untrusted repo content leading to arbitrary command execution. **Ensure Gemini CLI >= 0.39.1**.

---

## OpenCode

### Read-Only Mechanism
- **Flag**: No `--read-only` CLI flag. Uses permission config in `opencode.json`.
- **What it does**: Each permission resolves to `"allow"`, `"ask"`, or `"deny"`. When set to `"deny"`, the tool call is blocked before execution and a `PermissionDeniedError` is thrown. The tool never runs.
- **Non-interactive behavior**: In `opencode run` mode, any `"ask"` permission is **auto-rejected**. Only explicitly `"allow"`ed permissions work.

### Sandboxing
- **OS-level**: **None**. Zero built-in OS sandboxing. No bubblewrap, no containers, no seccomp, no namespaces. GitHub issue #2242 (46 upvotes) confirms this is a known gap.
- **Language-level**: None.
- **Tool execution sandbox**: Same process. All tools execute in the same Node.js/Bun process. Shell commands spawned via `ChildProcess.make()` with no sandboxing wrapper.
- **Network sandbox**: None. `webfetch` can reach any URL.
- **Child process isolation**: No. Shell commands run with `detached: true` on non-Windows. No process tree isolation.

### Tool Restrictions
- **Can disable tools**: Yes, via permission system. Setting permission to `"deny"` removes tool from model's tool list.
- **Dangerous tools**: `shell`, `edit`, `write`, `apply_patch`, `task` (subagents), `repo_clone`.
- **`edit` permission covers**: `edit`, `write`, `apply_patch` (grouped as `EDIT_TOOLS` constant).
- **MCP tools bypass permissions**: MCP tools use `dynamicTool()` from AI SDK and call `client.callTool()` directly — they do NOT go through OpenCode's permission system.

### Command for Dreamer
```json
// opencode.json
{
  "permission": {
    "*": "deny",
    "read": "allow",
    "glob": "allow",
    "grep": "allow",
    "lsp": "allow"
  }
}
```
```bash
opencode run --format json "your prompt here"
# No --dangerously-skip-permissions flag
```

### Gaps / Risks
1. **No OS-level sandboxing**: Zero defense in depth. If any permission misconfiguration allows a write, there is no second line of defense.
2. **Bash tool bypasses directory restrictions**: Collaborator confirmed "agent can use bash to get around it." Tree-sitter parser extracts file paths but is not a hard boundary.
3. **MCP server tools bypass all permissions**: Any configured MCP server can execute arbitrary operations without permission checks.
4. **Subagent escape via `task` tool**: Parent session can pass additional `"allow"` rules to subagents.
5. **`repo_clone` can write to disk**: Clones git repositories to cache directory.
6. **Network exfiltration via `webfetch`/`websearch`**: Can encode data in URLs or search queries.

---

## Codebuff

### Read-Only Mechanism
- **Flag**: None. Codebuff has **no built-in read-only mode, no approval modes, and no sandbox**.
- **What exists**: The only restriction mechanism is `toolNames` array on agent definitions (tool allowlist). Prompt-level restrictions only.
- **Current dreamer provider**: Uses the **HTTP API** (`/api/v1/chat/completions`), not the SDK. This is a pure chat-completion endpoint with no tool execution — inherently safe.

### Sandboxing
- **OS-level**: None.
- **Language-level**: None.
- **Tool execution sandbox**: None. `run_terminal_command` calls `child_process.spawn(shell, ['-c', command])` with zero filtering.
- **Network sandbox**: None.
- **Child process isolation**: No.

### Tool Restrictions
- **Can disable tools**: Yes, via `toolNames` on `AgentDefinition`. Only listed tools are available.
- **Dangerous tools**: `apply_patch`, `write_file`, `str_replace`, `run_terminal_command` (no path restriction), `write_todos`, `run_file_change_hooks`, `spawn_agents`.
- **Path scoping**: File-write tools use `resolveFilePathWithinProject` which rejects paths escaping project root. But `run_terminal_command` has no such guard.

### Command for Dreamer
**No changes needed for current HTTP API approach.** The API endpoint is text-only, no tool execution occurs.

If switching to SDK for richer analysis, define a custom agent:
```typescript
const readOnlyAgent: AgentDefinition = {
  id: 'dreamer-readonly',
  toolNames: [
    'read_files', 'code_search', 'find_files', 'glob',
    'list_directory', 'read_subtree', 'read_docs',
    'think_deeply', 'end_turn'
  ],
  systemPrompt: 'You are a read-only code analyzer. Never modify files or run commands.',
}
```

### Gaps / Risks
1. **No built-in enforcement**: All restrictions are custom.
2. **`run_terminal_command` bypasses path checks**: Unlike file-write tools, terminal commands have no path scoping.
3. **Prompt-level restrictions are soft**: LLM can ignore system prompt instructions.
4. **HTTP API is inherently safe**: Current dreamer approach is the safest.

---

## Cross-Provider Recommendations for Dreamer

### Current Provider Safety Matrix

| Provider | Integration Type | Current Safety | Recommended Action |
|----------|-----------------|----------------|-------------------|
| Claude Code | CLI (`-p` flag) | Uses `--permission-mode plan` | Add `--tools "Read,Grep,Glob"`, `--bare`, `--strict-mcp-config '{}'` |
| OpenClaude | CLI (`-p` flag) | Uses `--permission-mode plan` | Add `--tools "Read,Grep,Glob"`, `--bare`, `--strict-mcp-config '{}'` |
| Codex | CLI (`exec` subcommand) | Unknown | Use `--sandbox read-only --ask-for-approval never` |
| Gemini CLI | CLI (`-p` flag) | Unknown | Use `--approval-mode plan` + Policy Engine deny rules |
| OpenCode | ACP (HTTP) | Unknown | Use permission config: `"*": "deny"` + explicit read-only allows |
| Codebuff | HTTP API | Inherently safe (text-only) | No changes needed |

### Defense-in-Depth Strategy

1. **Layer 1 — Tool removal**: Use `--tools` whitelist (Claude Code/OpenClaude) or Policy Engine deny (Gemini) to remove write tools from model context entirely.
2. **Layer 2 — Permission system**: Use `--permission-mode plan` / `--approval-mode plan` to block writes at the permission layer.
3. **Layer 3 — OS sandbox**: Where available (Codex, Claude Code on Linux/macOS), enable OS-level sandboxing for Bash subprocesses.
4. **Layer 4 — Network isolation**: Disable network where possible (Codex `--unshare-net`, Docker `--network=none`).
5. **Layer 5 — MCP lockdown**: Use `--strict-mcp-config '{}'` or equivalent to prevent MCP servers from introducing write capabilities.

### Critical Version Requirements

- **Codex**: >= 0.39.0 (CVE-2025-59532 fix)
- **Gemini CLI**: >= 0.39.1 (GHSA-wpqr-6v78-jr5g RCE fix)
- **OpenClaude**: >= latest (GHSA-m6rx-7pvw-2f73 sandbox bypass fix)

### Windows-Specific Concerns

Dreamer runs on Windows. Key implications:
- **No OS-level sandbox** for Claude Code, OpenClaude, or Codex on Windows (bubblewrap/Seatbelt unavailable). Only permission system provides isolation.
- **Codex Windows sandbox** uses restricted process tokens — newer and less battle-tested.
- **Gemini CLI Windows sandbox** uses icacls integrity levels — changes persist after session.
- **Process tree issue**: `exec.CommandContext` on Windows doesn't kill child processes. Affects all CLI providers.
- **Mitigation**: Rely on tool-level restrictions (`--tools` whitelist) rather than OS sandboxing on Windows.
