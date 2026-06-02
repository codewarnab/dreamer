# Dreamer Testing Guidelines

Tests in `dreamer` serve as **living documentation** of the system's design, invariants, and capabilities. This document outlines the commands, procedures, and critical quality rules for writing and running tests in this repository.

---

## 1. Test Execution Commands

Run these commands to execute various aspects of the test suite:

```bash
# Run the entire test suite
go test ./...

# Run the test suite with the race detector enabled (MANDATORY for concurrent code)
go test -race ./...

# Run a single targeted package
go test ./cmd -run TestJobsCreate

# Generate a detailed test coverage profile
go test -coverprofile=coverage.out ./...

# View the coverage report in a web browser
go tool cover -html=coverage.out
```

---

## 2. Test Infrastructure & Quality Rules

To prevent **"theatrical tests"**—tests that compile and pass but fail to assert actual correctness or mask broken features—you must follow these 10 core quality rules:

### Rule 1: Assert Advertised Behavior, Not Structural Invariants
Every test must call the actual method it is named after and verify its specific, functional output. 
- *Theater:* A test named `TestProviderSupportsParallelSessions` that passes by merely checking `p.ID() == "copilot"`.
- *Correct:* Call `SupportsParallelSessions()` and explicitly assert `true`. 
- *Verification Check:* Delete the body of the function under test. If the test still passes, the test is theatrical and must be rewritten.

### Rule 2: Verify Configuration Propagation End-to-End
When passing configurations (e.g., custom executors, environment variables, commands, or models) to provider factories, assert that these variables reach the final underlying client wrapper.
- Use `acpcore.InspectProvider(p)` on ACP-backed providers to introspect structural variables and verify they match what was passed.

### Rule 3: Use Function Pointers for Option Verification
When testing that a factory or an options-builder maps to the correct internal method, do not merely check that a function field is non-nil. Verify its precise identity using function pointers:
```go
if reflect.ValueOf(gotFunc).Pointer() != reflect.ValueOf(expectedFunc).Pointer() {
    t.Fatalf("unexpected handler function resolved")
}
```

### Rule 4: Clear All Environment Variables in Isolation Helpers
Helpers like `setTestHome` must fully isolate the test process from the host developer environment to prevent flaky tests and false confidence. You must explicitly clear **all** related environment variables:
- `HOME`, `USERPROFILE`, `XDG_CONFIG_HOME`, `CLAUDE_CONFIG_DIR`, `GEMINI_HOME`, `XDG_DATA_HOME`, `OPENCODE_DB`, `KIRO_CLI_DB`, `CODEBUFF_CONFIG_DIR`.

### Rule 5: Avoid Hardcoded Ports, PIDs, and Platform Paths
- **Ports:** Never use hardcoded ports like `:8080` or `127.0.0.1:1`. Bind to `:0` to let the OS assign a free port:
  ```go
  ln, err := net.Listen("tcp", "127.0.0.1:0")
  ```
- **PIDs:** Do not hardcode fictitious process IDs (like `999999`). They can clash or behave unexpectedly. Instead, spawn a child process (e.g., `exec.Command("sleep", "0")`) and use its real PID, or wait for it to exit to get a defunct PID.
- **Paths:** Do not hardcode POSIX paths (like `/usr/bin/true`) in tests. Use `exec.LookPath` to resolve names dynamically or skip the test using `t.Skip` on unsupported platforms.

### Rule 6: Defer Resource Release Immediately After Acquisition
To prevent resource leaks on test failures, always call `defer resource.Close()` immediately after checking for a successful acquisition. 
- **CRITICAL:** Do *not* place defers after a `t.Fatal` or `t.Fatalf` line. If the failure path triggers, the process terminates or skips the remaining lines, meaning the defer is never registered and the resource leaks.

### Rule 7: Use Contexts/Channels for Synchronization, Not Sleep
Never use `time.Sleep` to wait for goroutines, HTTP servers, or background loops to finish. This introduces flakiness and significantly slows down the test suite.
- Use `<-ctx.Done()`, channels (`chan struct{}`), or `sync.WaitGroup` to coordinate execution steps deterministically.

### Rule 8: Test Names Must Match Assertions
Avoid misleading names. If a test is named `TestToMistakesFiltersEmpty`, it must assert that empty findings are indeed excluded from the output. Never name a test after behavior "X" but assert structural invariant "Y".

### Rule 9: Guard Process-Global Test Mutations
If your test mutates package-level global state (e.g., setting a global mock factory like `SetSDKClientFactory` or changing package variables), you **must not** run the test in parallel.
- Document this constraint at the top of the test function with a clear comment: `// Cannot run in parallel: mutates global state`. Do not call `t.Parallel()` on such tests.

### Rule 10: Never Ship Plan Documents with Implementations
Do not check speculative design plans (`PLANS/*.md` or `plan.md`) into your pull requests. They become stale instantly upon code merge. Track future features in external trackers, not as committed markdown files.

---

## 3. Concurrency and Race Detection

Because `dreamer` spawns multiple parallel routines (e.g., chat discovery providers, fsnotify reloading loops, background job runners), concurrency safety is a first-class requirement.

- **Hammering Shared State:** Sequential tests can easily pass even if mutex locks are missing or stubbed out. If a component uses locks or channels, write tests that launch multiple concurrent goroutines (using `errgroup` or `sync.WaitGroup`) to hammer reads/writes in parallel.
- **Race Detection Mandate:** Before checking in any code affecting channels, locks, or goroutines, execute:
  ```bash
  go test -race ./...
  ```
  A single reported data race is treated as a critical block.
