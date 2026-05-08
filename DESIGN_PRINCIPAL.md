# Dreamer: Design Principles for Chat Analysis CLI

This document outlines the core principles and design patterns used in **dreamer**, a Go CLI tool that reads Copilot chat histories and generates actionable project todos via the Copilot SDK. Our goal is to maintain high code quality while keeping the codebase understandable and easy to extend with new analysis rules, readers, or output formats.

**Related reading**: For cognitive load management principles grounded in working memory research (the 7±2 rule, cyclomatic complexity limits, and code-that-fits-in-your-head heuristics), see [`COGNITIVE_LOAD.md`](./COGNITIVE_LOAD.md).

## 1. Commenting and Documentation Rules

> **Analogy: Code is written once, read many times**
>
> Imagine writing a letter that you will re-read every time you want to act on it. If the letter is hard to parse, you waste time every single read. Code is that letter — and it is often read ten times more than it is written. Style is not about personal taste; it is about reducing the cognitive tax on every future reader.

- **Document Intent (The "Why"):** Comments should explain *why* a decision was made, why a specific trade-off was chosen, or the business logic behind a rule. Avoid explaining *what* the code does—the code itself should be readable enough to convey that.
- **Explain the Unexpected:** Document edge cases, weird boundary conditions, workarounds for external bugs, and non-obvious failure modes.
- **Docstrings for Interfaces:** Every public module, class, and function must have a clear docstring detailing its purpose, inputs, expected outputs, and potential exceptions.
- **Avoid Noise:** Remove stale, commented-out code, and avoid trivial comments like `i = i + 1  # increment i`. If a function requires a comment every two lines, it likely needs refactoring or better variable names.

## 2. Managing Complexity via Deep Modules

> "You do not reduce complexity purely by rewriting confusing code. You reduce it by either making code less confusing or making it less frequently touched—ideally both. The second lever is what deep modules provide."

- **Deep Modules:** Each of dreamer's four vertical slices (Chat Discovery, Reader Layer, Analysis Engine, Todo Generator) should hide internal complexity behind a simple interface.
  - **Chat Discovery**: Simple `DiscoverChats(projectPath) → []ChatSource`. Internally: scan JSONL, SQLite, protobuf; deduplicate; sort.
  - **Reader Layer**: Simple `ReadChat(source) → []ChatMessage`. Internally: handle format-specific parsing, timestamp reconstruction, thread building.
  - **Analysis Engine**: Simple `Analyze(messages, rules) → []Finding`. Internally: Copilot SDK auth, prompt templating, response parsing.
  - **Todo Generator**: Simple `GenerateTodos(findings, projectName) → markdown`. Internally: deduplication, category grouping, state tracking.
- **Information Hiding:** Each module exposes only its primary contract. A caller should never need to understand how JSONL parsing works to use the Reader; they just call `ReadChat()`.
- **Vertical Slice Boundaries:** Chat, Analyzer, Output are separate packages (`internal/chat/`, `internal/analyzer/`, `internal/output/`). Changes to JSONL format don't cascade into analysis logic.

## 3. Tactical vs. Strategic Mode

> "Am I solving today's problem or designing a shape that will survive tomorrow's changes? The answer reveals whether you are in tactical or strategic mode."

- **Tactical Mode:** Focuses on getting a feature out quickly or fixing an immediate bug. While sometimes necessary, accumulating tactical fixes leads to technical debt and a fragile architecture.
- **Strategic Mode:** Focuses on creating a clean, robust design that accommodates future growth and changes. Invest time in finding the right abstraction and designing solid interfaces.
- **Balance:** Strive to operate in strategic mode for core architectural pieces, while allowing tactical mode for isolated, non-critical leaf nodes when time is constrained. Always aim to leave the codebase better than you found it.

## 4. Mitigating "Unknown Unknowns"

"Unknown unknowns" are problems you don't even know you have until they cause catastrophic failures in production. To reduce and prevent them:

- **Fail Fast and Loud:** Validate inputs at the boundaries of your system or modules. If an invalid state is detected, raise an exception immediately rather than proceeding with corrupted data.
- **Strict Typing and Contracts:** Use type hints and explicit data contracts (e.g., Pydantic models, TypedDicts) to enforce data shapes at compile time or system boundaries.
- **Observability by Default:** Log inputs, outputs, state transitions, and errors comprehensively at critical boundaries. When an unknown unknown happens, you need the telemetry to diagnose it.
- **Isolate Side Effects:** Keep pure logic separate from side effects (I/O, database calls, network requests). Pure functions are predictable and easily testable, reducing surprising behaviors.

## 5. ETC: Easier to Change

> "ETC is the single deepest principle underlying almost all good design decisions."

- **The Core Question:** When unsure about a design, ask: "If requirements changed tomorrow, which version of this code would be easier to modify?" That version is better.
- **The Root of Best Practices:** Short functions, good names, and decoupling are tools to make code **Easier to Change**.

