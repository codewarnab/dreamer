# Research Prompt: Provider Sandboxing & Read-Only Safety

**Objective**: For each provider codebase in `provider-codebase/`, investigate how to ensure dreamer's analysis NEVER modifies user code, git branches, configs, or any files. Dreamer should only READ transcripts and WRITE to its own output files (`todos.md`, `state.json`, `dreamer.log`).

### For each provider, answer these questions:

**1. Permission/Approval Modes**
- What flags or config options control read-only vs read-write mode?
- What is the most restrictive mode available? (e.g., `--permission-mode plan`, `--approval-mode=plan`, `--sandbox read-only`)
- Does the provider reject write tool calls in this mode, or just warn?

**2. Tool Restrictions**
- Can we disable specific tools entirely? (e.g., no `Bash`, no `Edit`, no `Write`)
- Is there a `--disallowed-tools` or `--tool-allowlist` flag?
- What tools does the provider have that could modify the filesystem?

**3. Filesystem Isolation**
- Does the provider support `--add-dir` or workspace scoping to limit which directories it can access?
- Can we restrict the provider to ONLY read the project directory?
- Does the provider have a `--read-only` or `--no-write` flag?

**4. Sandboxing Mechanisms**
- Does the provider use OS-level sandboxing (containers, namespaces, seccomp)?
- Does it use language-level sandboxing (restricted permissions, capability dropping)?
- Does it sandbox tool execution separately from the main process?
- Can child processes bypass the sandbox?
- Does it sandbox network access separately from filesystem access?

**5. Network/Process Isolation**
- Can the provider spawn child processes that bypass restrictions?
- Does it have MCP server support that could write files?
- Can it make network requests that could exfiltrate data?
- Does it sandbox network access (allowlist domains, block local network)?

**6. What the provider ACTUALLY does in read-only mode**
- Does it still attempt writes and get rejected? (noisy, wastes tokens)
- Does it skip write tools entirely? (cleaner)
- Does it still run hooks that could modify files?

**7. Recommended flags for dreamer**
- List the exact command-line flags dreamer should use for each provider
- List any environment variables that enforce read-only behavior
- List any config file settings that restrict permissions

### Provider-specific research targets:

| Provider | Key files to check |
|----------|-------------------|
| **Claude Code** | `src/types/permissions.ts`, `src/main.tsx` (flag definitions), `src/tools/` (tool implementations), `src/utils/hooks/` (hook system) |
| **Codex** | `codex-rs/exec/src/cli.rs` (flag definitions), `codex-rs/core/src/` (tool execution), `codex-rs/sandbox/` (sandbox implementation) |
| **Gemini CLI** | `packages/cli/src/config/config.ts` (approval modes), `packages/core/src/tools/` (tool implementations), `packages/core/src/sandbox/` (if exists) |
| **OpenCode** | `packages/opencode/src/permission/` (permission system), `packages/opencode/src/acp/agent.ts` (ACP permissions), `packages/opencode/src/tool/` (tool execution) |
| **Codebuff** | `common/src/tools/` (tool definitions), `sdk/src/` (API permissions), `cli/src/sandbox/` (if exists) |
| **OpenClaude** | `src/types/permissions.ts`, `src/main.tsx` (same as Claude Code — fork), `src/utils/hooks/` (hook system) |

### Output format per provider:

```
## [Provider Name]

### Read-Only Mechanism
- Flag: `--permission-mode plan` (or equivalent)
- What it does: [rejects writes / skips write tools / warns only]

### Sandboxing
- OS-level: [containers / namespaces / seccomp / none]
- Language-level: [restricted permissions / capability dropping / none]
- Tool execution sandbox: [separate process / same process / none]
- Network sandbox: [allowlist / block local / none]
- Child process isolation: [yes / no]

### Tool Restrictions
- Can disable tools: [yes/no]
- Dangerous tools: [list of tools that could modify files]
- Recommended restriction: [flag or config]

### Command for dreamer
```
[exact command with all safety flags]
```

### Gaps / Risks
- [any way the provider could still modify files]
- [any hooks or MCP servers that bypass restrictions]
- [any child process escape vectors]
```
