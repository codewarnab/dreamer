# Plan: Settings Assistant Panel — Two-Panel Provider Selector

## Problem

The current `provider-grid` in `settings_assistant_panel.html` renders every
provider as a flat tile via `repeat(auto-fit, minmax(170px, 1fr))`. Today that
is 13 tiles across two rows. As new provider *families* land (e.g. `opencode`,
`codebuff`, future additions), the grid adds more tiles and becomes a wall —
no hierarchy, no way to distinguish families from variants, hard to scan.

---

## Goal

Replace the flat tile grid with a **two-panel selector** that scales
horizontally to any number of families and vertically to any number of
variants per family, without growing the viewport footprint.

- Left rail: scrollable list of provider *families* (one row each)
- Right detail: variants (ACP / CLI / SDK) for the selected family as a small
  radio/pill group, plus the existing per-provider config cards below

Adding a new family = one new row in the left rail.
Adding a new variant = one new pill in the right panel.
Viewport never grows.

---

## Background Knowledge

From `docs/PROVIDERS.md` §6 (Provider Taxonomy), providers follow a naming
convention: `<family><transport>` — e.g. `claude-cli`, `claude-acp`,
`codex-cli`, `codex-acp`. Transports are: `cli`, `acp`, `sdk`, `server`
(rarely `http`). The separator is always a hyphen.

From `internal/web/static/js/pages/settings.js`, `providerOptions` is a
sorted array of all provider IDs loaded from `/api/settings`. The Alpine
component already knows about all providers at load time.

---

## Affected Files

| File | Change |
|------|--------|
| `internal/web/templates/partials/settings/assistant_panel.html` | Replace `.provider-grid` markup with two-panel layout |
| `internal/web/static/js/pages/settings.js` | Add `providerFamilies()` computed helper and `selectedFamily` state |
| `internal/web/static/css/pages/settings.css` | Replace `.provider-grid` / `.provider-choice` with `.provider-split`, `.provider-family-list`, `.provider-variant-rail` classes |

No Go, no API, no test changes needed — purely a frontend layout change.

---

## Data Model Changes (JS only)

Add to the Alpine component object in `settings.js`:

```js
selectedFamily: "",   // e.g. "claude"

// Derive families from providerOptions by splitting on the last "-"
// and taking everything before the transport suffix.
// "claude-cli" → "claude", "copilot-sdk" → "copilot", etc.
providerFamilies() {
  const seen = new Set();
  const families = [];
  for (const id of this.providerOptions) {
    const family = this.familyOf(id);
    if (!seen.has(family)) { seen.add(family); families.push(family); }
  }
  return families;  // preserves providerOptions sort order
},

// Extract the family prefix from a full provider ID.
// Splits on the last hyphen so "copilot-sdk" → "copilot",
// "openclaude-cli" → "openclaude", "opencode-acp" → "opencode".
familyOf(providerID) {
  const idx = providerID.lastIndexOf("-");
  return idx > 0 ? providerID.slice(0, idx) : providerID;
},

// All variant IDs that belong to a family.
variantsOf(family) {
  return this.providerOptions.filter(id => this.familyOf(id) === family);
},

// Transport suffix label from a full provider ID.
// "claude-cli" → "cli", "copilot-sdk" → "sdk", "opencode-acp" → "acp"
transportOf(providerID) {
  const idx = providerID.lastIndexOf("-");
  return idx > 0 ? providerID.slice(idx + 1) : providerID;
},

// Called when a family row is clicked.
selectFamily(family) {
  this.selectedFamily = family;
  // Auto-select the first variant of this family as the active provider.
  const variants = this.variantsOf(family);
  if (variants.length > 0 && !variants.includes(this.selectedProviderID())) {
    this.selectProvider(variants[0]);
  }
},
```

Also patch `selectProvider` to keep `selectedFamily` in sync:
```js
selectProvider(providerID) {
  this.values.assistant.default_provider = providerID;
  this.selectedFamily = this.familyOf(providerID);  // ← add this line
  this.ensureProvider(providerID);
},
```

And in `load()`, after `providerOptions` is populated, initialise
`selectedFamily` from the current default provider:
```js
this.selectedFamily = this.familyOf(this.values.assistant.default_provider);
```

---

## HTML Changes (`assistant_panel.html`)

Remove the existing `.provider-grid` block and replace with:

```html
<div class="settings-card settings-card--accent">
  <div class="settings-card-title">
    <span>default assistant</span>
    <button class="btn-reset" @click="resetField('assistant', 'default_provider', 'openclaude-cli')">baseline</button>
  </div>

  <div class="provider-split">
    <!-- LEFT RAIL: one row per family -->
    <div class="provider-family-list" role="listbox" aria-label="Provider family">
      <template x-for="family in providerFamilies()" :key="family">
        <button
          type="button"
          class="provider-family-row"
          :class="selectedFamily === family ? 'active' : ''"
          role="option"
          :aria-selected="String(selectedFamily === family)"
          @click="selectFamily(family)"
        >
          <span class="provider-family-name" x-text="family"></span>
          <!-- Show a mint dot if the selected default provider belongs to this family -->
          <span
            class="provider-family-active-dot"
            x-show="familyOf(selectedProviderID()) === family"
            title="current default"
          ></span>
        </button>
      </template>
    </div>

    <!-- RIGHT RAIL: variants of the selected family -->
    <div class="provider-variant-rail" x-show="selectedFamily">
      <div class="provider-variant-label">transport</div>
      <div class="provider-variant-pills">
        <template x-for="variantID in variantsOf(selectedFamily)" :key="variantID">
          <button
            type="button"
            class="provider-variant-pill"
            :class="values.assistant.default_provider === variantID ? 'active' : ''"
            @click="selectProvider(variantID)"
            x-text="transportOf(variantID)"
          ></button>
        </template>
      </div>
      <!-- Health line for the currently selected variant -->
      <p class="provider-variant-health">
        <span x-text="values.assistant.default_provider"></span>
        —
        <span x-text="
          providerHealth(values.assistant.default_provider).healthy
            ? 'healthy'
            : providerHealth(values.assistant.default_provider).runs
              ? 'has history'
              : 'not run yet'
        "></span>
        <span x-show="providerHealth(values.assistant.default_provider).failures">
          · failures:
          <strong x-text="providerHealth(values.assistant.default_provider).failures"></strong>
        </span>
      </p>
    </div>
  </div>

  <p class="setting-help">Used when a project does not choose its own assistant.</p>
</div>
```

