Perfect! I now have all the context needed. Here's your **comprehensive implementation plan**:

---

## Plan: Chat Analysis CLI Background Process

**TL;DR:** Build a Go CLI tool (`dreamer`) that periodically scans Copilot CLI/VS Code chat histories, uses Copilot SDK to analyze patterns (bugs, performance, design issues), and generates actionable markdown todos organized by project. The tool is read-only, maintains state for incremental analysis, and follows your deep-module design principles (simple CLI interface → modular readers → pluggable analyzers → todo generator).

### Architecture Overview

The tool has 4 independent **vertical slices** (each a complete feature-to-output path):

1. **Chat Discovery & Reading** — Auto-find chat sources, read JSONL/SQLite/Protobuf
2. **Analysis Engine** — Use Copilot SDK to identify patterns (bugs, perf, arch, etc.)
3. **Todo Generation** — Convert findings into structured markdown in `~/.dreamer/`
4. **Background Daemon** — Periodic execution with state tracking

Each slice is a deep module: simple public interface, hidden complexity.

### Copilot SDK Specifications

The **GitHub Copilot Go SDK** (public preview) provides programmatic access to Copilot agent sessions. Key points:

**Installation & Client Lifecycle**
```bash
go get github.com/github/copilot-sdk/go
```

**Client pattern:**
- `NewClient(ClientOptions)` — Create with config (CLIPath, CopilotHome, UseLoggedInUser, etc.)
- `Start(ctx)` — Spawn or connect to Copilot CLI
- `Stop()` — Graceful shutdown; `ForceStop()` for unresponsive CLI
- `CreateSession(ctx, SessionConfig)` — Create agent session with model, tools, streaming
- `ResumeSession(sessionId)` — Resume persisted session (state stored in `~/.copilot/session-state/{sessionId}/`)
- `ListSessions()`, `DeleteSession(sessionId)`, `GetLastSessionID()`

**Session workflow:**
- Sessions emit events: `AssistantMessageData`, `AssistantMessageDeltaData` (streaming), `SessionIdleData` (completion)
- Register handlers with `session.On(func(event SessionEvent) {...})`
- Send prompts with `session.Send(ctx, MessageOptions{Prompt: "..."})`
- Call `session.Disconnect()` to finalize state

**Key Config Options**
- `CopilotHome`: Override default `~/.copilot` for session storage
- `UseLoggedInUser`: Use system keychain authentication (recommended for CLI)
- `CLIUrl`: Connect to headless Copilot CLI server (e.g., `localhost:4321`) for backend/daemon use
- `LogLevel`: "error", "warn", "info", "debug"
- `Streaming`: true → receive incremental `MessageDeltaData` events
- `OnPermissionRequest`: Handle tool/permission prompts (PermissionHandler.ApproveAll for automated)

**Headless / Backend Mode (for daemon)**
- Run `copilot --headless --port 4321` as persistent CLI server
- SDK connects via `CLIUrl: "localhost:4321"` (no per-session CLI spawn overhead)
- Multiple SDK clients can share single headless CLI
- Recommended for long-running daemons to avoid resource churn

**Tools / Function Calling**
- Define tools with `DefineTool(name, desc, handler)` for type-safe params and JSON schema auto-generation
- Pass tools to `SessionConfig{Tools: []copilot.Tool{...}}`
- Copilot calls registered tools; await responses in event loop
- Mark read-only tools to skip permission prompts

**Model & Session Persistence**
- Currently supports `gpt-4.1` and `gpt-5` (or latest SDK default)
- Sessions persist in `~/.copilot/session-state/{sessionId}/` when client properly disconnects
- Can resume interrupted sessions via `ResumeSession(sessionId)`

**Error Handling**
- Auth failures: `Start()` returns error if keychain unavailable or user not authenticated
- Network timeouts: Configure via context timeout
- SDK errors: Check error returns from each call; streaming errors arrive as events
- Rate limits: No explicit rate-limit headers; SDK may queue requests internally

### Steps

