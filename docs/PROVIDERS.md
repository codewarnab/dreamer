# Dreamer Provider System

The `internal/analyzer/providers/` directory contains all LLM provider
implementations.  This document covers the `Provider`/`Session` interface
contract, the two transport archetypes (ACP and CLI), the permission handler
wiring, Phase 2 tool-based finding transports, the provider registry, and the
tests required when adding a new provider.

---

## 1. Core Interfaces

All providers implement two interfaces defined in
[`internal/analyzer/provider.go`](../internal/analyzer/provider.go).

### `Provider`

```go
type Provider interface {
    ID() string
    Start(ctx context.Context) error
    NewSession(ctx context.Context, sessionConfig SessionConfig) (Session, error)
    Close() error
}
```

| Method | Responsibility |
|--------|----------------|
| `ID()` | Returns the canonical provider ID string (e.g. `"claude-cli"`). Must match the constant in `internal/config/` and the registry key. |
| `Start(ctx)` | Validates that the provider binary / SDK is reachable. CLI providers call `exec.LookPath`; ACP providers spawn the long-lived child process here. Must be idempotent — the orchestrator may call it multiple times. |
| `NewSession(ctx, cfg)` | Creates a ready-to-run session. For CLI providers this is cheap (builds argv). For ACP providers it constructs a session wrapper around the shared transport. |
| `Close()` | Tears down any persistent resources (ACP: closes child stdin; CLI: no-op). Must be idempotent. |

### `Session`

```go
type Session interface {
    Run(ctx context.Context, prompt string, timeout time.Duration) (string, error)
    Close() error
}
```

| Method | Responsibility |
|--------|----------------|
| `Run(ctx, prompt, timeout)` | Executes one LLM turn, returning the full assistant text. Applies the timeout internally. Returns `analyzer.ErrNilContext` when `ctx == nil` (never substitute `context.Background` silently — it breaks cancellation propagation). Returns `errs.RateLimit(...)` on rate-limit signals so the orchestrator can back off. |
| `Close()` | Releases session-scoped resources. For most providers this is a no-op. |

### `ModelLister` (optional)

```go
type ModelLister interface {
    ListModels(ctx context.Context) ([]string, error)
}
```

`ModelLister` is an **optional** capability a provider may implement to surface a live model list to the `/api/provider-meta` endpoint.  The handler type-asserts `provider.(analyzer.ModelLister)`; absence falls back gracefully to `defaults.go AllModels` — no silent degradation.

**Contract:**
- Caller must have called `Start()` successfully before calling `ListModels`.
- Implementations must respect `ctx` cancellation and return within its deadline.
- An error return means "I cannot list models right now"; the caller uses the static fallback. Never return a partial list alongside a non-nil error.
- Returned slice is sorted and deduplicated.

Currently implemented by: `opencodehttp` (queries `GET /api/providers` on the running OpenCode server).

The handler caches results for 5 minutes in `analyzer.ModelListCache` (wired via `handlers.Deps.ModelListCache`).  Adding `ModelLister` to more providers only requires implementing the interface in that provider's package — no handler or `Deps` changes needed.

---

## 2. Provider Registration

Providers register themselves via `init()` so that importing the package side
is enough to make it available.  There are two separate registrations:

```go
// 1. Factory registration — enables NewProvider() to build an instance.
analyzer.RegisterProvider(analyzer.ProviderFoo, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
    return New(...)
})

// 2. Metadata registration — required for UI ordering, Phase 2 mode, and capabilities.
analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
    ID:          analyzer.ProviderFoo,
    DisplayName: "Foo (stream-json)",
    Order:       50,          // lower = shown first in UI
    Phase2Mode:  analyzer.Phase2ModeMCP,
    Capabilities: analyzer.ProviderCapabilities{
        BackgroundSafe:           true,
        RequiresNetwork:          true,
        SupportsBackgroundWrites: true,
        NeedsNativeSandbox:       true,
        AllowsCustomCommand:      true,
    },
})
```

