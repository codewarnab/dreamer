# Dreamer Codebase Analysis

A focused review of `dreamer` against the design principles in `CLAUDE.md`
(Zero Tech Debt, Cognitive Load, and the Design Principles for Chat Analysis CLI).

Scope: every Go source file under `cmd/` and `internal/` plus the rule packs,
config templates, and provider adapters. The codebase is ~11.4k lines of Go
across 58 source files and 21 test files.

The report is organised top-down — biggest, highest-impact items first — so a
reader can stop reading at any point and still have a coherent picture.

## 4. Duplicated knowledge across files (DRY violations)

`CLAUDE.md` calls knowledge duplication out as the *most* important form of
DRY violation. The following are exact-knowledge dups, not coincidentally
similar code:

| Knowledge                                          | Locations                                                                                              |
|----------------------------------------------------|---------------------------------------------------------------------------------------------------------|
| "Rate-limit phrase recognition"                    | `internal/analyzer/providers/codexcli/codexcli.go:262-282` and `internal/analyzer/providers/acpcore/acpcore.go:768-789` — identical pattern lists                                            |
| "Cap prompt to fit input window; preserve header"  | `codexcli.go:capCodexInput` (lines 232-257) and `acpcore.go:capInputBytes` (lines 799-823) — comment in acpcore literally says "Mirrors codexcli.capCodexInput intentionally"                |
| "Merge process env + user-provided env"            | `claudecli.go:appendEnv/appendProcessEnv`, `codexcli.go:mergeEnv` (line 284), `acpcore.go:mergeEnvWithProcess` (line 825)                                                                    |
| "Read JSONL, walk for cwd evidence"                | Four functions in `internal/chat/discovery.go` (see §3)                                                |
| "Finding dedup hash"                               | `analyzer.ComputeFindingHash` (`orchestrator.go:345`) and `output.findingHash` (legacy v0, `generator.go:402`) — both still alive because new code computes hashes one way and we still pattern-match the legacy todos.md format |
| "Lookback window parser"                           | `cmd/lookback.go` is fine on its own, but the `parseLookbackWindow` result is consumed by both the live `executeAnalyze` and the dead `analyzeProject` with slightly different "where the lookback comes from" rules. After §1's cleanup this gets cleaner. |
| "Provider remediation strings"                     | `internal/config/providers.go:RemediationMessage` and the per-provider error strings (e.g. `copilotsdk.go:88` "ensure Copilot CLI is installed and authenticated (run `copilot auth login`)") say the same thing in two places. |

**Recommendation.** Pull rate-limit detection and input-capping into
`internal/analyzer/transport` (or similar) and have both codexcli and
acpcore import them. Pull env merging into `internal/analyzer/providers/procenv`.
The `extractAssistantText` helper in `acpcore.go:736` is also dead — delete it.

---

## 5. Cognitive load: file sizes and function complexity

`COGNITIVE_LOAD.md` sets two limits we can measure:

- "Module dependencies: <7" — broadly OK; the package graph is sane.
- "Cyclomatic complexity below 7" — frequently violated.

The 5 longest source files (by raw line count, tests excluded):

| File                                            | Lines | Notes                                                                  |
|-------------------------------------------------|-------|------------------------------------------------------------------------|
| `internal/chat/discovery.go`                    | 1055  | See §3.                                                                |
| `cmd/analyze_pipeline.go`                       |  611  | See §2.                                                                |
| `cmd/runtime.go`                                |  506  | Dead, see §1.                                                          |
| `internal/output/generator.go`                  |  407  | Mostly fine but blends parsing + rendering + I/O (see §6).             |
| `internal/analyzer/orchestrator.go`             |  382  | Fine; cleanly two-phase. One concern below.                            |

The worst single functions by branching:

- `executeAnalyze` (`cmd/analyze_pipeline.go:60-291`) — ~30 sequential
  control-flow steps in one function. Should be 6-8 named steps in a
  pipeline package.
- `buildRedactedTranscript` (`cmd/analyze_pipeline.go:405-456`) — nested
  for loop with a sanitizer special-case, a redactor, a counter, a logger,
  and a builder. Split into `readAndSanitize(source) []ChatMessage` +
  `renderTranscriptSection(messages, redactor) (string, int)`.
