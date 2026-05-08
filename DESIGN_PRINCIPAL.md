# High Code Quality and Design Principles

This document outlines the core principles and design patterns used in this repository to maintain high code quality, reduce cognitive load, and ensure long-term maintainability.

**Related reading**: For cognitive load management principles grounded in working memory research (the 7±2 rule, cyclomatic complexity limits, and code-that-fits-in-your-head heuristics), see [`COGNITIVE_LOAD_PRINCIPLES.md`](./COGNITIVE_LOAD_PRINCIPLES.md).

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

- **Deep Modules:** Aim for modules that provide powerful, broad functionality behind a simple, narrow interface. A deep module hides its implementation complexity from the caller.
- **Information Hiding:** Expose only what is strictly necessary. The less a caller needs to know about the internal workings of a module, the better. This reduces the blast radius of changes.

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
- **Example:** Mapping error codes to specific user-friendly messages, or routing events based on message types where the logic is simple but the variations are numerous.

### When to use a Declarative Registry Pattern
Use a **Declarative Registry** (or Plugin/Strategy Registry) when you need a highly extensible system where new behaviors or handlers can be added without modifying the core execution logic.
- **Signs:** You need to support a growing number of integrations, providers, or distinct strategies, and you want to decouple the definition of these components from where they are invoked.
- **Example:** Registering different LLM providers (OpenAI, Anthropic) or command handlers in a CLI framework.

## 10. Avoid Cargo-Cult Protection Programming

**Cargo-cult protection programming** is adding defensive checks, retries, or exception handlers just because they "look safe," without understanding the specific risk. This often hides bugs, makes failures silent, and avoids enforcing real invariants. 

Good protection programming is intentional: it identifies likely failure modes, sets clear boundaries, and fails loudly where appropriate.

**How to avoid this practice:**
* **Push Validation to the Boundaries:** Validate data at the edge of your system (e.g., API, CLI) using strict typing or schemas. Trust those types in your core logic instead of repeating `is not None` checks everywhere.
* **Tie Protection to Specific Invariants:** Don't add a check unless you can state the specific rule it enforces.
* **Never Catch Generic Exceptions Silently:** Avoid `except Exception: pass`. Catch only specific, expected exceptions. Let unexpected ones crash early.
* **Document the "Why":** When adding a retry or error handler, explicitly comment on the specific failure mode you are expecting and mitigating.