The meta-test `TestEveryRegisteredProviderHasMeta` (in
`providers_meta_test.go`) enforces that every provider with a factory also has
metadata.  A new provider that passes only step 1 will fail this test.

**`ProviderCapabilities` field guide:**

| Field | Meaning |
|-------|---------|
| `BackgroundSafe` | Provider can run as a background job. Set `true` for CLI providers only. ACP/SDK providers are not background-safe (they run a long-lived process that cannot safely be shared across jobs). |
| `RequiresNetwork` | Provider needs outbound network for model transport. Should be `true` for all real providers. |
| `SupportsBackgroundWrites` | Provider can perform file writes in `selected_writes` mode when the OS sandbox is active. |
| `SupportsToolPolicy` | Provider supports a tool allowlist. |
| `NeedsNativeSandbox` | Provider requires OS sandbox to be safe for background jobs. CLI providers should set this `true`. |
| `LongLivedProcess` | Provider keeps a persistent child process (ACP pattern). |
| `AllowsCustomCommand` | The `command:` config field is supported by this provider. |

The `TestCLIBackgroundSafeProviders` and `TestACPAndSDKProvidersNotBackgroundSafe`
tests in `providers_meta_test.go` enforce the CLI/ACP split.

---

## 3. Transport Archetypes

There are two transport patterns.  Choose based on what protocol the upstream
provider CLI/SDK speaks.

### 3.1 CLI (stream-JSON) providers — via `cliharness`

**Providers:** `claudecli`, `openclaudecli`, `geminicli`, `codexcli`

The provider binary is invoked as a **short-lived** subprocess per
`Session.Run()`.  The prompt is written to stdin and the response is read from
stdout as newline-delimited JSON (`stream-json` or similar).

**Lifecycle per `Session.Run()`:**

```
exec.CommandContext(ctx, binary, args...)
sandbox.Prepare(cmd, cfg)         ← pre-fork: restricted token / bwrap args
cmd.Start()
sandbox.PostStartWithHandleOrKill(...)  ← post-fork: Job Object / bwrap noop
postStartHook(jobHandle)          ← activity monitor for background jobs
io.WriteString(stdin, prompt)     ← async goroutine
spec.ReadStreamJSON(stdout)       ← parse NDJSON until EOF
cmd.Wait()
postCleanup()  → prepareCleanup() ← teardown in order
```

**Implementing a new CLI provider using `cliharness`:**

1. Create a new package in `internal/analyzer/providers/myprovider/`.
2. Define a `cliharness.Spec`:

```go
var providerSpec = &cliharness.Spec{
    ID:             ID,                          // e.g. "myprovider-cli"
    ErrPrefix:      "myprovider",
    DefaultCommand: defaultCommand,              // func(useNativeSandbox bool) []string
    StartErr:       cliharness.StartErrNotInstalled("Install hint here"),
    CmdStartErr:    cliharness.CmdStartErrPlain("myprovider"),
    WorkingDirFlag: "--cd",                      // or "" if not needed
    ConfigDir:      cliharness.ConfigDirFromEnv("MYPROVIDER_HOME", ".myprovider"),
    ParseErrFirst:  false,                       // true when parseErr > waitErr matters
    ReadStreamJSON: readStreamJSON,              // your NDJSON parser
    InjectPhase2:   cliharness.InjectMCPFlags,  // or InjectCLITools
    ResolveModel:   cliharness.ResolveModel2Tier,
}
```

3. Implement `defaultCommand(useNativeSandbox bool) []string`:
   - When `useNativeSandbox == true`: pass the provider's "skip all permissions"
     flag (e.g. `--dangerously-skip-permissions`, `--yolo`, `--full-auto`).
     The OS sandbox enforces read-only at the kernel level.
   - When `useNativeSandbox == false`: pass the provider's policy-only
     read-only flag (e.g. `--permission-mode plan`, `--approval-mode plan`,
     `--sandbox read-only`).

4. Register in `init()`.  See `claudecli.go` as the canonical reference.

5. Import the package with a blank import in `cmd/root.go` (or the provider
   loader) so `init()` fires.

---

### 3.2 ACP (JSON-RPC 2.0) providers — via `acpcore`

