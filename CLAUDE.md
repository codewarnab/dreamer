# CLAUDE.md

This file provides guidance to Claude Code (`claude.ai/code`) and other AI coding assistants when working with code in this repository.

---

## 1. Build / Test Commands

```bash
make build                    # release build (trimmed, stripped, ~18MB)
make build-dev                # dev build with debug symbols (~26MB)
make build-linux              # cross-compile for Linux
make test                     # full test suite
make test-race                # tests with race detector
make vet                      # go vet
make fmt                      # gofmt -w .
make lint                     # golangci-lint (auto-installs if missing)
make quality                  # dreamer-specific type-aware code quality checks (baseline-aware)
make cover                    # test coverage summary
make cover-html               # test coverage HTML report
make vulncheck                # dependency vulnerability check
make install-hooks            # activate pre-commit hook (.githooks/)
```

Or directly:
```bash
go build -trimpath -ldflags="-s -w" -o dreamer.exe .   # release build
go build -trimpath -o dreamer.exe .                     # dev build
go run . <command> [flags]                              # run dreamer CLI
go test ./...                                           # full test suite
go test ./cmd -run TestName                             # single test
go test -race ./...                                     # race detector
go run ./tools/quality --baseline .quality-baseline.json  # quality checks
```

**Important:** Always use `-trimpath` when building. It strips local filesystem paths from the binary so stack traces don't leak your directory structure and builds are reproducible. The binary warns at startup if built without it. Use `-tags notrimpath` to suppress the check (e.g. for CI fast-builds).

---

## 2. Core Documentation Links

Deep architectural details, coding conventions, and testing quality policies have been modularized under the `docs/` directory to prevent file bloating and stale context. Refer to these files for complete guidance:

1. **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)**: Details the single-binary Go CLI pipeline execution workflow, complete listing of registered Cobra CLI commands, major internal packages (`pipeline`, `chat`, `analyzer`, `sandbox`, `backgroundjobs`, `web`, `jobqueue`, `migrate`), database persistence layouts, finding lifecycles, and configuration overlays.
2. **[docs/TESTING.md](docs/TESTING.md)**: Explains the testing commands and documents the **10 critical "Test quality rules"** to avoid theatrical tests (including assertions, config propagation, clearing environment variables, resource cleanup, and race conditions).
3. **[docs/CONVENTIONS.md](docs/CONVENTIONS.md)**: Defines general Go coding conventions, error-wrapping standards, git commit disciplines (integrating **[docs/HOW_TO_COMMIT.md](docs/HOW_TO_COMMIT.md)**), and strict naming check conventions (idiomatic abbreviations, boolean prefixes, constructor mappings, receiver consistency).
4. **[docs/DESIGN.md](docs/DESIGN.md)**: Details the visual theme (The Verge-inspired), color palette roles, typography rules, component stylings, responsive behaviors, and LLM prompt guidelines. **AI agents must read this file before making any changes to the web dashboard or UI components.**
5. **[docs/CLI.md](docs/CLI.md)**: Comprehensive CLI reference detailing all command options, subcommands, Bubble Tea interactive wizards, and background job scheduling controls.
6. **[docs/API.md](docs/API.md)**: Complete API directory listing web routes, SPA paths, REST endpoints, and the codebase apply/undo safety engine strategies.
7. **[docs/DATAFLOW.md](docs/DATAFLOW.md)**: End-to-end data flow reference tracing every subsystem — config loading, chat discovery, cache checks, transcript preparation, two-phase LLM analysis, output generation, state persistence, daemon loop, background jobs engine, web UI read/write/SSE paths, MCP/CLI finding transport, and OS sandbox containment.
8. **[docs/SANDBOX.md](docs/SANDBOX.md)**: Threat model, per-OS containment strategy (Windows ACL/Job Objects, Linux bubblewrap/seccomp, macOS Seatbelt), what IS and IS NOT sandboxed per platform, known persistence gaps and planned mitigations, the four-function backend contract, and sandbox testing requirements.
9. **[docs/PROVIDERS.md](docs/PROVIDERS.md)**: `Provider`/`Session` interface contract, ACP JSON-RPC wire protocol, CLI stream-JSON harness pattern, permission handler wiring, Phase 2 tool transport (MCP and CLI), provider taxonomy, and required tests for adding a new provider.
10. **[docs/SECURITY.md](docs/SECURITY.md)**: Full security threat model — trust boundaries, secret redaction guarantees, web CSRF/DNS-rebinding protection, sandbox summary, permission handler rules, background job run tokens, known security gaps, and a reference table of security-sensitive files.

---

## 3. Mandatory Pre-Flight Project Skills

Before planning or executing **any** task or modifications in this codebase, you **must** read and align with the specialized project skills located in the `.claude/` directory:

- **[Dreamer Design & Cognitive Load Skill](.claude/skills/dreamer-design/SKILL.md)**: Fundamental guidelines on deep modules, 7±2 rules, newspaper code structures, functional core & imperative shell patterns, and vertical slicing principles.
- **[Code Review & Quality Skill](.claude/skills/code-review-and-quality/skill.md)**: Multi-axis pre-merge review checklists covering correctness, silent error swallowing checks, lock concurrency rules, and resource leak preventions.
- **[Naming Quality Check Skill](.claude/skills/naming-check/skill.md)**: Strict rules detailing Go naming best practices, shadowing preventions, package stutters, and singular/plural mismatch resolutions.

---

> [!IMPORTANT]
> The documentation files under `docs/` are **critical, authoritative context sources** for this project. If any structural, architectural, or design changes are made to the codebase, these markdown files **must be updated immediately** in the same pull request to ensure the developer workspace remains accurate and does not become stale.