## 6. DRY: Don't Repeat Yourself

> "Every piece of knowledge must have a single, unambiguous, authoritative representation within a system."

- **Knowledge, not just Code:** DRY is about avoiding duplication of *knowledge* (business rules, formulas, constraints).
- **Textual vs. Conceptual:** Two pieces of code that look similar are only a DRY violation if they represent the same piece of knowledge. Avoid premature abstraction for logically different things.

## 7. Reversibility: No Final Decisions

> "The mistake is to make 'final' architectural decisions early and then build everything on top of them as if they are permanent."

- **Design for Reversibility:** Avoid locking into specific technologies without clear abstraction layers.
- **Provisional Decisions:** Treat decisions as provisional and record the *why* so they can be rationally revisited later.
- **Configurable Volatility:** Use configuration for things likely to change (endpoints, feature flags, constants).

## 8. DbC: Design by Contract & Crash Early

> "A silent failure that propagates is far harder to debug than an immediate crash at the point of failure."

- **Design by Contract:** Think in terms of preconditions (caller's duty), postconditions (guaranteed result), and invariants (always true state).
- **Crash Early:** Detect errors at the source and halt immediately with a clear error message. Never proceed with corrupted data.

## 9. Design Pattern Selection Guide

Choosing the right pattern significantly impacts readability and extensibility. Use this guide to decide when to apply specific architectural patterns:

### When to use the Workflow Pattern
Use a **Workflow Pattern** when dealing with a sequence of operations (linear or a Directed Acyclic Graph - DAG) that might require retries, checkpointing, or pause/resume capabilities.
- **Signs:** You have distinct "stages" or "steps" that pass data to the next, often involving external service calls or long-running processes.
- **Example:** A data ingestion pipeline processing a file through validation, transformation, and load stages.

### When to use the State Machine Pattern
Use a **State Machine Pattern** when an entity has well-defined, mutually exclusive states and specific rules governing the transitions between them.
- **Signs:** You have complex `if/else` logic checking `status == "X" and previous_status == "Y"`. The behavior of an object changes drastically based on its current state.
- **Example:** Managing the lifecycle of an order (Pending -> Paid -> Shipped -> Delivered) or a complex UI flow.

### When to use Table-Driven Design
Use **Table-Driven Design** (or rule engines) when you have repetitive conditional logic that maps specific inputs or conditions to specific actions, outputs, or strategies.

- **Signs:** You have massive `switch` statements or long chains of `if/elif` that map keys/conditions to values/functions.
- **Example in dreamer:** The analysis engine has many rule categories (Bugs, Performance, Duplication, MissingTests, Architecture, etc.). Instead of a massive `switch Category` block, we define an `AnalysisRule` struct with a prompt template, severity threshold, and enabled flag. Rules are loaded from config YAML. New analysis types are added by editing config, not touching the orchestrator code.

### When to use a Declarative Registry Pattern
Use a **Declarative Registry** (or Plugin/Strategy Registry) when you need a highly extensible system where new behaviors or handlers can be added without modifying the core execution logic.
- **Signs:** You need to support a growing number of integrations, providers, or distinct strategies, and you want to decouple the definition of these components from where they are invoked.
- **Example in dreamer:** The Chat Reader layer uses a registry of format-specific readers (JSONL, SQLite, Protobuf). When adding support for a new chat storage format, register a new `Reader` implementation; the discovery and orchestration logic doesn't change. Similarly, analysis rules are registered and enabled/disabled via config.

## 10. Avoid Cargo-Cult Protection Programming

**Cargo-cult protection programming** is adding defensive checks, retries, or exception handlers just because they "look safe," without understanding the specific risk. This often hides bugs, makes failures silent, and avoids enforcing real invariants. 

Good protection programming is intentional: it identifies likely failure modes, sets clear boundaries, and fails loudly where appropriate.

**How to avoid this practice in dreamer:**
* **Push Validation to the Boundaries:** Validate project paths, chat sources, and config at the CLI boundary (`main.go`, config validation). Once validated, trust that types in core modules are sound.
* **Fail Fast on Copilot SDK Errors:** If Copilot SDK auth fails, crash immediately with a clear error message. Do not silently fall back to a stub analyzer; that hides infrastructure problems until production.
* **Tie Protection to Specific Invariants:** Retry logic for rate-limiting the Copilot SDK is justified and explicit. But don't retry all network errors; distinguish between transient (retry) and permanent (fail fast) failures.
* **Never Catch Generic Errors Silently:** Avoid `_ = err` or generic `recover()`. Either handle the specific error case or let it propagate with context (wrap with `fmt.Errorf()`).
* **Document the "Why":** When adding retry, deduplication, or caching logic, explicitly comment on the specific failure mode (e.g., "Retry on rate limit; concurrent chats may produce duplicate findings").
