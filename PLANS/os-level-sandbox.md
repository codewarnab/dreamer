# Plan: OS-Level Sandbox for CLI Providers

## The Problem

Dreamer's CLI providers (openclaude-cli, claude-cli) use policy-only flags for sandboxing:

```
--permission-mode plan --tools Read,Grep,Glob --bare
```

This is fundamentally flawed because:

1. **Policy-only, not OS-level** — the CLI says "you can't write" but the OS allows it
2. **Subagents bypass restrictions** — `--tools` only restricts the top-level model
3. **Phase 2 must relax permissions** — `--permission-mode plan` blocks MCP tools, so Phase 2 changes to `default`
4. **Prompt injection can bypass** — a malicious transcript could instruct the model to ignore restrictions

The code itself acknowledges this: "Defense-in-depth (NOT a true sandbox — policy-only, no isolation)"

## First Principles Analysis

### What does the model actually need?

| Operation | Phase 1 | Phase 2 | Tool |
|-----------|---------|---------|------|
| Read files | Yes | Yes | Read, cat |
| Search patterns | Yes | Yes | Grep, grep, rg |
| Find files | Yes | Yes | Glob, find |
| Record findings | No | Yes | MCP record_finding |
| Write to project | **No** | **No** | — |
| Execute commands | No | Yes (search) | Bash |

Key insight: **The model needs Bash for searching (grep, find, cat), not for writing.**

### What is the goal?

**Ensure the model cannot modify the user's codebase during analysis, even if prompt injection or model behavior tries to bypass restrictions.**

### What are the real constraints?

| Constraint | Why |
|------------|-----|
| Must protect codebase from writes | User's code is sacred |
| Must work cross-platform | Windows is primary |
| Must allow read access to codebase | Analysis requires reading code |
| Must allow write access to findings output | MCP needs to write JSONL |
| Must support Bash for searching | Models use grep/find/cat |

### What are historical constraints (can be removed)?

- Using `--permission-mode` because that's what Claude Code CLI offers
- Using `--tools` to restrict available tools
- Using `--bare` to disable MCP auto-discovery
- Phase 2 needing to relax permissions for MCP tools

## Options Considered

### Option 1: Restricted Bash Wrapper (Simplest)

A Go binary that intercepts Bash commands and only allows read-only operations.

```
Model calls: Bash("grep -r 'pattern' .")
     ↓
Restricted wrapper checks: is this read-only?
     ↓
Yes → forward to real bash
No  → BLOCKED
```

**Read-only commands allowed:**
- `grep`, `rg`, `find`, `cat`, `head`, `tail`, `wc`, `ls`, `pwd`, `file`, `stat`, `diff`, `sort`, `uniq`, `awk`, `sed` (without `-i`)

**Write commands blocked:**
- `tee`, `rm`, `mv`, `cp`, `echo >`, `dd`, `sed -i`, `chmod`, `chown`, `mkdir`, `touch`, `truncate`

| Pros | Cons |
|------|------|
| Simple implementation | Policy-based, not OS-level |
| No dependencies | Could be bypassed by creative commands |
| Cross-platform | Not true isolation |

### Option 2: cco Integration (Claude Condom)

Use the cco tool which wraps Claude Code in OS-level sandboxing.

| Platform | Backend | Mechanism |
|----------|---------|-----------|
| Linux | bubblewrap | Kernel namespaces |
| macOS | Seatbelt | Kernel sandbox profiles |
| Windows | Docker | Container isolation |

| Pros | Cons |
|------|------|
| Proven tool | Requires Docker on Windows |
| OS-level isolation | bash dependency |
| Git worktree support | External dependency |

### Option 3: OS Primitives Directly (Recommended)

Implement filesystem isolation using native OS primitives — no Docker dependency.

| Platform | Mechanism | Command/API |
|----------|-----------|-------------|
| Linux | bubblewrap | `bwrap --ro-bind / / --bind /tmp /tmp -- <cmd>` |
| macOS | Seatbelt | `sandbox-exec -f profile.sb -- <cmd>` |
| Windows | Job Objects + Restricted Tokens | `CreateRestrictedToken` + `AssignProcessToJobObject` |

**What Docker does (simplified):**
1. Creates restricted environment
2. Mounts project directory as read-only
3. Mounts temp directory as writable
4. Runs process in restricted environment

**What we do (same result, no Docker):**
1. Create restricted token (Windows) / call bwrap (Linux) / generate Seatbelt profile (macOS)
2. Project directory: read-only
3. Temp directory: writable
4. Run process with restricted access

| Pros | Cons |
|------|------|
| OS-level isolation | More implementation effort |
| No Docker dependency | OS-specific code |
| Low overhead | Need to handle each platform |
| Same isolation as Docker | — |

## Recommended Approach: OS Primitives

### Why?

1. **First principles**: Docker is a wrapper around OS primitives. We can call those primitives directly.
2. **No dependency**: Don't require users to install Docker.
3. **Low overhead**: No container daemon, no image management.
4. **Same isolation**: Kernel-level enforcement cannot be bypassed by userspace.

### Implementation Strategy

