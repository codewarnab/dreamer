# How to Commit

This guide outlines the commit message standards and discipline for this repository, based on principles from Mark Seemann's "Code That Fits in Your Head".

## Committing Multiple Changes

While atomic commits (one logical change per commit) are ideal, there are times when you need to commit multiple related changes at once. When committing many things together, **a detailed commit message is absolutely critical** to explain how all the pieces fit together.

## Commit Message Discipline

Commit messages should explain **why**, not just **what**. While the *what* can be inferred from the diff, the *why*—the reasoning, tradeoffs, and intent—is lost if not documented in the commit message.

### A Good Structure

- **First line**: 50 characters or fewer, imperative mood (e.g., "Add validation for reservation overlap").
- **Blank line**: separates subject from body.
- **Body**: 
  - Explain *why* this change was made, what alternatives were considered, and any non-obvious consequences.
  - **For multi-part commits:** Use bullet points to detail each specific change and how it relates to the broader goal of the commit. This helps reviewers and future maintainers untangle what happened.
