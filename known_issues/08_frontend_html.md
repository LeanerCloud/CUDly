# Known Issues: Frontend HTML

> **Audit status (2026-04-20):** `0 still valid · 5 resolved · 0 partially fixed · 0 moved · 0 needs triage · 1 deferred (focus trap)`

## ~~CRITICAL: Inline onclick handlers blocked by Content Security Policy~~

**File**: `frontend/src/index.html:617, 623, 999, 1023, 1026`
**Description**: Five buttons used inline `onclick` attributes; CSP with `script-src 'self'` (no `'unsafe-inline'`) blocked them.
**Impact**: User and group management UI was silently non-functional.
**Status:** ✔️ Resolved

**Resolved by:** `0cb45a370` — all five inline `onclick` handlers removed; buttons now wired via `addEventListener` in `frontend/src/index.ts:42-46`.

## ~~HIGH: aria-labelledby references non-existent element ID~~ — RESOLVED

**File**: `frontend/src/index.html:35`
**Description**: The dashboard tabpanel previously declared `aria-labelledby="dashboard-tab-btn"` while the six tab buttons had no `id` attributes at all, so the IDREF was broken for every tabpanel.
**Status:** ✔️ Resolved

**Resolved by:** Added `id="dashboard-tab-btn"`, `recommendations-tab-btn`, `plans-tab-btn`, `history-tab-btn`, `ri-exchange-tab-btn`, `settings-tab-btn` to the six tab buttons and wired each tabpanel's `aria-labelledby` to the matching id.

### Original implementation plan

**Goal:** Every ARIA tabpanel points to an existing tab element via a valid IDREF, and every tab has a matching `id`.

**Files to modify:**

- `frontend/src/index.html:26-31` — add `id` attributes to the six tab buttons.
- `frontend/src/index.html:35, 69, and the other four tabpanel openings` — add correct `aria-labelledby` values.

**Steps:**

1. For each tab button add `id` matching the scheme the panels expect: `dashboard-tab-btn`, `recommendations-tab-btn`, `plans-tab-btn`, `history-tab-btn`, `ri-exchange-tab-btn`, `settings-tab-btn`.
2. Ensure each tabpanel `div` has `aria-labelledby="<corresponding>-tab-btn"`.
3. Grep the page for any residual broken IDREFs; fix them too.

**Edge cases the fix must handle:**

- JavaScript that queries tabs via `[data-tab=...]` must not regress — adding `id` is additive.
- Automated tests that select the first `.tab-btn` still work.

**Test plan:**

- Add a DOM snapshot test that asserts every `aria-labelledby` has a target element in the document.
- Run axe-core (or similar) via Playwright against the page and assert zero "ARIA reference not found" violations.

**Verification:**

- `cd frontend && npm test`
- Manual: Chrome DevTools Accessibility tree — select the dashboard panel; confirm Name comes from the Dashboard tab button.

**Related issues:** `08_frontend_html.md#MEDIUM: Tab buttons have no IDs` (same fix resolves both).

**Effort:** `small`

## ~~HIGH: label for= references non-existent control ID~~ — RESOLVED

**File**: `frontend/src/index.html:288`
**Description**: `<label for="setting-enabled-providers">` referenced a non-existent ID; the actual controls were three checkboxes (`provider-aws`/`azure`/`gcp`).
**Status:** ✔️ Resolved

**Resolved by:** Replaced the orphan label with `<span id="enabled-providers-label" class="label-like">`. Added `role="group" aria-labelledby="enabled-providers-label"` to the surrounding `.setting-input` div so the three checkboxes are properly grouped and labelled for assistive technologies. Extended `.setting-info label` CSS selector to cover `.label-like` so the visual styling is unchanged. Using a span + role="group" instead of a nested fieldset avoids breaking the existing flex-grid layout of `.setting-row`.

### Original implementation plan

**Goal:** The "Enabled Providers" group is described via native grouping semantics (fieldset/legend), not a broken `for` attribute.

**Files to modify:**

- `frontend/src/index.html:286-295` — restructure the setting-row for Enabled Providers.

**Steps:**

1. Replace the outer `<div class="setting-row">` for this control with a `<fieldset class="setting-row">` and put the label text inside a `<legend>Enabled Providers</legend>`.
2. Remove the orphan `<label for="setting-enabled-providers">`.
3. Preserve the info-icon tooltip by placing it inside the `<legend>` or as a sibling block.
4. Adjust CSS if the settings-row grid was built on `<div>` — likely needs a small override rule for `fieldset.setting-row { display: grid; ... }`.

**Edge cases the fix must handle:**

- Keep existing provider checkbox IDs (`provider-aws`/`provider-azure`/`provider-gcp`) intact — other code depends on them.
- The `dirty` CSS class applied via `.closest('.setting-input')` keeps working.

**Test plan:**

- Axe-core violation count for this page = 0 for "orphan label" rule after fix.
- Click the legend text in the browser; confirm no longer focusable as a label.

