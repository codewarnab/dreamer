# Dreamer Coding & Development Conventions

This document outlines the coding standards, naming conventions, error-handling conventions, and git commit disciplines followed in the `dreamer` repository.

---

## 1. General Go Coding Conventions

- **Newspaper Structure:** Arrange code files logically with the most important public interfaces and constructors at the top, and low-level details (private helper methods, utility functions) lower down. A reader scanning the first 20 lines of a file should understand its responsibility.
- **Intent-Driven Comments (The "Why"):** Comments should document *why* a design decision was made, not *what* the code does. The code itself must be clear enough to convey the "what". Document edge cases, external bug workarounds, and non-obvious failure modes. Avoid noisy or trivial comments.
- **DRY (Don't Repeat Yourself):** Avoid duplicating *knowledge* (business logic, formulas, access policies). If the same block of boilerplate is copied across multiple files, extract a shared helper.
- **Fail Fast and Loud:** Validate inputs at boundaries (e.g. user CLI entry points, config loading). Once validated, trust the types in internal core modules. When an invariant fails, crash or return a loud error immediately rather than propagating corrupted state.
- **Avoid Cargo-Cult Fallbacks:** Do not silently fall back to stubs or swallow errors. If a provider connection fails, raise the error. If a strict capability cannot be honored, return an error.

---

## 2. Naming Conventions (Naming Check Skill)

We follow strict naming rules to maintain codebase clarity and minimize cognitive load. Scan all changed files during reviews to ensure these standards are met.

### Accepted Idiomatic Abbreviations (Do NOT flag these)
The following short names are standard Go and require no expansion:

| Abbreviation | Meaning |
| :--- | :--- |
| `cfg` | configuration |
| `msg` | message |
| `ctx` | context.Context (always the first parameter) |
| `err` | error |
| `ok` | bool check result (e.g., from maps or channels) |
| `wg` | sync.WaitGroup |
| `mu` | sync.Mutex / sync.RWMutex |
| `ts` | timestamp |
| `req` / `resp` | request / response |
| `dst` / `src` | destination / source |
| `buf` | byte/string buffer |
| `idx` | slice or array index |
| `ch` | channel |
| `fd` | file descriptor |
| `fn` | function |
| `db` | database |
| `rw` | io.ReadWriter or gin.ResponseWriter |
| `n` | count |
| `i`, `j`, `k` | loop index variables |
| `v` | value in range loops |
| `r` / `w` | io.Reader / io.Writer |
| `s` | string |
| `p` | pointer or path string |
| `t` | testing.T instance |

*Note: Single-letter receiver names (e.g. `s *Server`, `o *Orchestrator`) are encouraged and standard.*

### Naming Anti-Patterns (Always flag these)
- **Cryptic Non-Standard Abbreviations:** Avoid names like `sr`, `tr`, `pc`, `mc`, `ar`. If it isn't in the list above, write the full word (e.g. `sessionResult`, `chatTracker`, `permissionCheck`).
- **Vague Generics:** Never use names like `data`, `result`, `temp`, `obj`, `item`, `val`, `thing`, `doStuff()`, `process()`. Choose descriptive names that reveal intent.
- **Hungarian Notation:** Do not prefix variables with type signifiers (e.g., `strName`, `bIsActive`, `iCount`, `arrItems`).
- **Boolean Naming:** Booleans must be prefixed with `is`, `has`, `should`, `can`, `ok`, or `exists` (e.g. `isActive`, `hasErrors`, `shouldRetry`). Struct fields that read naturally in conditions (e.g. `cfg.Enabled`) are permitted.
- **Negative Boolean Names:** Avoid naming booleans with negative assertions like `isNotReady`, `hasNoErrors`, `disableCache`. Conditional logic like `!isNotReady` is extremely hard to read. Use positive naming instead (`isReady`, `hasErrors`, `cacheEnabled`).
- **Receiver Name Drift:** Do not use different receiver names for the same struct type across different methods (e.g., `srv *Server` in one file and `s *Server` in another). Select one receiver name per struct and use it consistently.
- **Package Stutter:** Avoid repeating the package name in struct and type definitions.
  - *Bad:* `config.Config`, `chat.ChatMessage`, `sandbox.Sandbox`
  - *Good:* `config.Settings`, `chat.Message`, `sandbox.Env`
- **Constructor Inconsistency:** We use `New*` for constructors returning instantiated values or objects. We use `Create*` ONLY for factories that introduce side effects (e.g. creating a physical file, registering OS schedules, spawning processes).
- **Sentinel Errors Casing:** Exported sentinel errors must follow capital camel case and be prefixed with `Err` (e.g., `ErrNotFound`). Unexported sentinels are a bug because they cannot be verified across package boundaries.
- **Shadowed Builtins:** Never use Go built-in function names (`len`, `cap`, `new`, `make`, `copy`, `close`, `append`, `panic`, `recover`) as variable names.
- **Time/Duration Disambiguation:** Use `time.Time` fields for absolute dates and end them with the suffix `At` (e.g., `createdAt`, `finishedAt`). Do not use vague names like `timeout` or `delay` for points in time (those imply `time.Duration`).
- **Plural vs. Singular Collections:** Slices, maps, and channels holding multiple values must use plural names (e.g., `sources` instead of `source`, `findings` instead of `finding`).

---

## 3. Error Handling Conventions

- **Wrap Errors at Boundaries:** Propagate errors with operational context by wrapping them. Never return raw errors from deep internal packages directly to boundaries.
- **Standard Wrapping Pattern:**
  ```go
  return fmt.Errorf("verb noun %q: %w", name, err)
  ```
- **NO Silent Swallowing:** Every error at a system boundary must be checked or handled. 
  - *Anti-pattern:* `if err != nil { log.Warn(...) }` without returning the error.
  - *Anti-pattern:* Assigning an error to `_` or `err :=` and failing to check it.
  - *Anti-pattern:* Silent degraded fallbacks. If a file cannot be parsed, return the error or log a prominent warning. Do not silently degrade to an empty state.

---

## 4. Git Commit Message Standards (HOW_TO_COMMIT.md)

Commit messages must document **why** a change was made, not just **what** changed. The *what* is visible in the diff; the *why* represents the architectural trade-offs, bug triggers, and long-term design intent.

- **First Line (Subject):** Limit to 50 characters or fewer, written in the imperative mood, starting with a capital letter, and ending without a period.
  - *Correct:* `Add validation for configuration overlay overlap`
  - *Incorrect:* `Added some validation checks.`
- **Body Structure:** Separate the subject line from the body with a blank line. 
- **Explaining the Trade-offs:** The body must explain the reasoning, the edge cases handled, and why the specific implementation was chosen over alternatives.
- **For Multi-Part Commits:** When committing multiple cohesive changes at once, **a detailed commit message is critical**. Use bullet points in the body to detail each specific component change and show how they fit into the broader goal of the commit.
