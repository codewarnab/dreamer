# Cognitive Load Principles (Code That Fits in Your Head)

Additional cognitive load management principles from Mark Seemann's "Code That Fits in Your Head" that complement the core design principles in `DESIGN_PRINCIPLES.md`.

## The 7±2 Rule

Human working memory holds approximately 7±2 items simultaneously. Beyond this, comprehension degrades sharply.

**Apply to**:
- Function parameter lists: keep to 3-4 items; group related parameters into value objects
- Local variables: a function with 8+ local variables exceeds cognitive capacity
- Module dependencies: each module should depend on fewer than 7 others
- Nested structures: deep nesting forces tracking multiple context levels at once

## Cyclomatic Complexity Below 7

Count independent execution paths through a function. Every `if`, `else`, `for`, `while`, `and`, `or` adds one path. Keep total below 7.

**Refactoring strategies**:
- Extract validation chains into validator objects
- Use early returns (guard clauses) to reduce nesting
- Split complex functions into focused helpers

**Watch out**: Goal is clarity, not metric worship. A clear 12-line function beats a confusing 7-line function.

## Newspaper Code Structure

Arrange code like a newspaper: most important information at the top, details deeper down.

**In practice**:
- Public APIs and high-level orchestration at top of files
- Private helpers and implementation details below
- Reader scanning top 20 lines should understand module purpose

## Functional Core, Imperative Shell

Push side effects (I/O, API calls, file writes) to system edges. Keep business logic in pure functions.

**Pure function benefits**: output depends only on inputs, no side effects, trivially testable, understandable in isolation.

**Apply to this codebase**:
- Prompt builders: pure transformations from context to text
- Validators: pure transformations from JSON to validation result
- Stage runners: handle I/O at boundaries, delegate logic to pure helpers

## Walking Skeleton (Incremental Delivery)

Build thinnest possible end-to-end slice first, then add depth incrementally. Integration problems surface immediately when cheapest to fix.

**When adding new stage/feature**:
1. Implement minimal end-to-end flow first (stub logic is fine)
2. Verify integration with existing stages
3. Add real logic incrementally
4. Each increment leaves system in working state

## Vertical Slices Over Horizontal Layers

Organize by feature, not layer. All code for one feature (API → logic → data) lives together.

**Why**: To understand/fix a feature, open one folder—no context-switching between architectural layers.

**This codebase**: Each stage folder (`stage1/`, `stage2/`, `stage3/`) is already a vertical slice containing prompts, runners, validators, and context builders.

## Deep Modules

Provide powerful functionality behind simple, narrow interface. Hide implementation complexity from callers.

**Examples in this codebase**:
- Stage runners: complex internal orchestration, simple `run_stage(topic, context) -> output` interface
- Prompt builders: complex assembly logic, simple `build_prompt(context)` interface
- Context renderers: complex compaction logic, simple `render(artifact)` interface

## Magic Numbers

Numbers in code without explanation are cognitive hazards. Use named constants with clear intent.

**Bad**: `if score > 42: award_bonus()`  
**Good**: `BONUS_THRESHOLD = 42` then `if score > BONUS_THRESHOLD: award_bonus()`

**Apply to**: Token limits, window sizes, retry counts, complexity thresholds.

## Names Should Reveal Intent

Name should answer: "Why does this exist and what does it do?" Not *how* it does it.

**Common mistakes**:
- Abbreviated: `usr` instead of `user`, `cfg` instead of `config`
- Vague: `data`, `result`, `temp`, `obj`
- Misleading: `get_user()` that also deletes sessions
- Inconsistent: using `fetch`, `get`, `load`, `retrieve` interchangeably

**Diagnostic**: Struggle to name something? The thing itself is often poorly defined.

## Feature Flags for Continuous Integration

Deploy code with incomplete features hidden behind runtime flags. Separates deployment from release.

**Watch out**: Flags never cleaned up become technical debt. Must have expiry plans.

**This codebase**: CLI flags like `--stage3-context-mode` and `--stage2-planning-mode` control behavior without code changes.

## Tests as Living Documentation

Tests are the only documentation automatically verified to be correct. Comments and READMEs go stale; passing tests are accurate by definition.

**This codebase**:
- Offline prompt assembly tests document expected prompt structure
- Stage runner tests document input/output contracts
- Validator tests document schema requirements