**Verification:**

- `cd frontend && npm run build && npm test`
- Manual: screen-reader walkthrough of the Enabled Providers group.

**Effort:** `small`

## ~~MEDIUM: Tab buttons have no IDs — ARIA tabs pattern incomplete~~ — RESOLVED

**File**: `frontend/src/index.html:26-31`
**Description**: Tab buttons had `role="tab"` + `aria-controls` but no `id`, and most tabpanels had no `aria-labelledby`. Assistive tech could not associate panels with their controlling tabs.
**Status:** ✔️ Resolved

**Resolved by:** Fixed together with the HIGH issue above — all six tab buttons now carry `id="<tab>-tab-btn"` and all six tabpanels now carry `aria-labelledby="<tab>-tab-btn"`. Keyboard arrow-key navigation between tabs is a separate nice-to-have and was not implemented in this pass.

### Original implementation plan

See the implementation plan under `08_frontend_html.md#HIGH: aria-labelledby references non-existent element ID` — the same set of changes (add `id` to each tab, wire `aria-labelledby` on every panel) resolves this issue.

**Additional steps specific to this issue:**

1. For each of the six panels (dashboard, recommendations, plans, history, ri-exchange, settings), add `aria-labelledby="<tab-id>"`.
2. Confirm `role="tabpanel"` is present on all six (some may be missing).
3. Add keyboard handling in `index.ts` so arrow keys move focus between tabs (ARIA tabs pattern requires this).

**Test plan:**

- Axe-core: "tabs must have accessible name" and "tabpanel must be labelled" violations = 0.
- Manual keyboard test: Tab → first tab → ArrowRight → second tab; focus moves and aria-selected updates.

**Effort:** `small` (HTML only) / `medium` (include keyboard handling)

## ~~MEDIUM: All modal dialogs missing role="dialog" and aria-modal~~ — RESOLVED (focus trap deferred)

**File**: `frontend/src/index.html:598, 754, 761, 773, 786, 816, 852, 879, 913`
**Description**: Nine modal containers had no `role="dialog"`, `aria-modal`, or `aria-labelledby`. Screen readers did not switch into dialog mode when modals opened.
**Status:** ✔️ Resolved for ARIA semantics; focus trap deferred.

**Resolved by:** Added `role="dialog" aria-modal="true"` to all nine modal containers. Eight modals now reference a matching heading via `aria-labelledby` (adding `id` attributes to the four h2s that didn't already have one — `purchase-modal-title`, `select-recommendations-modal-title`, `create-apikey-modal-title`, `group-duplicate-modal-title`). The ri-exchange-modal's content is rendered dynamically by JS, so it uses a static `aria-label="RI Exchange"` instead.

**Deferred:** Focus trap (+ESC-to-close) was listed in the plan but requires coordinated changes across every modal open/close call site and is safer as a dedicated follow-up. Screen readers still get the dialog role announcement; only the keyboard-trap-to-dialog behaviour is outstanding.

### Original implementation plan

**Goal:** Every modal has correct ARIA dialog semantics, is labelled by its heading, and traps focus while open.

**Files to modify:**

- `frontend/src/index.html:598, 754, 761, 773, 786, 816, 852, 879, 913` — add `role="dialog"`, `aria-modal="true"`, and `aria-labelledby` to each modal container.
- `frontend/src/modal.ts` (new helper) or existing modal utility — implement focus trap + ESC-to-close.
- All scripts that call `modal.classList.remove('hidden')` / `add('hidden')` — route through the helper so focus trap engages/disengages.

**Steps:**

1. For each modal, give the inner `<h2>` a unique `id` (e.g. `plan-modal-title`) and set `aria-labelledby` on the outer `<div>` accordingly.
2. Add `role="dialog"` and `aria-modal="true"` to each outer `<div>`.
3. Introduce `openModal(modalEl)` / `closeModal(modalEl)` helpers that: store the previously focused element; add a keydown handler trapping Tab inside the modal's focusable children; restore focus on close.
4. Replace existing `.classList.add/remove('hidden')` calls in modal open/close code paths with the helper.
5. Add ESC key close where it makes sense (non-destructive modals).

**Edge cases the fix must handle:**

- Nested modals (group-duplicate-modal can open from group-modal) — stack the previously-focused element per open modal.
- Modal that closes itself via form submit — restore focus before navigating away.
- Modal already open when another is triggered — current behaviour is unclear; pick a deterministic rule (queue or replace).

**Test plan:**

- Axe-core: zero "dialog must have accessible name" and "aria-modal is true" violations.
- Unit test focus trap: render modal, Tab repeatedly, assert focus never leaves modal subtree.
- Unit test focus restoration: open modal, close, assert document.activeElement is the original trigger.

**Verification:**

- `cd frontend && npm test`
- Manual: open every modal with VoiceOver/NVDA; confirm "dialog" role announced and focus trapped.

**Effort:** `medium`