**Providers:** `claudeacp`, `geminiacp`, `codexacp`, `copilotacp`, `kiroacp`, `opencodeacp`

The provider binary speaks the **Agent Client Protocol** (ACP) — a subset of
JSON-RPC 2.0 over stdio.  The child process is spawned **once** at
`provider.Start()` and reused across all sessions.

**Wire protocol (ACP §2):**

```
dreamer → agent:  {"jsonrpc":"2.0","id":"1","method":"initialize","params":{
                     "protocolVersion":1,
                     "clientCapabilities":{"fs":{"readTextFile":true,"writeTextFile":false},"terminal":false}}}

agent → dreamer:  {"jsonrpc":"2.0","id":"1","result":{...}}   ← availableModels, modes, etc.

dreamer → agent:  {"jsonrpc":"2.0","id":"2","method":"session/new","params":{"cwd":"...","mcpServers":[]}}

agent → dreamer:  {"jsonrpc":"2.0","id":"2","result":{"sessionId":"abc"}}

dreamer → agent:  {"jsonrpc":"2.0","id":"3","method":"session/set_model","params":{"sessionId":"abc","modelId":"..."}}
dreamer → agent:  {"jsonrpc":"2.0","id":"4","method":"session/set_mode","params":{"sessionId":"abc","modeId":"..."}}

dreamer → agent:  {"jsonrpc":"2.0","id":"5","method":"session/prompt","params":{"sessionId":"abc","prompt":[{"type":"text","text":"..."}]}}

(async notifications from agent while prompt is in-flight:)
agent → dreamer:  {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"abc","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"..."}}}}
agent → dreamer:  {"jsonrpc":"2.0","method":"session/request_permission","id":"6","params":{...}}
dreamer → agent:  {"jsonrpc":"2.0","id":"6","result":{"outcome":{"outcome":"selected","optionId":"..."}}}

agent → dreamer:  {"jsonrpc":"2.0","id":"5","result":{"stopReason":"end_turn",...}}

dreamer → agent:  {"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"abc"}}  ← best-effort
```

The `readLoop` goroutine demultiplexes incoming lines:
- Lines with an `id` matching a pending `call()` → delivers to the waiting
  channel.
- `session/request_permission` → routed to the active `permissionHandler`.
- `session/update` → text chunks accumulated in the per-session `sessionStream`.

**Fresh session per rule:** Each `Session.Run()` calls `session/new` to get a
fresh `sessionId`.  This prevents transcript history from accumulating across
rules and hitting "Prompt is too long" guards.

**Input cap:** 200 KB (`acpMaxInputBytes`) per prompt body to stay within
model input windows.

**Implementing a new ACP provider:**

1. Create `internal/analyzer/providers/myproviderapc/myprovideracp.go`.
2. In `init()`:

```go
analyzer.RegisterProvider(analyzer.ProviderMyProviderACP, func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
    command := cfg.Command
    if len(command) == 0 {
        command = []string{"myprovider", "--acp"}
    }
    return acpcore.New(acpcore.Options{
        ID:           "myprovider-acp",
        Command:      command,
        Env:          cfg.Env,
        DefaultModel: cfg.DefaultModel,
        Sandbox:      cfg.Sandbox,
        // ... sandbox fields
    })
})
analyzer.RegisterProviderMeta(analyzer.ProviderMeta{
    ID:          analyzer.ProviderMyProviderACP,
    DisplayName: "MyProvider via ACP",
    Order:       90,
    Capabilities: analyzer.ProviderCapabilities{
        RequiresNetwork: true,
        // BackgroundSafe: false (ACP providers are never background-safe)
    },
})
```

3. Add the provider ID constant to `internal/config/config.go` alongside the
   other `ProviderXxx` constants.
4. Add a blank import in `cmd/root.go` so `init()` fires.

---

## 4. Permission Handler Wiring

Every analysis session runs in `read-only` mode.  The permission handler
(`analyzer.DecidePermission`) is the policy layer that intercepts tool calls
from the provider subprocess before they are executed.

