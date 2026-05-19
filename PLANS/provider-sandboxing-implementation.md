# Plan: Provider Sandboxing & Read-Only Safety

## Problem

Dreamer shells out to provider CLIs (or uses their ACP/HTTP interfaces) to run analysis prompts. These providers can edit files, run shell commands, and modify git state. If dreamer's analysis prompt triggers any of these actions, it could corrupt the user's codebase.

**Goal**: Every provider integration must be configured so the provider can ONLY read the project directory and return text analysis. Never edit/create/delete files, run destructive commands, change git state, modify configs, spawn bypassing child processes, or exfiltrate data.

## Current State

| Provider | Current Flags | Gap |
|----------|--------------|-----|
| Claude Code | `--permission-mode plan` | Missing `--tools "Read,Grep,Glob"`, `--bare`, `--strict-mcp-config '{}'` |
| OpenClaude | `--permission-mode plan` | Same as Claude Code |
| Codex | `--sandbox read-only` | Missing `--ask-for-approval never` |
| Gemini CLI | `--approval-mode plan` | Missing Policy Engine deny rules for headless YOLO auto-exit |
| OpenCode (ACP) | ACP `readTextFile:true, writeTextFile:false` | No `--bare`-equivalent; relies on ACP permission handler |
| Codebuff (HTTP) | Pure chat API, no tools | Already safe — no changes needed |

## Changes

### 1. Claude Code — harden default command
**File**: `internal/analyzer/providers/claudecli/claudecli.go:34`

**Current**:
```go
command = []string{"claude", "-p", "--verbose", "--output-format=stream-json", "--permission-mode", "plan"}
```

**New**:
```go
command = []string{"claude", "-p", "--verbose", "--output-format=stream-json", "--permission-mode", "plan", "--tools", "Read,Grep,Glob", "--bare", "--strict-mcp-config", "{}"}
```

**What each flag does**:
- `--permission-mode plan`: blocks write tool calls at permission layer
- `--tools "Read,Grep,Glob"`: removes Bash, Edit, Write, WebFetch, NotebookEdit from model context entirely — model cannot even attempt write actions
- `--bare`: skips hooks, skills, plugins, auto-memory, CLAUDE.md discovery
- `--strict-mcp-config '{}'`: disables all MCP servers (empty config)

### 2. OpenClaude — harden default command
**File**: `internal/analyzer/providers/openclaudecli/openclaudecli.go:35`

**Current**:
```go
command = []string{"openclaude", "-p", "--verbose", "--output-format=stream-json", "--permission-mode", "plan"}
```

**New**:
```go
command = []string{"openclaude", "-p", "--verbose", "--output-format=stream-json", "--permission-mode", "plan", "--tools", "Read,Grep,Glob", "--bare", "--strict-mcp-config", "{}"}
```

Same flags as Claude Code — identical fork, identical permission system.

### 3. Codex — add approval policy
**File**: `internal/analyzer/providers/codexcli/codexcli.go:48`

**Current**:
```go
command = []string{"codex", "exec", "--json", "--sandbox", "read-only"}
```

**New**:
```go
command = []string{"codex", "exec", "--json", "--sandbox", "read-only", "--ask-for-approval", "never"}
```

**What it does**: In read-only sandbox mode, if the model attempts a write, the sandbox rejects it. With `--ask-for-approval never`, writes silently fail without prompting (no interactive user to answer). Network is already fully isolated via `--unshare-net` in read-only mode.

### 4. Gemini CLI — add Policy Engine deny rules
**File**: `internal/analyzer/providers/geminicli/geminicli.go`

**Problem**: `--approval-mode plan` is strong, but in headless mode, if the model calls `exit_plan_mode`, Gemini CLI auto-switches to YOLO mode and all restrictions vanish.

**Approach**: Add `--sandbox` flag to default command for Docker-based isolation. The Policy Engine deny rules require writing a TOML file to `~/.gemini/policies/`, which is more complex — defer to a follow-up or document as a recommended user-side config.

**Current**:
```go
command = []string{"gemini", "-p", "--output-format=stream-json", "--approval-mode=plan"}
```

**New**:
```go
command = []string{"gemini", "-p", "--output-format=stream-json", "--approval-mode=plan"}
```

**Decision**: Keep as-is for now. The `--approval-mode plan` flag is the primary defense. The YOLO auto-exit is a Gemini CLI design issue that needs either:
- A Gemini CLI upstream fix (headless mode should not auto-exit to YOLO)
- Policy Engine TOML file (complex, requires file management)
- Document as a known risk in the provider's error output

Add a comment documenting the risk.

### 5. OpenCode ACP — verify read-only enforcement
**File**: `internal/analyzer/providers/acpcore/acpcore.go`

The ACP core already has strong read-only enforcement:
- `clientCapabilities.fs.writeTextFile: false` (line 103)
- Permission handler with path-scoped filesystem checks
- Shell write idiom detection
- Empty MCP servers in session/new (line 204)

**No code changes needed**. The ACP permission system in `permission.go` is already well-designed. Document the gaps (MCP tool permission bypass is an OpenCode-side issue, not dreamer's).

### 6. Tests
Update existing provider tests to verify the new flags appear in the default command. Tests already exist at:
- `internal/analyzer/providers/claudecli/` (register.go)
- `internal/analyzer/providers/codexcli/`
- `internal/analyzer/providers/geminicli/`
- `internal/analyzer/providers/openclaudecli/`

## Files to Change

1. `internal/analyzer/providers/claudecli/claudecli.go` — add `--tools`, `--bare`, `--strict-mcp-config`
2. `internal/analyzer/providers/openclaudecli/openclaudecli.go` — same flags
3. `internal/analyzer/providers/codexcli/codexcli.go` — add `--ask-for-approval never`
4. `internal/analyzer/providers/geminicli/geminicli.go` — add comment documenting YOLO risk

## What We're NOT Changing

- **Codebuff HTTP API**: Already safe (text-only, no tool execution)
- **ACP permission system**: Already well-designed (`permission.go`)
- **OpenCode ACP**: Already enforces read-only via ACP capabilities
- **No new config options**: These are hardcoded safety defaults, not user-configurable. Users who override via `command:` in config take on the risk themselves.

## Verification

After changes:
1. `go build ./...` — must compile
2. `go test ./...` — must pass
3. `go vet ./...` — clean
4. Verify each provider's default command string matches the research report recommendations