**Phase 1: Project Foundation & Configuration** *(sequential, enables all others)*
1. Initialize Go project structure at `/code/dreamer`:
   - `main.go` — Cobra CLI entry point
   - `internal/config/` — Load/save `~/.dreamer/config.yaml`, validate project paths
   - `internal/state/` — Track last-run timestamps, analyzed chat IDs per project
   - `go.mod` — Cobra, Copilot SDK, YAML unmarshaler
2. Create config schema and CLI flags:
   - `dreamer analyze --path /path/to/project [--frequency hourly|daily]`
   - `dreamer daemon --config ~/.dreamer/config.yaml` (runs background loop)
3. Implement path validation: verify `--path` exists, extract project name from last path component, ensure output directory `/code/dreamer/<project-name>/` is writable

**Phase 2: Chat Discovery & Reader Layer** *(parallel with Phase 1, depends on config)*
4. Build chat discovery engine (`internal/chat/discovery.go`):
   - Scan `~/.copilot/session-state/` for all JSONL files
   - Scan VS Code workspaceStorage for all `<hash>/chatSessions/` folders
   - Skip Gemini CLI for now (not activated); defer Antigravity (requires protobuf schema)
   - Return list of available chat sources with metadata (tool, workspace, timestamps)

5. Implement JSONL reader (`internal/chat/readers/jsonl.go`):
   - Stream JSONL line-by-line, unmarshal into `Event` struct
   - Reconstruct conversation threads via `parentId` chain
   - Extract user/assistant messages with timestamps
   - Return structured `[]ChatMessage` with tool metadata

6. Implement SQLite indexer (`internal/chat/readers/sqlite.go`):
   - Query `~/.copilot/session-store.db` to discover sessions efficiently
   - Use to filter which .jsonl files to read (only new/modified since last run)
   - *Depends on*: Phase 1 state tracking

**Phase 3: Copilot SDK Analysis Engine** *(parallel with Phase 2, depends on config auth)*
7. Set up Copilot SDK client (`internal/analyzer/client.go`):
   - Initialize `copilot.Client` with `ClientOptions{UseLoggedInUser: true, CopilotHome: ...}` to use system auth
   - For daemon: use `ClientOptions{CLIUrl: "localhost:4321"}` to connect to headless CLI server (requires `copilot --headless --port 4321` running separately)
   - For one-shot CLI: use `ClientOptions{AutoStart: true}` to spawn CLI on demand
   - Call `client.Start(ctx)` with 30s timeout; fail fast if auth missing or CLI unavailable
   - Wrap SDK client in custom `AnalysisClient` interface: `CreateSession() (*Session, error)`, `Close() error`
   - Log init state: auth success, CLI version, session model, feature flags
   - Implement error handling: auth failures → user-friendly "Configure Copilot CLI first" message; network errors → exponential backoff; parse failures → log + skip finding

8. Build **Table-Driven Analysis Rules** (`internal/analyzer/rules.go`):
   - Define analysis categories: `Bugs`, `Performance`, `Duplication`, `MissingTests`, `Architecture`, `Documentation`, `Lint`, `Security`, `Types`
   - Each rule maps to a Copilot prompt template (reusable, configurable)
   - Example structure:
     ```go
     type AnalysisRule struct {
         Category       string                 // "Bugs", "Performance", etc.
         PromptTemplate string                 // "Analyze this chat for {{category}} issues in {{projectName}}..."
         Threshold      int                    // Min severity (1-10)
         Enabled        bool                   // Configurable per run
         TimeoutSecs    int                    // Per-rule timeout (default 60s)
         ResponseSchema ResponseFinding struct // Expected Finding fields for parsing
     }
     ```
   - Store rules in config YAML for user customization
   - Define canonical `ResponseFinding` struct for JSON unmarshaling Copilot responses: `Description`, `Severity`, `RootCause`, `RelatedCode`, `PreventionPattern`