### Decision rules (`internal/analyzer/permission.go`)

| Permission kind | Rule |
|-----------------|------|
| `read` | Approved if the resolved path is within the project root. Symlinks are fully resolved. Paths with null bytes, URL schemes, or `://` are denied. |
| `url` | Approved if scheme is `http`/`https`, host is not `localhost`/`.local`, and all DNS-resolved IPs are non-loopback / non-private / non-CGNAT / non-multicast. |
| `shell` | Approved only when the upstream SDK marks it read-only **and** the raw command text does not match `shellWriteIdiomRE` (catches `tee`, `dd`, `sed -i`, `bash -c`, `perl -i`, `>(...)`, etc.). |
| `mcp` / `custom_tool` | Approved only when `ReadOnly == true`. |
| Any other kind | Denied. |

### DNS rebinding note

URL checks resolve DNS at permission time and store the resolved IP in
`PermissionDecision.ApprovedIP`.  This is a **point-in-time** check — it does
not prevent rebinding attacks where a short-TTL record changes between
permission time and dial time.  A future mitigation requires a
dreamer-controlled forward proxy.

### ACP permission wiring

`acpcore.NewSession` builds a closure over the symlink-resolved project root
and wires it as the `permissionHandler`.  On each `session/request_permission`
notification, `translatePermissionRequest` maps ACP wire fields to
`analyzer.PermissionRequest`, calls `analyzer.DecidePermission`, and returns
the appropriate `optionId` from the `options[]` array.

Fail-closed behaviours:
- Empty or malformed permission params → deny (not silent-approve).
- Missing `WorkingDirectory` → deny (project root unknown).
- No `optionId` match → respond `{outcome: "cancelled"}` so the agent can
  finish the turn.

### CLI permission wiring (background mode)

CLI providers always run with their own policy flags.  When using the native
OS sandbox (`useNativeSandbox == true`), `--dangerously-skip-permissions`
bypasses the provider's own guard — the OS sandbox enforces the boundary at
the kernel level instead.  When not using the native sandbox, the provider's
own `--permission-mode plan` / `--sandbox read-only` flag is the guard.

For **background jobs**, `cliharness.adjustForBackground` switches the
provider's permission flag from read-only to permissive so the agent can
execute commands.  The OS sandbox (if active) still enforces the filesystem
boundary.

---

## 5. Phase 2 Tool Transport

Phase 2 is the "finding recording" pass.  Instead of parsing JSON out of an
LLM free-text response, the provider is given a tool it can call to record
findings in a structured JSONL file.

`ProviderMeta.Phase2Mode` declares which transport the provider supports:

| Phase2Mode | Transport | How findings reach dreamer |
|------------|-----------|---------------------------|
| `""` (none) | — | JSON parsing from free-text response (legacy path) |
| `"mcp"` | MCP stdio child (`dreamer mcp-server`) registered via `--mcp-config` | Provider calls the `record_finding` MCP tool |
| `"cli"` | Bash tool invokes `dreamer record-finding` | Provider pipes a finding payload to the dreamer binary |

**MCP transport wiring (CLI providers):**

`cliharness.InjectMCPFlags` appends `--mcp-config <tempfile>` and
`--tools record_finding` (or equivalent) to the command when
`sessionConfig.Phase2.Mode() == Phase2ModeMCP`.  The MCP config JSON points
to the `dreamer mcp-server` child.

**CLI transport wiring:**

`cliharness.InjectCLITools` appends `--tools record_finding` when
`Phase2Mode == Phase2ModeCLI`.

**`Phase2Config` validation:**

Exactly one of `MCP` or `CLI` must be non-nil and
`FindingsOutputPath` must be non-empty.  `Phase2Config.Validate()` enforces
this; providers call it in `NewSession` so misconfiguration fails at the
boundary rather than producing silent empty runs.

---

## 6. Provider Taxonomy

All 16 provider subdirectories follow one of two base patterns:

