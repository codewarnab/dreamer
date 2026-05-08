# Cognitive Load Principles for Dreamer

Additional cognitive load management principles from Mark Seemann's "Code That Fits in Your Head" that complement the core design principles in `DESIGN_PRINCIPAL.md`. These apply specifically to dreamer, a multi-phase Go CLI tool with distinct vertical slices.

## The 7±2 Rule

Human working memory holds approximately 7±2 items simultaneously. Beyond this, comprehension degrades sharply.

**Apply to dreamer:**
- **Config struct fields:** A Config should hold ~7 top-level settings (output dir, log level, frequency, auth method, etc.). Nested settings go into sub-structs (AnalyzerConfig, StateConfig).
- **Function parameters:** Reader methods take max 3 parameters (source, opts, ctx). Use option structs for complex cases.
- **Module dependencies:** Each `internal/` package depends on <7 others. Keep imports minimal. Chat reader doesn't need Analyzer; Analyzer doesn't need Output.
- **CLI command complexity:** The main Cobra command tree stays shallow: `dreamer analyze`, `dreamer daemon`, `dreamer config`, `dreamer ls-chats`. Subcommands avoid deep nesting (max 2 levels).

## Cyclomatic Complexity Below 7

Count independent execution paths through a function. Every `if`, `else`, `for`, `while`, and logical operator (`&&`, `||`) adds one path. Keep total below 7.

**Refactoring strategies for dreamer:**
- **Discovery logic:** Instead of one giant loop checking multiple conditions (`if isJSONL { ... } else if isSQLite { ... }`), use a registry of format detectors that are checked sequentially. Each detector is a simple function.
- **Analysis orchestrator:** Rather than nested loops over rules/messages/findings, extract inner loops into named functions (`analyzeRule()`, `parseResponse()`).
- **Error handling in readers:** Early returns reduce nesting. Before processing a chat, validate it once; if invalid, return early with an error.

**Watch out:** Goal is clarity, not metric worship. A clear 12-line function with straightforward logic beats a confusing 7-line function.

## Newspaper Code Structure

Arrange code like a newspaper: most important information at the top, details deeper down.

**In dreamer files:**
- **`main.go`**: Cobra command definitions and entry points at top. Helper functions below.
- **`internal/chat/discovery.go`**: Public `DiscoverChats()` at top. Specific format detection helpers (jsonlSources, sqliteSources) below.
- **`internal/analyzer/orchestrator.go`**: Public `Analyze()` at top. Private rule execution and response parsing helpers below.
- **Reader implementations** (`jsonl.go`, `sqlite.go`): Public `Read()` interface at top. Format-specific parsing at bottom.

Reader scanning the first 20 lines should understand the module's responsibility.

## Functional Core, Imperative Shell

Push side effects (I/O, API calls, file writes) to system edges. Keep business logic in pure functions.

**Pure function benefits:** Output depends only on inputs, no side effects, trivially testable, understandable in isolation.

**Apply to dreamer:**
- **Chat readers:** `readMessageThread(rawBytes) → []ChatMessage` is pure. Formats message objects with no I/O or state mutation.
- **Analysis logic:** `parseAnalysisResponse(sdkOutput, rules) → []Finding` is pure. Transforms Copilot SDK response into findings.
- **Todo generation:** `generateMarkdown(findings) → string` is pure. No file writes inside this function.
- **Imperative shell:** `main.go` orchestrates I/O: reads files, calls Copilot SDK, writes state.json. Then delegates to pure helpers.

## Walking Skeleton (Incremental Delivery)

Build thinnest possible end-to-end slice first, then add depth incrementally. Integration problems surface immediately when cheapest to fix.

**For dreamer development:**
1. **MVP (Phase 1-2):** Get JSONL chat discovery → read messages → print to stdout. No Copilot SDK yet, no state tracking.
2. **Add Analyzer (Phase 3):** Integrate Copilot SDK, run one hardcoded analysis rule, print findings to stdout.
3. **Add Output (Phase 4):** Write findings as markdown todos, track state.
4. **Add Daemon (Phase 5):** Wrap in periodic loop, add CLI flags.

Each phase is tested end-to-end before moving on. Integration bugs (e.g., state.json permission issues) are caught early.