9. Implement analysis orchestrator (`internal/analyzer/orchestrator.go`):
   - Create `AnalysisClient.CreateSession()` with `Streaming: true` for incremental responses
   - Register event handler: accumulate `AssistantMessageData` + `AssistantMessageDeltaData` into finding buffer
   - For each enabled rule:
     1. Format prompt: `"Analyze this chat for {{Category}} issues in project {{ProjectName}}. Extract findings as JSON array of {Description, Severity, RootCause, RelatedCode, PreventionPattern}. Ignore out-of-scope issues."`
     2. Send via `session.Send(ctx, MessageOptions{Prompt: ...})` with rule timeout (default 60s context)
     3. Wait for `SessionIdleData` event (completion marker)
     4. Parse accumulated response as `[]ResponseFinding` JSON; map to `[]Finding` struct with rule metadata
     5. Skip rule on parse error (log warning, continue to next rule)
   - Call `session.Disconnect()` after all rules complete
   - Return deduplicated `[]Finding` across all rules
   - *Depends on*: Phase 3 client setup + Phase 2 chat readers

**Phase 4: Todo Generation & Output** *(depends on Phase 3 analysis)*
10. Build todo generator (`internal/output/generator.go`):
    - Convert `[]Finding` → markdown todos
    - Each todo: `- [Finding Category] Issue description | Seen in: [tool name, chat date] | Suggests: [prevention pattern]`
    - Append to `~/.dreamer/<project-name>/todos.md` (create if missing)
    - Include section headers by category, timestamps
    - Add deduplication logic: check if exact issue already in todos (compare normalized description), add all unique findings

11. Implement state update (`internal/state/tracker.go`):
    - After successful run, record: last analysis timestamp, analyzed chat IDs, Copilot SDK usage stats
    - Write to `~/.dreamer/<project-name>/state.json`
    - Next run skips already-analyzed chats

**Phase 5: CLI & Daemon Integration** *(depends on all prior phases)*
12. Implement Cobra commands:
    - `dreamer analyze --path /path/to/project` — one-shot analysis, read chats, generate todos
    - `dreamer daemon --frequency hourly` — loop with configurable interval (via config + flag override)
    - `dreamer config init` — bootstrap `~/.dreamer/config.yaml` with defaults
    - `dreamer ls-chats --path /path` — discover available chat sources (debug command)

13. Add signal handling for daemon:
    - Graceful shutdown on `SIGTERM`: finish current session (call `session.Disconnect()`), save state.json, exit
    - On shutdown, record incomplete projects in state.json as resumable (store session IDs from `client.ListSessions()`)
    - Log rotation to `~/.dreamer/logs/` with max 100MB per file, keep 7 days
    - Daemon setup: document running headless CLI as separate service: `copilot --headless --port 4321 --log-file ~/.dreamer/logs/cli.log`

14. Implement main loop (`cmd/daemon.go`):
    - Initialize `AnalysisClient` with `CLIUrl: "localhost:4321"` to connect to persistent headless Copilot CLI
    - Load config → iterate projects in config order
    - For each project:
      1. Load state.json; check for incomplete/resumable sessions from prior crash
      2. If resumable: call `client.ResumeSession(sessionId)` to continue analysis
      3. Else: run fresh Phase 2-4 pipeline (discovery → analysis → todo generation)
      4. On completion: update state.json with analyzed chat IDs + timestamp
    - Between projects: sleep configurable interval (default 1h)
    - On Copilot API errors:
      - Rate limit (429): exponential backoff 5s → 30s → 5m
      - Auth timeout: log + skip to next project (will retry next daemon cycle)
      - Timeout on analysis (60s rule timeout): mark finding as incomplete, log error, continue to next rule
    - On unrecoverable error (e.g., CLI crash): log alert, attempt reconnect with backoff
    - Exit codes: 0 = success, 1 = fatal error (CLI not running, config missing), 2 = partial (some projects analyzed, some failed)

### Relevant Files