| Provider | Type | Phase2 | BackgroundSafe | Notes |
|----------|------|--------|---------------|-------|
| `claudecli` | CLI/cliharness | MCP | ✓ | Reference CLI implementation |
| `openclaudecli` | CLI/cliharness | MCP | ✓ | Open-source Claude variant |
| `geminicli` | CLI/cliharness | MCP | ✓ | Gemini CLI |
| `codexcli` | CLI/cliharness | CLI | ✓ | Codex CLI (Rust binary, not Node) |
| `claudeacp` | ACP/acpcore | — | ✗ | claude --acp |
| `geminiacp` | ACP/acpcore | — | ✗ | gemini --acp |
| `codexacp` | ACP/acpcore | — | ✗ | codex --acp |
| `copilotacp` | ACP/acpcore | — | ✗ | GitHub Copilot --acp |
| `kiroacp` | ACP/acpcore | — | ✗ | Kiro --acp |
| `opencodeacp` | ACP/acpcore | — | ✗ | OpenCode --acp |
| `copilotsdk` | SDK (direct) | — | ✗ | GitHub Copilot Go SDK |
| `codebuffsdk` | SDK (direct) | — | ✗ | Codebuff Go SDK |
| `opencodehttp` | HTTP server | — | ✗ | OpenCode local server |
| `flagutil` | (shared util) | — | — | Flag injection helpers |
| `acpcore` | (shared base) | — | — | ACP transport and permission handler |
| `cliharness` | (shared base) | — | — | CLI lifecycle and sandbox wiring |

> **Note (`opencodehttp`):** the auto-start path scans both child stdout and stderr for
> the listen address (first match wins), keeps draining both streams until the provider
> closes, and on Windows resolves npm `.cmd`/`.ps1` shims to the real binary under
> `node_modules/` before spawning. When spawning `opencode serve`, the child process working
> directory is explicitly set to `WorkingDirectory` (`discovery.projectPath`). Additionally,
> an auto-approval background handler continuously polls and approves pending tool permissions
> (`POST /api/session/:sessionID/permission/:requestID/reply` with `{"reply":"allow"}`) so headless runs
> never stall on tool permissions. Detection failures quote a truncated tail of the
> captured server output. See `internal/analyzer/providers/opencodehttp/`.

---

## 7. Required Tests for a New Provider

Add the following tests in `myprovider_test.go` (package `myprovider_test`):

1. **`TestNew_DefaultCommand`** — verify `defaultCommand(true)` contains the
   skip-permissions flag and `defaultCommand(false)` contains the policy flag.
2. **`TestNew_CustomCommand`** — pass a custom `Command` and verify it is
   preserved and not replaced by `DefaultCommand`.
3. **`TestStart_BinaryNotFound`** — set `PATH=""` and verify `Start()` returns
   a non-nil error whose message contains the binary name.
4. **`TestNewSession_SandboxConfig`** — verify `Session.SandboxConfig()` has
   `ProjectDir` set to `WorkingDirectory` and `WritableDirs` includes the
   provider's config home and `os.TempDir()`.
5. **`TestRun_Context_Nil`** — verify `session.Run(nil, ...)` returns
   `analyzer.ErrNilContext`.
6. **Config propagation test** — for ACP providers, use
   `acpcore.InspectProvider(p)` to verify `Command` and `Env` were stored
   correctly without mutation.
7. **Meta-test coverage** — add a blank import of the new package in
   `providers_meta_test.go` so `TestEveryRegisteredProviderHasMeta`,
   `TestProviderMetaOrdersUnique`, `TestProviderMetaDisplayNameNonEmpty`, and
   the CLI/ACP capability split tests cover the new provider.

### Key testing conventions (from `docs/TESTING.md`)

- Never pass a non-nil error to assertions in table-driven tests without
  calling `t.Fatal` / `t.Error`.
- Clear the environment in tests that rely on `PATH` or `HOME` using
  `t.Setenv` (not `os.Setenv`) so the value is restored after the test.
- Use `t.TempDir()` for `WorkingDirectory` values — never a literal `/tmp/foo`.
- Do not use `t.Parallel()` in tests that mutate the provider registry or
  package-level sandbox caches (`bwrapCached`, `bwrapChecked`).