## Vertical Slices Over Horizontal Layers

Organize by feature, not layer. All code for one feature (chat discovery, analysis, output) lives together.

**Why:** To understand/fix a feature, open one package—no context-switching between architectural layers.

**Dreamer structure:**
- `internal/chat/` — Everything chat-related (discovery.go, readers/, discovery tests)
- `internal/analyzer/` — Everything analysis-related (client.go, rules.go, orchestrator.go, analyzer tests)
- `internal/output/` — Everything output-related (generator.go, deduplicator.go, output tests)
- `internal/config/` — Configuration loading/validation (shared by all slices)
- `internal/state/` — State persistence (shared by all slices)

Each slice owns its tests, data types, and logic. No horizontal "utils" layer of generic helper functions.

## Deep Modules

Provide powerful functionality behind simple, narrow interface. Hide implementation complexity from callers.

**Examples in dreamer:**
- **Chat Discovery:** Complex internal logic (scanning multiple directories, detecting formats, filtering by timestamp). Simple interface: `DiscoverChats(projectPath) → []ChatSource`.
- **Chat Readers:** Complex format-specific parsing. Simple interface: `Read(source) → []ChatMessage`.
- **Analysis Engine:** Complex Copilot SDK integration, templating, response parsing. Simple interface: `Analyze(messages, rules) → []Finding`.
- **Todo Generator:** Complex deduplication and grouping. Simple interface: `GenerateTodos(findings, projectName) → markdown`.

## Magic Numbers

Numbers in code without explanation are cognitive hazards. Use named constants with clear intent.

**Bad:** `if len(response) > 512 { truncate() }`  
**Good:** `const maxFindingLength = 512` then `if len(response) > maxFindingLength { truncate() }`

**Apply to dreamer:**
- Token limits for Copilot SDK: `const maxTokensPerAnalysis = 4000`
- Retry limits: `const maxRetries = 3`, `const retryBackoffMs = 1000`
- Deduplication thresholds: `const minSimilarityScore = 0.85` (for finding dedup)
- Batch sizes for concurrent analysis: `const concurrentAnalyzers = 4`

Every number has a named constant with a comment explaining the choice or source.

## Names Should Reveal Intent

Name should answer: "Why does this exist and what does it do?" Not *how* it does it.

**Common mistakes:**
- Abbreviated: `msg` instead of `chatMessage`, `cfg` instead of `config`, `resp` instead of `analysisResponse`
- Vague: `data`, `result`, `temp`, `obj`, `process()`
- Misleading: `readChat()` that also mutates state; `findFindings()` that also writes to disk
- Inconsistent: using `fetch`, `get`, `load`, `retrieve` interchangeably in Chat readers

**Good names in dreamer:**
- `discoverChats()` — clearly finds chats, doesn't analyze or write
- `analyzeWithCopilot()` — explicitly uses Copilot, not some other model
- `findingsByCategory` — map organized by category, not a flat list
- `stateTrackerForProject()` — tracker scoped to one project, not global

**Diagnostic:** Struggle to name something? The thing itself is often poorly defined.

## Feature Flags for Continuous Integration

Deploy code with incomplete features hidden behind runtime flags. Separates deployment from release.

**Watch out:** Flags never cleaned up become technical debt. Must have expiry plans.

**Dreamer example:** CLI flags like `--enable-analyzer-rule=performance` and `--skip-state-persistence` allow testing new analysis rules without affecting production behavior. Config YAML also defines rule enablement. Long-lived feature flags are marked in code with a comment: `TODO: Remove flag-name after [date/version]`.

## Tests as Living Documentation

Tests are the only documentation automatically verified to be correct. Comments and READMEs go stale; passing tests are accurate by definition.

**Dreamer test strategy:**
- **Unit tests per vertical slice:** 
  - `chat/discovery_test.go` documents expected chat source formats and discovery behavior
  - `analyzer/orchestrator_test.go` documents rule application and finding extraction
  - `output/generator_test.go` documents todo markdown format and deduplication
- **Integration tests:** End-to-end flow from discovery through output, using fixture chat files
- **Tests document:** Expected behavior, input contracts, edge cases (empty chats, malformed JSON, rate limits), error modes