```
internal/sandbox/
├── sandbox.go          # Core types, backend interface, detection
├── sandbox_test.go     # Unit tests
├── none.go             # Fallback (no sandbox available)
├── bwrap.go            # Linux: bubblewrap
├── seatbelt.go         # macOS: Seatbelt
└── windows.go          # Windows: Job Objects + Restricted Tokens
```

### Key Types

```go
// Config describes what the sandbox should isolate.
type Config struct {
    ProjectDir    string   // read-only inside sandbox
    WritableDirs  []string // additional writable paths (temp dirs for MCP/findings)
    WorktreeDir   string   // git worktree dir, writable when set
}

// Backend is the interface each OS-specific sandbox implements.
type Backend interface {
    Name() string
    Available() bool
    WrapCommand(command []string, cfg Config) ([]string, error)
}
```

### Integration Point

In `openclaudecli.go` and `claudecli.go`, after flag assembly:

```go
// Wrap command in OS-level sandbox if available.
sandboxCfg := sandbox.Config{ProjectDir: wd}
if sessionConfig.Phase2 != nil {
    if sessionConfig.Phase2.FindingsOutputPath != "" {
        sandboxCfg.WritableDirs = append(sandboxCfg.WritableDirs,
            filepath.Dir(sessionConfig.Phase2.FindingsOutputPath))
    }
}
command, err = sandbox.WrapCommand(command, sandboxCfg)
```

### Platform-Specific Details

#### Linux (bubblewrap)

```bash
bwrap \
  --ro-bind / /              # Root filesystem read-only
  --bind /tmp /tmp           # Temp dir writable
  --bind <project> <project> # Project dir read-only (inherits from ro-bind)
  --bind <writable> <writable> # Additional writable dirs
  --unshare-net              # Network isolation (optional)
  -- <command>
```

Requires: `bwrap` installed (`apt install bubblewrap`)

#### macOS (Seatbelt)

```scheme
(version 1)
(allow default)
(deny file-write*)
(allow file-write* (subpath "/tmp"))
(allow file-write* (subpath "<writable-dir>"))
(allow process-exec)
(allow process-fork)
```

Requires: `sandbox-exec` (built-in)

#### Windows (Job Objects + Restricted Tokens)

```go
// 1. Create restricted token
token, _ := windows.OpenProcessToken(
    windows.CurrentProcess(),
    windows.TOKEN_DUPLICATE|windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY,
)
restrictedToken, _ := windows.CreateRestrictedToken(
    token,
    0, // flags
    nil, // SIDs to disable
    nil, // privileges to delete
    nil, // SIDs to restrict
)

// 2. Create Job Object with restrictions
job, _ := windows.CreateJobObject(nil, nil)
info := windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
    LimitFlags: windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY,
}
windows.SetInformationJobObject(job, windows.JobObjectBasicLimitInformation, ...)

// 3. Create process with restricted token
process, _ := windows.CreateProcessAsUser(
    restrictedToken,
    command,
    ...,
)

// 4. Assign process to Job Object
windows.AssignProcessToJobObject(job, process)
```

Requires: None (Windows API)

### Fallback Behavior

When no sandbox backend is available:
1. Log warning: "No OS-level sandbox available; falling back to policy-only"
2. Use existing `--permission-mode plan` flags
3. Continue with current behavior

### Configuration (Optional)

Add to `config.yaml`:

```yaml
providers:
  openclaude-cli:
    sandbox: auto  # auto | true | false
```

- `auto` (default): Use sandbox if available, fall back to policy-only
- `true`: Require sandbox (error if not available)
- `false`: Disable sandbox explicitly

## Implementation Sequence

| Phase | Files | Description |
|-------|-------|-------------|
| 1 | `sandbox.go`, `none.go`, `sandbox_test.go` | Core types, fallback backend, tests |
| 2 | `bwrap.go` | Linux bubblewrap backend |
| 3 | `seatbelt.go` | macOS Seatbelt backend |
| 4 | `windows.go` | Windows Job Objects + Restricted Tokens |
| 5 | `openclaudecli.go`, `claudecli.go` | Integrate sandbox into providers |
| 6 | Provider tests | Update tests for sandbox integration |
| 7 | Config (optional) | Add sandbox config option |

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| bwrap not installed on Linux | No OS-level sandbox | Graceful fallback, document `apt install bubblewrap` |
| Windows API complexity | Implementation effort | Start with basic restricted token, iterate |
| MCP server inherits sandbox | May break MCP stdio | Verify in integration tests |
| Custom `command:` override | Sandbox may conflict | `sandbox: false` disables |

## Key Insight

> **The model should get full tool access with OS-level restrictions, not restricted tools with policy-level restrictions.**

Why?
1. OS-level restrictions **cannot be bypassed** by prompt injection
2. Full tool access allows the model to **work naturally** (no "verification before recording" confusion)
3. The sandbox ensures safety **regardless of model behavior**

## Evidence That Would Change This Approach

- If the model **never needs Bash** in Phase 2 (MCP tools suffice)
- If `--allowed-tools` **overrides** `--permission-mode plan` (no sandbox needed)
- If the **complexity cost** outweighs the safety benefit
- If **prompt engineering** alone can fix the Phase 2 behavior issue

## Next Steps

1. Discuss this plan
2. Decide on implementation priority (which platform first?)
3. Implement and test
4. Iterate based on results
