# Dreamer Refactor Plan

Implementation plan for the remaining items in `analysis.md`. Ordered by
leverage-to-cost ratio. Each phase names exact files, signatures, and
acceptance criteria.

Status as of branch `v1-rework`:
- **Done:** §1 (dead pipeline), §2 (pipeline extraction), §3 (discovery
  registry), §7 (AnalyzerConfig shrunk to `RuleTimeoutSeconds` + `Rules`).
- **Pending:** §4, §5, §6, §10, §11, smaller nits, structured logging.

---

## Phase A — Dead code sweep

One PR. ~30 minutes. No behavior change.

### A1. Delete unused exports (§11)

- `internal/analyzer/rules.go:99` — delete `MergeRulePack`
- `internal/analyzer/rules.go:150` — delete `SortRulePacks`
- `internal/analyzer/orchestrator.go:377` — delete `SessionTimeout`
- `internal/analyzer/redaction.go:75` — delete `MergeRedactionResult`
- `internal/analyzer/providers/acpcore/acpcore.go:591` — delete
  `transportPermFields` (empty struct)
- `internal/analyzer/providers/acpcore/acpcore.go:736` — delete
  `extractAssistantText` (no callers)

Run `go build ./... && go vet ./... && go test ./...` — all must pass.

### A2. Fix the `gemini-sdk` template footgun (§11 nit)

`cmd/config.go:72-80` ships a `gemini-sdk:` block marked
"NOT YET IMPLEMENTED in v1". If a user uncomments it, dreamer exits 1
with "provider not registered".

- Remove the `gemini-sdk:` block (`cmd/config.go:72-80`).
- Remove the menu reference on line 16.

### A3. `NewOrchestrator` panic comment (§11 nit)

`internal/analyzer/orchestrator.go:88` — add one-line comment:

```go
// Safe: LoadDefaultRulePacks only fails on go:embed corruption (build-time guarantee).
```

---

## Phase B — DRY consolidation (§4)

One PR. ~1-2 hours. New `internal/analyzer/transport` package.

### B1. New package `internal/analyzer/transport`

Three files, each pure and unit-tested.

**`transport/ratelimit.go`**

```go
func IsRateLimitMessage(msg string) bool
```

Use the **union** of both pattern lists — codexcli's 7 patterns plus
acpcore's `"overloaded"`. Eight patterns total. The union is harmless
for codex paths because "overloaded" is an Anthropic-only string and
matching it elsewhere won't fire.

**`transport/inputcap.go`**

```go
func CapInputBytes(body string, maxBytes int, truncNote string) string
```

Take `truncNote` as a parameter so callers preserve their own wording
(codex says "codex input cap", acpcore says "agent input cap"). Logic
unchanged from existing `capCodexInput` / `capInputBytes`: preserve the
prompt header before `"\n\nChat transcript follows:\n"` and tail-truncate
the transcript.

**`transport/env.go`**

```go
func MergeWithProcessEnv(extra map[string]string) []string
```

Must support the **empty-value-means-delete** semantics that acpcore
uses today. The richer behavior is harmless for codexcli callers (they
never pass empty values).

### B2. Replace callsites

- `codexcli/codexcli.go` — delete local `capCodexInput`,
  `isRateLimitMessage`, `mergeEnv`. Import transport. Keep
  `codexMaxInputBytes` constant locally; pass truncnote
  `"\n\n[transcript truncated to fit codex input cap]\n"`.
- `acpcore/acpcore.go` — delete local `capInputBytes`,
  `isRateLimitMessage`, `mergeEnvWithProcess`. Truncnote
  `"\n\n[transcript truncated to fit agent input cap]\n"`.
- `claudecli/env.go` — audit; if it duplicates env-merge logic, swap to
  transport.
- `claudecli/claudecli.go` and any other providers with their own
  rate-limit checks — audit and unify.

### B3. Tests for transport

- `transport/ratelimit_test.go` — table-driven against all 8 patterns
  plus a negative.
- `transport/inputcap_test.go` — header-preserved, header-too-big
  tail-truncate, no-marker tail-truncate, body-fits-pass-through.
- `transport/env_test.go` — append, delete-via-empty, preserve order.

**Acceptance:** ~80 lines deleted across providers; ~120 lines added in
transport (including tests).

---

## Phase C — Generator purity (§6)

One PR. ~1 hour.

### C1. Split `GenerateTodos` into pure + shell

`internal/output/generator.go:46` currently bundles read+compute+write.
Split into:

```go
// MergeTodos is pure: takes existing content + new inputs,
// returns merged content + result.
func MergeTodos(
    projectName, existingContent string,
    findings []analyzer.Finding,
    opts GenerateOptions,
) (mergedContent string, result GenerateResult)

// GenerateTodos becomes the thin shell.
func GenerateTodos(
    projectName string,
    findings []analyzer.Finding,
    opts GenerateOptions,
) (GenerateResult, error) {
    todosPath, err := todosPathForProject(...)
    if err != nil { return GenerateResult{}, err }
    existing, err := readExistingTodos(todosPath)
    if err != nil { return GenerateResult{}, err }
    merged, result := MergeTodos(projectName, existing, findings, opts)
    if merged == "" { return result, nil }
    return result, writeFile(todosPath, merged)
}
```

Update `generator_test.go` to test `MergeTodos` directly (no tempdir
required for the bulk of cases). Keep one integration test for the I/O
path.

### C2. Drop v0 hash fallback (§6 second bullet)

`extractExistingFindingHashes` (line 127) currently does v1
hash-marker extraction **and** a v0 category-heading+description
fallback. The v0 branch is dead-on-arrival for any v1 install.

**Decision required (ask user before this step):** is there any user
with pre-v1 `todos.md` on disk?

- **If yes:** add a one-shot migration step in `pipeline.go` — on first
  run with v1 state, walk the existing `todos.md`, compute v0 hashes,
  store them in `state.FindingHashes`. Then delete the v0 branch.
- **If no:** clean break — just delete lines 135-156 in `generator.go`,
  delete `findingHash` (line 403), drop the `crypto/sha256` / `hex`
  imports.

### C3. `renderSnippet` blank-line indent (§11 nit)

`generator.go:294-298` prepends `"    "` to every snippet line, including
blank ones, producing `    ` lines that confuse some markdown renderers.

Skip the indent when `line == ""`. One-line conditional.

---

## Phase D — Orchestrator phase de-dup (§5)

One PR. ~1 hour.

The phase-1 (`orchestrator.go:107-130`) and phase-2 (`:139-163`) loops
are structurally identical: iterate enabled packs, build prompt, call
session, handle rate-limit, parse, post-process, append.

### D1. Extract `runPhase` with generics

```go
func runPhase[T any](
    ctx context.Context,
    session Session,
    packs []RulePack,
    phaseName string,
    buildPrompt func(RulePack) (prompt string, skip bool),
    parse func(raw string, pack RulePack) ([]T, error),
    accept func(pack RulePack, parsed []T),
) []string  // warnings
```

Phase-1 instantiates `T = Mistake`; phase-2 instantiates `T = Finding`.
Single loop body. ~40 lines deleted.

Rate-limit short-circuit, parse-failure warning, and skip-when-empty
behavior all live inside `runPhase`.

### D2. Test coverage

`orchestrator_test.go` must continue to cover:

- Rate-limit short-circuit (both phases).
- Parse-failure warning (both phases).
- Empty-mistakes early-return (phase-2 skipped).
- Dry-run skip (phase-2 skipped).

---

## Phase E — Pipeline test backfill (§10)

One PR. ~2-3 hours.

`internal/pipeline/` currently has only `lookback_test.go` and
`transcript_test.go`. Add:

### E1. `cache_test.go`

- Cache hit: all source hashes match + repo SHA match → early return.
- Cache miss: hash drift on any source.
- Cache miss: repo SHA changed.
- `--force` bypasses cache regardless.

### E2. `projectname_test.go`

- `DeriveProjectName` collision behavior: two projects with the same
  basename → second gets `-<hash>` suffix.
- Absolute-path resolution edge cases.

### E3. `pipeline_test.go`

Full happy path with fakes + tempdir output root:

- `todos.md` written.
- `state.json` updated.
- No error returned.

Plus dry-run path (no writes) and warning-rendering path (parse
failures still produce a warnings section).

### E4. `logs_test.go`

- `loggingSession` records prompt + response to disk.
- Rotation / path conventions sane.

### Fakes

- `analyzer.Provider` / `Session` — `fakeSession` returning canned
  responses keyed by prompt category.
- `chat.ChatSourceProvider` — point `DiscoveryEnvironment` at a tempdir
  with fixture JSONL.

**Acceptance:** `go test ./internal/pipeline/... -cover` shows >70%
statement coverage.

---

## Phase F — Smaller nits

One PR. ~30 minutes total. Opportunistic.

### F1. Justify magic numbers (§11 nit)

`internal/chat/readers/claude_sanitizer.go` — one-line comment above:

```go
// ≈ 1k tokens × 250 msgs ≈ context budget for one analysis prompt.
const claudeMaxCharsPerMessage = 4000
const claudeMaxMessagesPerSource = 250
```

### F2. `copilotsdk` UseLoggedInUser silent override (§11 nit)

`internal/analyzer/providers/copilotsdk/copilotsdk.go:202-205` silently
flips `UseLoggedInUser=false` to `true`.

Two acceptable resolutions:

- **Preferred:** honor `false` — if the SDK supports it, just pass it
  through.
- **Fallback:** emit
  `logger.Warn("copilotsdk: forcing use_logged_in_user=true; SDK requires it")`
  so the override is observable.

Pick one. Don't keep both `*bool` config + silent override.

### F3. `RepoHeadSHA` debug log on failure (§11 nit)

`internal/state/tracker.go:182-195` silently returns `""` on git
failure. Accept an optional `*logging.Logger`; if non-nil:

```go
logger.Debug("repo head SHA failed", "wd", wd, "err", err)
```

Update callers in `internal/pipeline/cache.go` to pass the pipeline's
logger.

### F4. Default model duplication (§7 nit)

`"gpt-5.3-codex"` is duplicated in `cmd/config.go:53` (template) and
`internal/config/loader.go:193` (default in code).

Define one source of truth:

```go
// internal/config/loader.go
const DefaultModel = "gpt-5.3-codex"
```

Then reference it from both — the template builder in `cmd/config.go`
interpolates via `fmt.Sprintf`, not a hard-coded string.

### F5. Unreferenced default constants (§7 nit)

`internal/config/loader.go:16-17` defines `DefaultRuleTimeoutSecs = 45`
and `DefaultRuleThreshold = 0.70` but nothing references them.

Pick: either wire them into `applyDefaults` (so rule timeout always has
a floor) or delete them. Default to deletion unless `applyDefaults`
actually needs them.

---

## Phase G — Structured logging migration (§6 last bullet)

One PR. ~2-3 hours. **Largest remaining item.** Do last — touches many
files and conflicts most with other refactors.

### Decision

Commit to `log/slog`, or drop the `key=value` pretense and log prose.
**Recommended:** `log/slog`. Today's log lines look structured but
aren't grep-reliable; slog gives proper kv parsing for free.

### G1. Audit scope

```bash
grep -rn 'logger\.\(Info\|Warn\|Error\|Debug\)' internal cmd | wc -l
```

### G2. Replace `internal/logging/logger.go`

Use `slog.Handler` writing to file + stderr. Keep the existing `Logger`
interface as a thin wrapper so callers don't all change at once.

### G3. Convert callers in batches

1. `cmd/` and `internal/pipeline/` first (highest signal — analyze /
   daemon lifecycle).
2. `internal/chat/discovery.go` next.
3. Providers last (low log volume).

### G4. Acceptance

- Every log line is `slog`-formatted (kv parsed, not hand-built).
- `dreamer.log` is grep-friendly.
- One integration test reads back a log line and asserts
  `level=info msg=... project=...`.

---

## Suggested PR ordering

| PR  | Phase | Effort   | Risk      | Notes                                                |
|-----|-------|----------|-----------|------------------------------------------------------|
| PR1 | A     | ~30 min  | Near-zero | Dead code sweep + gemini-sdk template + panic note   |
| PR2 | B     | ~1-2 hrs | Low       | Transport package + DRY consolidation                |
| PR3 | F     | ~30 min  | Near-zero | Small nits bundle                                    |
| PR4 | C     | ~1 hr    | Medium    | Generator purity split. Ask re: v0 fallback first    |
| PR5 | D     | ~1 hr    | Low       | Orchestrator phase de-dup with generics              |
| PR6 | E     | ~2-3 hrs | Low       | Pipeline test backfill                               |
| PR7 | G     | ~2-3 hrs | Medium    | Structured logging migration                         |

## Net impact estimate

- ~−400 to −500 lines of production code (deletions dominate).
- ~+300 lines of new tests.
- New `transport` package ~+120 lines including tests.
- New pipeline tests ~+200-300 lines.

## Open questions

1. **Pre-v1 `todos.md` migration** (Phase C2): do any users have
   pre-v1 todos.md on disk that need v0 hash migration, or is a clean
   break acceptable?
2. **`copilotsdk.UseLoggedInUser` semantics** (F2): can the SDK
   authenticate with `UseLoggedInUser=false`, or is the current silent
   flip load-bearing?
3. **Structured logging commitment** (Phase G): proceed with `log/slog`
   migration, or defer indefinitely?
