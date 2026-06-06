# Plan: Fresh Model Lists & Settings UI Model Picker

## Problem

Two related issues:

1. **Stale static defaults.** `internal/config/defaults.go` contains hardcoded `AllModels` slices that
   go stale as providers release new models. The `ModelListCache` is purely in-memory, so every daemon
   restart starts cold — the datalist in the settings UI falls back to whatever was last committed in
   `defaults.go` until a background fetch succeeds (takes one page-load cycle).

2. **Settings UI model picker is invisible.** The `<datalist>` on the model input field only surfaces
   as browser autocomplete. When the live list has *just two entries* (e.g. kiro-acp returns
   `claude-sonnet-4` and `claude-sonnet-4.5`) there is no visible affordance — the user sees a blank
   text box and has no idea what values are valid.

---

## What We Have Today

| Layer | Mechanism | Freshness |
|---|---|---|
| `defaults.go` `AllModels` | Hard-coded slice, committed to repo | Stale until manually updated |
| `ModelListCache` | In-memory TTL=5 min, keyed by provider ID | Cold on every restart; populated by background goroutine on first `/api/provider-meta` hit |
| `acpcore.ListModels` | `session/new` → `availableModels` JSON field | Live, but only works when provider CLI is running |
| `opencodehttp.ListModels` | HTTP GET `/api/providers` | Live, HTTP-based, works whenever server is up |
| `settings.js` `providerModelMap` | JS-side hardcoded fallback | Last-resort; never authoritative |

The `enrichWithLiveModels` helper in `handlers/providers.go` already does the right thing: cache-hit →
return live; no cache → spawn background fetch, return static fallback for this request. The gap is
that the static fallback is never updated by successful fetches, so a restart always shows stale data
for at least one page load, and the UI doesn't show the list visually.

---

## Plan

### Part 1 — Persist the Live Model List to Disk

**Goal:** When a live `ListModels` fetch succeeds, persist the result so the next daemon restart uses
it as the initial fallback instead of the hardcoded slice.

**Files touched:**
- `internal/analyzer/modellistcache.go` — add `Load(path)` / `Save(path)` methods
- `internal/web/handlers/providers.go` — call `Save` after a successful background fetch
- `internal/web/server.go` — call `Load` at startup to warm the cache from disk

**Detail:**

```
ModelListCache.Load(dir string) error
  - reads <dir>/model-list-cache.json
  - format: { "provider-id": { "models": [...], "fetched_at": "RFC3339" } }
  - skips entries older than 7 days (generous — model lists are stable for days/weeks)
  - safe to call even if file doesn't exist (returns nil)

ModelListCache.Save(dir string) error
  - writes same JSON atomically (write to .tmp, rename)
  - called from enrichWithLiveModels background goroutine after Set()
  - failure is logged but never fatal
```

The cache file lives alongside other daemon state files (next to `state.json` in `output_root` or a
dedicated `<output_root>/.dreamer/` dir). The exact path is passed in from `server.go` where
`output_root` is known.

**Why not update `defaults.go` automatically?**  
`defaults.go` is compiled into the binary — it can't be patched at runtime. The cache file is the
right mechanism. `defaults.go` becomes the "factory default of last resort" and the cache file is
the "learned best-known list".

---

### Part 2 — Update `defaults.go` Static Lists to Match Current Live Data

**Goal:** Make the factory defaults as fresh as possible so cold starts are useful even without the
cache file.

**Providers to update based on live ACP probes (run `go run ./tools/listkirmodels/`):**

| Provider | Current `AllModels` | Update to |
|---|---|---|
| `kiro-acp` | `["claude-sonnet-4-5-20250929"]` | `["claude-sonnet-4", "claude-sonnet-4.5"]` + keep old as fallback |
| `opencode-acp` | `["deepseek-v4-flash"]` | expand from the big list returned by opencode ACP (see JSON in previous session) |
| `claude-cli` | already has 4 models | verify against `claude --version` |
| `gemini-acp` | 3 models | expand if gemini ACP advertises more |

For `kiro-acp` specifically: the default model should be updated from `claude-sonnet-4-5-20250929`
(old date-versioned slug) to `claude-sonnet-4.5` (what the live ACP actually reports). Keep the old
slug in `ModelFallbacks` so existing configs that saved the old name still work.

