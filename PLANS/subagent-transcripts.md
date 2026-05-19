# Plan: Subagent Transcript Support

## Context

Dreamer discovers chat transcripts from 6 providers (Claude Code, Codex, Gemini CLI, OpenCode, Codebuff, OpenClaude). Subagent/child transcripts exist in all providers but dreamer has three gaps:

1. **No parent-child linkage** — subagent transcripts are analyzed independently, losing parent context
2. **Inline subagent content not extracted** — readers skip `AgentContentBlock` (Codebuff), `subtask` parts (OpenCode), `collab_agent` items (Codex)
3. **Duplicate analysis** — subagent JSONL files analyzed as separate sessions alongside parent

## Approach

Two independent workstreams:

### Workstream A: Extract inline subagent content from readers

These are self-contained reader improvements — no discovery changes needed.

**A1. Codebuff: Extract `AgentContentBlock` text**
- File: `internal/chat/readers/codebuff.go`
- Current: `codebuffRawBlock` has only `Type` and `Content` fields (line 46-49)
- Change: Add `Blocks []codebuffRawBlock` field to `codebuffRawBlock` for nesting
- Change: In `extractCodebuffBlockText` (line 95-108), when `block.Type == "agent"`, recursively extract text from `block.Content` and nested `block.Blocks`
- Impact: Subagent reasoning and output from Codebuff sessions becomes visible

**A2. OpenCode: Extract `subtask` and `tool` part types**
- File: `internal/chat/readers/opencode.go`
- Current: `openCodePartText` (line 207-223) only handles `"text"` and `"reasoning"` types
- Change: Add `"subtask"` case — extract the subtask's content text from the JSON `data` field
- Change: Add `"tool"` case — extract tool name and result summary (tool name + outcome status)
- Impact: Subtask delegation and tool usage from OpenCode sessions becomes visible

### Workstream B: Parent-child linkage and filtering

**B1. Add `ParentID` to `ChatSource`**
- File: `internal/chat/types.go`
- Change: Add `ParentID string` field to `ChatSource` struct (line 25-29)
- Empty = top-level session; non-empty = subagent/child transcript

**B2. Populate `ParentID` per provider during discovery**

- `internal/chat/source_claude.go`: After `walkChatFiles`, detect paths containing `/subagents/agent-`. Extract parent session ID from the directory structure (`<sessionId>/subagents/agent-<agentId>.jsonl` → `ParentID = <sessionId>`).

- `internal/chat/source_gemini_cli.go`: After `walkChatFiles`, detect paths containing nested `<parentId>/<childId>.jsonl` under `chats/`. The parent session file is at `chats/<parentId>.jsonl` (doesn't exist — parent is `chats/session-*.jsonl`). Extract `ParentID` from the parent directory name.

- `internal/chat/source_codex.go`: After `walkChatFiles`, read `session_meta` from the first line. If `source` field is `"subagent"`, look up the parent thread ID from the `CollabToolCall` events in parent rollout files. (Lower priority — Codex child threads have their own CWD and work independently.)

- `internal/chat/source_opencode.go`: After listing sessions, query `parent_id` column. If non-null, set `ParentID`.

**B3. Config-driven subagent filtering with opt-in**
- File: `internal/config/config.go` — add `IncludeSubagentTranscripts bool` to `AnalyzerConfig` (default: false)
- File: `internal/config/defaults.go` — default to `false`
- File: `cmd/analyze_pipeline.go` — pass config to `buildProviderBlocks`
- File: `internal/pipeline/transcript.go` — in `buildProviderBlocks` (line 36-108):
  - Skip sources where `ParentID != ""` unless `IncludeSubagentTranscripts` is true
  - When including: tag subagent messages with `[subagent: <agentId>]` prefix for clarity
- Config YAML: `analyzer.include_subagent_transcripts: true` to opt in

## Files to Modify

| File | Change |
|------|--------|
| `internal/chat/readers/codebuff.go` | A1: Recursive block extraction for `AgentContentBlock` |
| `internal/chat/readers/opencode.go` | A2: Extract `subtask` and `tool` part types |
| `internal/chat/types.go` | B1: Add `ParentID` to `ChatSource` |
| `internal/chat/source_claude.go` | B2: Set `ParentID` from path structure |
| `internal/chat/source_gemini_cli.go` | B2: Set `ParentID` from nested directory |
| `internal/chat/source_opencode.go` | B2: Set `ParentID` from `parent_id` column |
| `internal/pipeline/transcript.go` | B3: Filter by `ParentID` |

## Verification

1. `go build ./...` — compile check
2. `go test ./...` — existing tests pass
3. `go test ./internal/chat/readers/ -run TestCodebuff` — Codebuff reader tests
4. `go test ./internal/chat/readers/ -run TestOpenCode` — OpenCode reader tests
5. `go vet ./...` — static analysis
6. Manual: run `dreamer analyze` on a project with Codebuff/OpenCode sessions that have subagent content, verify subagent text appears in the output