The per-provider config cards below (model, access, safety, health) are
**unchanged** — they already key off `selectedProviderID()` which stays the
same.

---

## CSS Changes (`settings.css`)

Remove `.provider-grid`, `.provider-choice`, `.provider-choice-name`,
`.provider-choice-meta`.

Add:

```css
/* Two-panel provider selector */
.provider-split {
  display: grid;
  grid-template-columns: 160px 1fr;
  gap: 0;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  overflow: hidden;
  margin-bottom: var(--space-3);
}

/* LEFT RAIL */
.provider-family-list {
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--border-subtle);
  max-height: 280px;
  overflow-y: auto;
  background: var(--canvas-black);
}

.provider-family-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 40px;
  padding: var(--space-2) var(--space-3);
  border: none;
  border-bottom: 1px solid var(--border-subtle);
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-family: var(--font-mono);
  font-size: 11px;
  letter-spacing: 1px;
  text-align: left;
  text-transform: uppercase;
  transition: color 150ms ease, background 150ms ease;
}

.provider-family-row:last-child {
  border-bottom: none;
}

.provider-family-row:hover {
  color: var(--text-primary);
  background: var(--bg-translucent-hover);
}

.provider-family-row.active {
  color: var(--jelly-mint);
  background: var(--bg-mint-subtle);
}

.provider-family-active-dot {
  flex: 0 0 auto;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--jelly-mint);
}

/* RIGHT RAIL */
.provider-variant-rail {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  background: var(--bg-translucent-card);
}

.provider-variant-label {
  font-family: var(--font-mono);
  font-size: 9px;
  letter-spacing: 1.5px;
  text-transform: uppercase;
  color: var(--text-secondary);
}

.provider-variant-pills {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.provider-variant-pill {
  min-height: 32px;
  padding: var(--space-1) var(--space-3);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--canvas-black);
  color: var(--text-secondary);
  cursor: pointer;
  font-family: var(--font-mono);
  font-size: 11px;
  letter-spacing: 1px;
  text-transform: uppercase;
  transition: border-color 150ms ease, color 150ms ease, background 150ms ease;
}

.provider-variant-pill:hover {
  border-color: var(--jelly-mint);
  color: var(--text-primary);
}

.provider-variant-pill.active {
  border-color: var(--jelly-mint);
  background: var(--bg-mint-subtle);
  color: var(--jelly-mint);
}

.provider-variant-health {
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 10px;
  letter-spacing: 0.8px;
  margin: 0;
}

.provider-variant-health strong {
  color: var(--color-error);
}

/* Responsive: stack vertically on narrow panels */
@media (max-width: 640px) {
  .provider-split {
    grid-template-columns: 1fr;
  }
  .provider-family-list {
    flex-direction: row;
    flex-wrap: wrap;
    max-height: none;
    border-right: none;
    border-bottom: 1px solid var(--border-subtle);
  }
  .provider-family-row {
    border-bottom: none;
    border-right: 1px solid var(--border-subtle);
  }
}
```

---

## Interaction Design

| Action | Result |
|--------|--------|
| Click a family row | `selectedFamily` updates, right rail shows that family's variants, first variant auto-selected as default if the current default is from a different family |
| Click a variant pill | `selectProvider(variantID)` called — same as before |
| Page load | `selectedFamily` derived from `default_provider`; corresponding family row is highlighted; correct variant pill is active |
| Add a new provider family in Go | No UI changes needed — `providerFamilies()` derives families dynamically from `providerOptions` |
| Add a new variant to an existing family | No UI changes needed — `variantsOf(family)` derives variants dynamically |

---

## Visual Result (approximate)

```
┌────────────────────────────────────────────────────────────────────┐
│ DEFAULT ASSISTANT                                          BASELINE │
│                                                                     │
│ ┌─────────────┬──────────────────────────────────────────────────┐ │
│ │ CLAUDE    • │  TRANSPORT                                        │ │
│ │ CODEX       │                                                   │ │
│ │ COPILOT     │  [ CLI ]  [ ACP ]                                 │ │
│ │ GEMINI      │                                                   │ │
│ │ KIRO        │  claude-cli — not run yet                        │ │
│ │ OPENCLAUDE  │                                                   │ │
│ │ OPENCODE    │                                                   │ │
│ │ CODEBUFF    │                                                   │ │
│ └─────────────┴──────────────────────────────────────────────────┘ │
│                                                                     │
│ Used when a project does not choose its own assistant.              │
└────────────────────────────────────────────────────────────────────┘
```

The mint dot (•) on the left rail indicates which family the current default
provider belongs to. The active variant pill has a mint border.

---

## Out of Scope

- No changes to Go backend or API
- No changes to the per-provider config cards (model, access, safety, health)
- No changes to the save/load/validate logic
- No changes to any other settings panels
- No new tests (pure layout change with no logic branching)