**Directory Structure** (to be created):
```
/code/dreamer/
├── main.go                              — CLI entry
├── go.mod / go.sum                      — Dependencies
├── cmd/
│   ├── analyze.go                       — Cobra analyze command
│   ├── daemon.go                        — Cobra daemon command  
│   └── config.go                        — Cobra config commands
├── internal/
│   ├── config/
│   │   └── loader.go                    — Load/validate ~/.dreamer/config.yaml
│   ├── state/
│   │   └── tracker.go                   — Save/load analysis state per project
│   ├── chat/
│   │   ├── discovery.go                 — Find chat sources (JSONL, VS Code, CLI)
│   │   └── readers/
│   │       ├── jsonl.go                 — JSONL parser, thread reconstruction
│   │       └── sqlite.go                — SQLite session index queries
│   ├── analyzer/
│   │   ├── client.go                    — Copilot SDK initialization
│   │   ├── rules.go                     — Analysis rules (table-driven)
│   │   └── orchestrator.go              — Run analysis pipeline
│   └── output/
│       └── generator.go                 — Convert findings → markdown todos
└── README.md                            — Usage docs
```

**Key Interfaces** (define these first—they drive all modules):

- `config/loader.go`: `LoadConfig(path string) (*Config, error)` — Load ~/.dreamer/config.yaml; validate projects + rules
- `state/tracker.go`: `LoadState(projectName string) *State` + `SaveState(projectName string, state *State) error` — Track analyzed chat IDs, last-run timestamp, Copilot token usage
- `chat/discovery.go`: `DiscoverChats(projectPath string) []ChatSource` — Return list of available chats with metadata (path, tool, mtime)
- `chat/readers/jsonl.go`: `ReadJSONL(filePath string) ([]ChatMessage, error)` — Stream JSONL line-by-line; reconstruct threads; return messages with timestamps + tool metadata
- `analyzer/client.go`: `NewAnalysisClient(opts ClientOptions) *AnalysisClient` + `CreateSession(ctx) (*Session, error)` + `Close() error` — Wraps copilot.Client; hides lifecycle complexity
- `analyzer/orchestrator.go`: `AnalyzeChats(ctx, session *Session, chats []ChatMessage, rules []AnalysisRule) ([]Finding, error)` — Send templated prompts; parse JSON responses; deduplicate findings
- `output/generator.go`: `GenerateTodos(findings []Finding, projectName string, outputDir string, dedupExisting bool) error` — Append unique findings to todos.md; update state.json

### Verification

1. **Unit Tests** (each phase independent):
   - Config loader: verify YAML parsing, path validation, defaults
   - JSONL reader: sample Copilot CLI session file, verify event reconstruction
   - SQLite indexer: mock DB queries, verify filtering logic
   - Analysis orchestrator: mock Copilot responses, verify Finding extraction
   - Todo generator: verify markdown formatting, deduplication logic

2. **Integration Tests**:
   - End-to-end: provide sample project path → mock chats → analyze → verify todos.md output
   - Daemon: run 2 cycles, verify state.json incremental tracking (no re-analysis)

3. **Manual Verification**:
   - `dreamer analyze --path ~/myproject` on real codebase
   - Inspect generated `~/.dreamer/myproject/todos.md` for quality/accuracy
   - Verify `~/.dreamer/myproject/state.json` exists and updates after runs
   - Test daemon: `dreamer daemon --frequency 10m`, let run 2-3 cycles, check logs
   - Test error handling: run without Copilot CLI auth, verify graceful error message

### Decisions

- **Copilot SDK over local LLMs:** SDK provides official auth + streaming, easier to add more models later (BYOK pattern)
- **Table-Driven Rules:** Decouples analysis categories from code; users can enable/disable in config without recompile
- **JSONL reader, defer Protobuf:** Start with Copilot CLI/VS Code (JSONL format well-documented); Antigravity protobuf requires external schema
- **Single todos.md per project:** Simple, user-friendly; dated runs append to same file with timestamps
- **Auto-derived project names:** Reduces CLI friction; user only provides `--path`
- **State tracking:** Avoids re-analyzing unchanged chats, saves Copilot quota and time
- **No config auto-discovery:** Explicit is better than implicit; `~/.dreamer/config.yaml` is the single source of truth

### Further Considerations