- `Orchestrator.Run` (`internal/analyzer/orchestrator.go:100-165`) —
  phase-1 and phase-2 loops are textually similar. Extract a single
  `runPhase(packs, prompt-builder, parse-fn, post-fn)` helper. Today the
  parse-and-warning-and-fallback logic is duplicated between phases.
- `extractVSCodeWorkspacePath` and `extractClaudeCWDEvidence` in
  `discovery.go` — each is a recursive `any` walker with its own depth
  bound and its own key list. Replace with one parameterised walker.

---

## 6. Functional core / imperative shell mostly OK, with exceptions

Most internal modules are pure-enough and side-effects are at the edges, in
line with `DESIGN_PRINCIPAL.md` §4. Specific call-outs:

**Good:**
- `analyzer.Redactor` is pure (`internal/analyzer/redaction.go`). I/O-free.
- `analyzer.ComputeFindingHash`, `analyzer.FormatTemplate`,
  `analyzer.MergeRulePack` are all pure.
- `state.ChatCacheKey`, `state.HashFile` are pure / well-scoped I/O.
- `parseLookbackWindow` is pure.

**Not great:**
- `output.GenerateTodos` (`internal/output/generator.go:46-91`) mixes
  "read existing todos.md", "compute new content", and "write file" in
  one function. Easier-to-change version: take `existing string` as
  input and return `merged string` as output; have a thin caller do the
  read/write. Mirrors the codebase's own preference for purity stated
  in `DESIGN_PRINCIPAL.md`.
- `output.extractExistingFindingHashes` (line 127) embeds both v1 and
  legacy-v0 hash extraction logic. Once v0 todos.md files no longer need
  back-compat (or once a one-shot migration writes hashes for them),
  the v0 branch can disappear. Today it sits there as dead-on-arrival
  code for any new install.

**Edges that lack a structured layer:**
- `internal/logging/logger.go` writes `LEVEL msg` plain-text lines. The
  callers all hand-format `key=value` pairs (e.g.
  `"analyze begin project=%q path=%q"`). This is one step short of structured
  logging and produces strings nobody can grep reliably. Either commit to
  a structured logger (`log/slog`) and ditch the hand-rolled lines, or
  drop the kv pretense and log prose. Today it's the worst of both.

---

## 7. Configuration: schema split, defaults scattered

`internal/config/loader.go` carries two parallel models for the same
information:

- `cfg.Providers["copilot-sdk"]` is the v1 spec home for Copilot SDK
  config.
- `cfg.Analyzer` is the pre-v1 home and shadows half of it: `Model`,
  `CopilotHome`, `CLIURL`, `UseLoggedInUser`, `AutoStart`.

`ResolveProviderConfig` (loader.go:261-292) reconciles them by building a
"legacy" `ProviderBlock` from `cfg.Analyzer` and *merging* it under any
explicit `providers["copilot-sdk"]` block. So every "where does the model
come from" question now has two answers. The default
`config init` template (`cmd/config.go:12-119`) only writes the new shape.
There is no caller-facing reason to keep `cfg.Analyzer.{Model,CopilotHome,
CLIURL,UseLoggedInUser,AutoStart}`.

**Recommendation.** Drop those fields from `AnalyzerConfig` in the same PR
as §1. Keep `RuleTimeoutSeconds` and `Rules` (the only fields still on the
hot path) and rename the struct to something honest like `AnalyzerOptions`
once the legacy shape is gone.

Other config nits:

- `DefaultRuleTimeoutSecs = 45` and `DefaultRuleThreshold = 0.70`
  (loader.go:16-17) are exported constants that nothing references.
  Either wire them into `applyDefaults` (today rule timeout is sourced
  from per-rule YAML or `cfg.Analyzer.RuleTimeoutSeconds`, not these
  constants) or delete them.
- The default model `"gpt-5.3-codex"` is duplicated: `cmd/config.go:53`
  (template) and `internal/config/loader.go:193` (default in code). If
  one changes and the other doesn't, behaviour changes silently.

