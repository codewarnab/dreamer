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
   - Initialize `CopilotClient` with Copilot CLI authentication (auto-detect system keychain)
   - Create session with `gpt-4.1` model (or equivalent latest)
   - Implement error handling: fail loud on auth failure, network errors, rate limits
   - Log initialization state and token usage estimates

8. Build **Table-Driven Analysis Rules** (`internal/analyzer/rules.go`):
   - Define analysis categories: `Bugs`, `Performance`, `Duplication`, `MissingTests`, `Architecture`, `Documentation`, `Lint`, `Security`, `Types`
   - Each rule maps to a Copilot prompt template (reusable, configurable)
   - Example structure:
     ```go
     type AnalysisRule struct {
         Category    string
         PromptTemplate string  // "Analyze this chat for {{category}} issues..."
         Threshold   int        // Min severity (1-10)
         Enabled     bool       // Configurable per run
     }
     ```
   - Store rules in config YAML for user customization

9. Implement analysis orchestrator (`internal/analyzer/orchestrator.go`):
   - Take `[]ChatMessage` + project codebase context
   - For each enabled rule, send Copilot SDK prompt: "Extract {{Category}} issues from this chat about my project {{ProjectName}}. Ignore issues outside the codebase."
   - Stream responses, parse findings into `[]Finding` struct
   - Extract: issue description, related code/files, severity, root cause pattern
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
    - Graceful shutdown on `SIGTERM` (finish current project, save state)
    - Log rotation to `~/.dreamer/logs/`

14. Implement main loop (`cmd/daemon.go`):
    - Load config → iterate projects
    - For each: run Phase 2-4 pipeline (discovery → analysis → todo generation)
    - Sleep between runs
    - Exponential backoff on Copilot SDK errors (rate limit, auth timeout)

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

- `config/loader.go`: `LoadConfig(path string) (*Config, error)` — Load ~/.dreamer/config.yaml
- `state/tracker.go`: `LoadState(projectName string) *State` + `SaveState(projectName string, state *State) error`
- `chat/discovery.go`: `DiscoverChats(projectPath string) []ChatSource` — Return list of available chats with metadata
- `chat/readers/jsonl.go`: `ReadJSONL(filePath string) ([]ChatMessage, error)` — Stream JSONL into messages
- `analyzer/client.go`: `NewCopilotClient(useLoggedInUser bool) *CopilotClient` + `SendPrompt(prompt string, context string) (string, error)`
- `analyzer/orchestrator.go`: `AnalyzeChats(chats []ChatMessage, rules []AnalysisRule) []Finding`
- `output/generator.go`: `GenerateTodos(findings []Finding, projectName string, outputDir string) error`

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

1. **Gemini CLI Activation** — Currently unverified (not installed). Once you activate Gemini CLI, add discovery + JSONL reader. *Recommendation:* Add as Phase 6 post-launch.

2. **Antigravity Protobuf** — Requires `.proto` schema. *Recommendation:* Research Google's public schema; if unavailable, implement as optional Phase 6.

3. **Extensibility for Future Chat Tools** — Current design supports adding new chat readers as new `internal/chat/readers/*.go` files. *Recommendation:* Document the `ChatReader` interface to make adding tools straightforward.

4. **Rate Limiting & Copilot Quota** — SDK has rate limits. *Recommendation:* Add telemetry logging + exponential backoff; document estimated cost per 100-project scan.

5. **Test Data** — Should I create sample JSONL files to test against? *Recommendation:* Once Phase 2 is drafted, generate fixtures from real Copilot CLI exports.

---

**Ready to proceed?** I've captured all decisions and architecture. Next steps: you can approve this plan, request refinements, or I can move to implementation. What would you like to adjust?