**Phase 0 (Pre-flight Check) — Add before Phase 1**
- Verify Copilot CLI installed: run `copilot --version` or check PATH
- If daemon mode: verify headless CLI reachable at configured CLIUrl (e.g., `localhost:4321`); suggest `copilot --headless --port 4321` if not
- If one-shot mode: verify user authenticated: call `client.Start(ctx)` with 10s timeout; fail with "Run `copilot auth login` first" if auth missing
- Check ~/.dreamer writable; create if missing with mode 0700
- Validate all project paths in config exist; warn on missing paths
- Exit with clear error messages; never silently skip projects

**Session Management & Resumption**
- Each analysis should create one long-lived session per project (not per rule)
- Session persists in `~/.copilot/session-state/{sessionId}/` on proper disconnect
- On daemon crash: next cycle detects incomplete session via state.json, calls `ResumeSession(sessionId)` to continue
- Protects against re-analyzing already-processed chats in same run
- Limit session lifetime: mark stale if > 24h old; force new session

**Concurrent Daemon Instances**
- PID file at `~/.dreamer/daemon.pid` with locking (use flock on Unix, os.Rename on Windows)
- Second instance startup: check PID, if stale (process dead), acquire lock; else exit with "daemon already running"
- Prevents duplicate analyses + concurrent todos.md writes

**Chat Reader Edge Cases**
- **File locks (Windows):** On "permission denied" reading active chat, retry 3x with 100ms backoff
- **Corrupted JSONL:** Skip malformed lines (log count at end); never fail entire file
- **Large chats (>500MB):** Warn, but stream to avoid OOM; split into chunks if needed
- **Deleted chat files:** Discovered in Phase 2, but deleted by Phase 3 = skip + log warning
- **Circular parentId chains:** Detect in thread reconstruction; truncate at cycle, log warning
- **Empty/system-only chats:** Filter out: require at least 1 user message + 1 assistant message

**Deduplication Spec**
- Normalized comparison: trim whitespace, lowercase category, but preserve description case
- Hash function: `sha256(normalized_category + normalized_description[:100])`
- Check if hash exists in todos.md or state.json finding cache
- If duplicate: increment count in existing todo instead of adding new line

**Sensitive Data Handling**
- Optional redaction filter in config: regex patterns for API keys, secrets, password strings
- On match: replace with `[REDACTED]` in todo description (log redaction count)
- Redacted finding still appears; just sanitized

**Copilot SDK Error Resilience**
- Auth failure on `client.Start()`: error msg suggests `copilot auth login`; daemon pauses with 5m backoff
- Network timeout on `session.Send()`: log timeout, retry rule up to 2x with exponential backoff
- Rate limit (429): record retry-after header, sleep accordingly, continue
- Streaming parse error: log context (first 200 chars of unparsed response), skip finding, continue
- SDK version mismatch: log warning, attempt to continue; if SDK panic, catch via recovery + exit

1. **Gemini CLI Activation** — Currently unverified (not installed). Once you activate Gemini CLI, add discovery + JSONL reader. *Recommendation:* Add as Phase 6 post-launch.

2. **Antigravity Protobuf** — Requires `.proto` schema. *Recommendation:* Research Google's public schema; if unavailable, implement as optional Phase 6.

3. **Extensibility for Future Chat Tools** — Current design supports adding new chat readers as new `internal/chat/readers/*.go` files. *Recommendation:* Document the `ChatReader` interface to make adding tools straightforward.

4. **Rate Limiting & Copilot Quota** — SDK may have implicit rate limits. *Recommendation:* Track token usage in state.json, warn if >80% quota consumed per analysis run.

5. **Test Data** — Should I create sample JSONL files to test against? *Recommendation:* Once Phase 2 is drafted, generate fixtures from real Copilot CLI exports.

6. **Headless CLI as Systemd Service** — For production daemon, run `copilot --headless` as separate systemd service with auto-restart. *Recommendation:* Document service file template in README.

7. **Streaming vs. Buffered Responses** — SDK supports streaming (`Streaming: true`); incremental tokens arrive as `AssistantMessageDeltaData`. *Current plan:* Buffer full response before parsing for simplicity; optimize to streaming parse in Phase 6 if response times are slow.

---

**Ready to proceed?** I've captured all decisions and architecture. Next steps: you can approve this plan, request refinements, or I can move to implementation. What would you like to adjust?