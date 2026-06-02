# Dashboard UI Findings

## Two Highlighted Elements (from screenshot)

---

### 1. Status Pills (top-left, orange-circled near nav)

There are actually **two pill badges** sitting side by side in the topbar.

**Daemon status pill**
- Shows `"running"` or `"idle"` depending on whether a run is active.
- Green (`pill--healthy`) when running, yellow-orange (`pill--warn`, `#ffd166`) when idle.
- Driven by SSE events: turns green on `run.start`, turns yellow on `run.done` or `run.error`.
- The screenshot shows the **warn/yellow** state, meaning the daemon is currently idle (never run).

**SSE connection pill** (smaller, next to it)
- Shows `"sse: ok"` or `"sse: reconnecting"`.
- Same color logic — green when the server-sent event stream is connected, yellow when disconnected/reconnecting.

**Defined in:**
- `internal/web/templates/layout.html` — markup and Alpine.js bindings
- `internal/web/static/css/dreamer.css` — `.pill`, `.pill--healthy`, `.pill--warn` classes
- `internal/web/static/js/app.js` — `appState()` manages the `status` field reacting to SSE events

---

### 2. "Run Now" Button (top-right, orange-circled green element)

This is a **primary action button** styled as a rounded green pill, not a toggle switch despite its appearance.

- Label: `"run now"` (switches to `"queuing..."` while busy).
- Clicking it calls `runNow()` in `appState()`, which fires a POST request:
  - To `/api/projects/:id/run` if on a project page.
  - To `/api/run` for a global run across all projects otherwise.
- Disabled while a run is already queued (`busy` flag).
- Color: mint green (`--jelly-mint: #3cffd0`) via `.btn--primary`.

**Defined in:**
- `internal/web/templates/layout.html` — `<button class="btn btn--primary">` in the topbar
- `internal/web/static/js/app.js` — `runNow()` function inside `appState()`
- `internal/web/static/css/dreamer.css` — `.btn`, `.btn--primary` classes

---

## Summary

| Element | What it is | Current state in screenshot |
|---|---|---|
| Yellow pill (top-left) | Daemon status indicator | `idle` / warn — daemon has never run |
| Smaller pill (next to it) | SSE connection status | Likely `sse: ok` or `sse: reconnecting` |
| Green pill (top-right) | "Run Now" trigger button | Ready to queue a run |