```go
// internal/config/defaults.go — kiro-acp entry
RegisterProviderDefaults(ProviderKiroACP, ProviderDefaults{
    DefaultModel:   "claude-sonnet-4.5",
    AllModels:      []string{"claude-sonnet-4.5", "claude-sonnet-4"},
    ModelFallbacks: []string{"claude-sonnet-4-5-20250929"}, // legacy slug
    DefaultSandbox: "auto",
    Remediation:    "Install the Kiro CLI and ensure `kiro-cli acp` starts cleanly.",
})
```

---

### Part 3 — Settings UI: Visible Model List

**Goal:** Replace the invisible `<datalist>` with a visible dropdown/chip strip so users can see and
click available models without having to know names in advance.

**Current behaviour:** `<input type="text" list="provider-model-options">` — browser renders a plain
text box; the datalist only appears if the user starts typing, and many browsers don't show it at all
when the field is blank.

**Proposed UI:**

```
MODEL                                    providers.*.model
┌─────────────────────────────────────────────────────────┐
│  claude-sonnet-4.5                              ▾       │   ← styled input
└─────────────────────────────────────────────────────────┘
  Quick-pick:  [claude-sonnet-4]  [claude-sonnet-4.5]        ← pill chips
  Pick a known model or enter a provider-specific model name.
```

The "quick-pick" chips only render when `modelOptions(selectedProviderID()).length > 0`. Clicking a
chip sets `values.providers[selectedProviderID()].model = chip` and calls `autofillApiKeyEnv()`.

Also add a small source badge next to the "MODEL AND LIMITS" card title indicating whether the list
came from a live fetch or the static fallback:

```
model and limits     [KIRO-ACP]     ○ live models          ← when cache hit
model and limits     [KIRO-ACP]     ● default list         ← when using fallback
```

This requires a new field in the `providerMetaEntry` response:

```go
type providerMetaEntry struct {
    ...
    ModelSource string `json:"model_source"` // "live" | "cache" | "static"
}
```

Set in `enrichWithLiveModels`:
- `"live"` — returned from cache (was fetched live at some point)
- `"static"` — returned from `defaults.AllModels` (no cache entry yet)

The `loadProviderMeta` in `settings.js` already stores the full meta entry in `providerMetaMap[id]`,
so `providerMetaMap[selectedProviderID()].model_source` is immediately available to the template.

**Files touched:**
- `internal/web/handlers/providers.go` — add `ModelSource` to `providerMetaEntry`
- `internal/web/templates/partials/settings/assistant_panel.html` — replace datalist with chip strip + source badge
- `internal/web/static/js/pages/settings.js` — add `setModel(model)` helper (one-liner, calls autofill)

---

### Part 4 — Also Update `settings.js` Fallback Map

The JS-side `providerModelMap` is a last-resort fallback used only when `/api/provider-meta` is
completely unreachable. Update it to match the updated `defaults.go`:

```js
"kiro-acp": ["claude-sonnet-4.5", "claude-sonnet-4"],
```

This is purely defensive; the live path should always win.

---

## Execution Order

1. **Part 2** (update `defaults.go` + `settings.js`) — zero-risk, pure data change, immediate benefit
   on cold start. Update `kiro-acp` default model and model list first.

2. **Part 3** (settings UI chips + source badge) — isolated frontend change, no backend required for
   the chips (data already arrives via `/api/provider-meta`). Add `ModelSource` to the Go struct at
   the same time.

3. **Part 1** (disk persistence) — more involved; requires new file I/O and startup wiring. Do last
   so the simpler parts ship first and the cache path has tests.

---

## Files Summary

| File | Change |
|---|---|
| `internal/config/defaults.go` | Update `kiro-acp` (and others) `AllModels` + `DefaultModel` |
| `internal/analyzer/modellistcache.go` | Add `Load` / `Save` methods |
| `internal/web/handlers/providers.go` | Add `ModelSource` field; call `Save` after background fetch |
| `internal/web/server.go` | Call `cache.Load(outputRoot)` at startup |
| `internal/web/templates/partials/settings/assistant_panel.html` | Model chip strip + source badge |
| `internal/web/static/js/pages/settings.js` | `setModel()` helper; update fallback map |
| `docs/ARCHITECTURE.md` | Document cache file location + `model_source` field |

---

## Non-Goals

- Polling: the background goroutine on page-load is enough. No need for a periodic refetch daemon.
- Editing the binary default list via the UI: `defaults.go` is the floor; the cache file is the
  ceiling. Users who need a model not in either list can always type it freeform.
- Sorting/filtering the chip strip: keep it simple — render in the order the provider returns them.