---

## 8. Provider abstraction: solid contract, inconsistent boilerplate

`internal/analyzer/provider.go` is one of the cleanest files in the repo —
a 50-line interface definition that 10 providers satisfy. That's the
"Deep Modules" pattern done well.

Two warts:

1. **Registration style is inconsistent.** Most providers register in their
   main file's `init()` (e.g. `kiroacp.go:11`, `codexcli.go:31`,
   `claudeacp.go:11`). Two providers — `copilotsdk` and `claudecli` —
   put the registration in a separate `register.go` file. Pick one.
   The separate-file pattern is cleaner because the provider package
   exports `Options` and `New(...)` without forcing every importer to
   pay for `init()`; recommend converting the other 8 to that shape.

2. **`cmd/runtime.go:165` is the only caller that uses the typed
   constant `analyzer.ProviderCopilotSDK`.** Everywhere else passes the
   string from config through `analyzer.ProviderID(providerID)`. After §1
   the typed constants are essentially unused. They can be kept as a
   spec-checklist (it's nice to have the canonical IDs in one place) but
   should be acknowledged as such — today they look like the primary
   API.

---

## 9. Concurrency

`dreamer` does almost no concurrent I/O. Defensible for v1, but worth
naming:

- **Chat discovery is sequential across 8 source types** (`discovery.go:158-214`).
  These are independent OS walks; running them in parallel would be a
  20-line errgroup change and would materially help on machines with many
  AI tools installed.
- **Per-rule provider calls in phase 1 are sequential** (`orchestrator.go:107-130`).
  Six rules × ~45s/timeout = the bulk of `analyze` wall-clock. Parallel
  with a small semaphore would cut runtime ~3-4×. Risk: provider rate
  limits. Worth a flag, or at minimum a `// TODO`.
- **Daemon iterates projects sequentially** (`cmd/daemon.go:84-122`).
  Probably fine — projects share auth — but again worth documenting the
  decision.

---

## 10. Tests: covering the dead pipeline

`cmd/runtime_test.go` is 803 lines — the single largest test file in the
project — and exercises the dead pipeline (§1). `cmd/analyze_pipeline.go`
is 611 lines of live code with **no dedicated unit test file**. Coverage of
the live path is incidental — through the readers, providers, etc.

After deleting `runtime.go`, write tests for:

- `executeAnalyze` cache-hit / cache-miss / force / dry-run paths.
- `DeriveProjectName` collision behaviour
  (`analyze_pipeline.go:298-313` — exported, no direct test).
- `buildRedactedTranscript` redaction-hit accounting (currently only
  indirectly checked via redaction tests).
- `loggingSession` (currently zero tests).

Today this is mostly invisible because the legacy pipeline shares enough
helpers (`readMessagesFromSource`) with the live one that some coverage
spills over. Once §1 is done, the coverage gap becomes obvious — better
to land §1 and §10 together.

---

## 11. Smaller targeted findings

These are concrete cleanups that don't fit elsewhere.

- **Unused exported API.** Delete (or wire up):
  - `analyzer.MergeRulePack` (`rules.go:99`)
  - `analyzer.SortRulePacks` (`rules.go:150`)
  - `analyzer.SessionTimeout` (`orchestrator.go:377`)
  - `analyzer.MergeRedactionResult` (`redaction.go:75`)
  - `analyzer.extractAssistantText` (`acpcore.go:736`)
  - `chat.discoverChatsFromRoots` (`discovery.go:143`) — only tests
  - `acpcore.transportPermFields` (`acpcore.go:591`) — empty struct
    with a comment claiming it documents something; documents nothing.

- **Magic numbers in the right place are still magic if no one explains
  them.** `claudeMaxCharsPerMessage = 4000`,
  `claudeMaxMessagesPerSource = 250` in
  `internal/chat/readers/claude_sanitizer.go` aren't justified. A one-line
  comment ("≈ 1k tokens × 250 msgs ≈ context budget for one prompt")
  would convert them from magic to communicated.

- **`copilotsdk.buildSDKClientOptions` has a "always-on" override**
  (`copilotsdk.go:202-205`): if the caller asked for
  `UseLoggedInUser=false`, the code silently flips it to `true`. That's
  surprising behaviour for a config flag and exactly the kind of thing
  `*bool` pointers in the config layer are designed to prevent. Either
  honor `false` or remove the field.

- **`fixture_sqlite_drivers_test.go` is 160 lines of test infrastructure
  in the `chat` package proper**, not under `chat/readers`. It logically
  belongs alongside the readers it's faking. Today every non-test build
  of `chat` is unaffected (build tag protects it), but its location
  pollutes the package's mental model.

- **`cmd/config.go` ships a YAML template with `gemini-sdk` advertised
  as a real option but marked "NOT YET IMPLEMENTED in v1"** in inline
  comments (`config.go:73-80`). Either implement it or remove the entry
  from the template — shipping a config that, if uncommented, exits 1
  with "provider not registered" is a footgun.

- **`internal/output/generator.go:286-301` (`renderSnippet`)** prepends
  four-space indentation to every snippet line, including blank ones.
  Pre-formatted blank lines render as `    ` lines and confuse some
  markdown renderers. Trivial: skip indent on empty lines.

- **`internal/state/tracker.go:182-195` (`RepoHeadSHA`)** shells out to
  `git -C wd rev-parse HEAD` and silently returns `""` on any failure.
  That's fine for non-git dirs, but it also masks "git not installed"
  and "wd is corrupt". A `logger.Debug` ping in the failure path would
  pay for itself the first time someone debugs cache-miss behaviour.

- **`Orchestrator.NewOrchestrator` panics on embedded-asset corruption**
  (`orchestrator.go:88`). Acceptable for a `go:embed` failure — that
  *is* a build-time guarantee — but worth a one-line comment explaining
  *why* a panic is safe here.

---

## 12. What is working well

To keep the report honest, the principles that are well-served today:

- **The provider interface** (`internal/analyzer/provider.go`) is exactly
  the deep, narrow shape `DESIGN_PRINCIPAL.md` argues for.
- **The permission decision logic** (`internal/analyzer/permission.go`)
  is a single pure function with one job. Easy to audit, easy to test.
- **Redaction** is similarly narrow, hidden behind a single struct, and
  unit-testable in isolation.
- **The two-phase orchestrator** (`Orchestrator.Run`) is a clean
  expression of the spec's pipeline; the only refactor it needs is the
  phase-de-duplication called out in §5.
- **Rule packs are data, not code** (`internal/analyzer/rules/*.yaml`),
  embedded via `go:embed`. Adding a category is one YAML file. This is
  the "Table-Driven Design" / "Declarative Registry" the design doc
  asks for, executed properly.
- **State persistence has one canonical shape** (`state.State`) with
  json tags, default zero-values, version field. No mutability landmines.

---

## Suggested order of operations

Roughly cheapest-to-do, biggest-leverage first:

1. **§1**: delete `cmd/runtime.go`, `cmd/runtime_test.go`, legacy
   `state.LoadState/SaveState`, and shrink `AnalyzerConfig`. Update
   `CLAUDE.md`. Net deletion: ~1500 lines.
2. **§11 unused API**: prune dead exports while you're in there.
3. **§4 DRY**: pull rate-limit + cap-input + env-merge into shared
   helpers under `internal/analyzer/transport`.
4. **§2 pipeline extraction**: move `executeAnalyze` out of `cmd/` into
   `internal/pipeline` and slim the Cobra files.
5. **§3 discovery registry**: refactor `discovery.go` into one file per
   source + a registry; while at it, parallelize the eight scans.
6. **§10**: backfill unit tests for the live pipeline.
7. **§7 config consolidation**: collapse `AnalyzerConfig` into the
   provider block proper (requires a small backwards-compat migration
   if any user has the old shape on disk).
8. **§6 generator purity**, **§5 long-function splits**, and the smaller
   nits — opportunistic, in PRs touching those files.

Steps 1-3 alone should drop the codebase by ~2000 lines (~17%), make
`cmd/` half its current size, and remove the only currently-documented
behaviour that contradicts the live code.
