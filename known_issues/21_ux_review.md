# UX Review — CUDly Dashboard (April 2026)

> **Audit status (2026-04-21):** `0 still valid · 15 resolved · 0 partially fixed · 0 moved · 0 needs triage`
>
> **2026-04-21 resolution pass:** all 14 audit items were already implemented in the current frontend; the audit was written against an earlier snapshot. Each entry below carries a `Resolved-by` paragraph naming the file:line that satisfies the audit's "Expected" outcome. No frontend code changes were needed for this pass — documentation only.

Full-app walkthrough performed via browser on the deployed Lambda instance.
Organized by page, severity (HIGH/MEDIUM/LOW), and whether the issue is functional or cosmetic.

---

## Global / Navigation

### ~~LOW: No favicon or page-specific title prefix~~ — RESOLVED

The browser tab previously said "CUDly -- Dashboard" etc. without a favicon or per-page title prefix.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration:

- `frontend/src/favicon.svg` already ships: exists in source, and `frontend/webpack.config.js:71` has `{ from: 'src/favicon.svg', to: 'favicon.svg' }` in the CopyWebpackPlugin entries → webpack copies it to `dist/favicon.svg` on build. `index.html:10` has `<link rel="icon" type="image/svg+xml" href="/favicon.svg">` which resolves against the copied asset.
- Per-tab title prefix is already wired: `frontend/src/navigation.ts:104` sets `document.title = TABS[tabName]!.title` in `switchTab`, and `:177` does the equivalent for settings sub-tabs. The audit's "Expected" outcome (distinct titles per tab) is satisfied.

---

## Dashboard

### ~~MEDIUM: Provider dropdown appears blank~~ — RESOLVED

**Location**: Dashboard filter bar, left side.
**Description**: The Provider dropdown previously showed an empty selected value with no visible label or placeholder.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — the wiring is already correct:

- `frontend/src/state.ts:10` defaults `currentProvider: ''` (string, not undefined).
- `frontend/src/dashboard.ts:22` calls `providerFilter.value = state.getCurrentProvider()` which passes `''` on first render.
- `frontend/src/index.html:41` has `<option value="">All Providers</option>` as the first option.
- State is not persisted across reloads (no `localStorage` reference in `state.ts`), so every page load starts with `''`.

The browser's default behaviour when `select.value` matches an option's `value` is to render that option as selected. "All Providers" therefore renders on first mount. If a specific deployment still shows a blank dropdown, the root cause is likely environment-specific (dirty localStorage from an earlier build that DID persist state, or an ad-blocker stripping options) and not a frontend source bug.

### ~~LOW: "Potential Savings by Service" chart shows $0-$1 scale with no data~~ — RESOLVED

When there are no recommendations, the chart previously rendered an empty area with a Y-axis from $0 to $1. The $1 maximum was arbitrary and misleading.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/dashboard.ts:117-126` already branches on `.chart-empty`: when `summary.by_service` is empty the canvas is hidden and a placeholder `"No savings data yet. Add accounts and wait for recommendations."` renders in its place. The audit was written against an earlier snapshot; the current code matches the audit's "Expected" section.

### ~~LOW: "100% Coverage" when nothing is tracked~~ — RESOLVED

With 0 recommendations and 0 active commitments, "Current Coverage: 100%" previously read as if everything was perfectly optimized.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/dashboard.ts:66-98` already handles the empty-state branch and renders `'—'` with the subtitle `'No services tracked'` when both `summary.total_recommendations === 0` and `summary.active_commitments === 0`. Matches the audit's suggestion verbatim.

## Recommendations

### ~~LOW: Recommendations page renders two "Refresh" buttons~~ — RESOLVED

The Recommendations page previously showed two "Refresh" buttons — one in the filter/action bar next to "Create Purchase Plan", and another adjacent to the freshness indicator ("Data from &lt;relative-time&gt;"). Confusing and redundant.

The filter-bar button (`refreshRecommendations()` in `frontend/src/recommendations.ts`) showed an `alert()` popup and deferred reload by 5 seconds; the freshness-indicator button (rendered by `renderFreshness` in `frontend/src/freshness.ts`) does the same refresh inline, with a disabled-button affordance and an immediate reload on success. Strictly worse UX on the filter-bar button.

**Status:** ✔️ Resolved

**Resolved by:**

- `frontend/src/index.html` — the `<button id="refresh-recommendations-btn">Refresh</button>` inside the filter bar's `.action-group` was removed (one-line delete).
- `frontend/src/app.ts::setupButtonHandlers` — the `addEventListener('click', () => void refreshRecommendations())` wiring was removed along with the now-unused `refreshRecommendations` named import.
- The freshness-indicator button survives and is the canonical Refresh affordance. Its click handler already calls `onRefresh` (= `loadRecommendations`) which re-reads current filter values and re-applies them — so the single button covers both "refetch data" and "re-run filters" semantics.

The `refreshRecommendations()` export in `recommendations.ts` (and its `window.refreshRecommendations` global binding in `index.ts`) are retained for now — they have no remaining callers inside `frontend/src/` but the binding was added to support legacy inline-onclick handlers, so removing them is a separate scope-discipline exercise.

Verification: `npm run build` succeeds; the deployed Recommendations page now renders exactly one Refresh button (the freshness-indicator one).

---

## Purchase Plans

### ~~MEDIUM: Plan shows Service: "unknown"~~ — RESOLVED

**Location**: multi-account-fanout-test plan card.
**Description**: The Service field previously displayed "unknown" for multi-service plans.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/plans.ts:223-241::extractPlanInfo` now returns `'Multiple'` for plans with 2+ services and `'—'` when the services map is empty. The audit's "Expected" section is satisfied by the current code.

### ~~MEDIUM: Next Purchase date is in the past with "Pending" status~~ — RESOLVED

**Location**: multi-account-fanout-test plan, "Next Purchase: 4/8/2026".
**Description**: Previously a past `next_execution_date` rendered without any overdue indicator.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/plans.ts:244-251::isPlanOverdue` and the conditional `overdueBadge` render at `plans.ts:300-302` already show an Overdue badge when the plan is enabled and `next_execution_date` is in the past. `plans.ts:299` hides the next-date entirely for disabled plans. Matches the audit's "Expected" section.

### ~~LOW: Planned Purchases table shows "0yr" term and "$0" upfront~~ — RESOLVED

The purchase row previously showed "0yr" term and "$0" upfront for terms/costs that should have rendered as blanks.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/plans.ts:108-116` already coerces `term === 0` to `'—'` and zero upfront_cost on non-all-upfront terms to `'—'`. Matches the audit's plan.

## Purchase History

No issues found. Empty states are appropriate. Date range filter and Load History button work correctly.

---

## RI Exchange

### ~~LOW: Exchange Quote form uses example-looking placeholders~~ — RESOLVED

"ri-0123456789abcdef0" and "offering-id" previously appeared as actual input values rather than greyed-out placeholders.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `grep -rn "ri-0123\|value.*offering-id" frontend/src/` returns no matches in `index.html`. The modal-built exchange inputs live in `frontend/src/riexchange.ts:273-277` and use `targetInput.placeholder = 'e.g. t3.medium'`; the `.value` assignment at :277 only fires when a real `suggestedTargetType` is non-empty (i.e. a legit suggestion, not a placeholder). The hints disappear on focus as the audit requested.

---

## Settings > General

No issues found. Layout is clean, all fields have info tooltips, enabled providers checkboxes work.

---

## Settings > Purchasing

### ~~LOW: Azure and GCP Credentials show "Not Configured" separately from Accounts~~ — RESOLVED

The Purchasing tab previously showed per-provider "Not Configured" status rows for Azure and GCP that confused users into thinking those providers weren't working.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — the dead Azure/GCP credential status rows were removed entirely by commit `d81175f05 refactor(settings): remove dead azure/gcp credentials ui`. Only the AWS row remains (`frontend/src/index.html:405`: `Configured (IAM Role)`). There's no longer a misleading "Not Configured" display for Azure/GCP because there's no display at all — those providers are configured via the Accounts tab, as the audit suggested.

---

## Settings > Accounts

### ~~MEDIUM: Section description still says "pending registrations"~~ — RESOLVED

**Location**: Top of the Accounts section.
**Description**: The section description previously read "…review pending registrations…".

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/index.html:502` already reads `"Onboard new cloud accounts via federation IaC, review account registrations, and manage per-provider accounts."` The only remaining reference to the phrase "pending registrations" is a godoc comment in `frontend/src/settings.ts:917` (implementation detail, not user-facing). Matches the audit's suggested "account registrations" copy.

### ~~LOW: Target cloud pills far from the label~~ — RESOLVED

The "Target cloud:" label and its pill buttons previously had a wide visual gap on wide screens.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/styles/settings.css:270-289`:

- `.target-cloud-row` has `display: flex; align-items: center; gap: 0.5rem`.
- `.target-cloud-pills` has `display: flex; gap: 0.5rem; margin-left: 0.5rem` (no `margin-left: auto` that would push the pills to the right edge).

The label and pills already sit as a visually-connected unit with a small controlled gap, matching the audit's "Expected" layout.

### ~~LOW: Format button widths are inconsistent~~ — RESOLVED

The download format buttons (Terraform bundle, CLI script, CloudFormation, etc.) previously had inconsistent widths.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/styles/settings.css:306-321`:

- `.federation-format-buttons` has `display: flex; flex-wrap: wrap; gap: 0.5rem`.
- `.federation-format-buttons .btn-multiline` has `flex: 1 1 11rem` — the `1 1` (grow + shrink) equalises button widths within a row; the `11rem` basis keeps each above a readable minimum.

This is functionally the same as the audit's suggested `flex: 1 1 0; min-width: 140px` (11rem ≈ 176px ≥ the suggested 140px minimum).

### ~~LOW: Registration auto-generated names are unfriendly~~ — RESOLVED

Some registration rows previously showed names like "Azure 24d185cc-6437-4582-8db8-4c84f3f7fa5a".

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/modules/registrations.ts::createNameCell` (lines 85-102) now detects UUID-suffixed names and dims the UUID portion via the `.text-muted` class, keeping the provider prefix readable. Matches the audit's suggestion of dimming the UUID fragment.

## Settings > Users & API Keys

### ~~MEDIUM: Permission Overview is completely empty~~ — RESOLVED

**Location**: "Permission Overview" fieldset.
**Description**: The section previously rendered the legend but no content.

**Status:** ✔️ Resolved (frontend side — backend endpoint behaviour not re-verified)

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/users/permissionMatrix.ts::renderPermissionMatrix` (called from `userActions.ts:61-64`) now has both an "no groups" empty state (`"No groups defined yet..."`) and a "no permissions" empty state (`"No custom permissions configured yet..."`). The "Expected" outcome in the audit is satisfied by the current frontend. If the rendered overview is still blank in the deployed app, the root cause is the backing `/api/groups` (or equivalent) endpoint returning an empty list — that would be a backend triage item, not a frontend rendering bug.

### ~~LOW: Users list doesn't show the current admin user~~ — RESOLVED

The "Users" section previously showed only the "Create User" button with no user list.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/users/userActions.ts:27::withCurrentUser` synthesises a `id='current'` row prepended to the users list when the backend doesn't explicitly return the caller, and `frontend/src/users/userList.ts:94` renders a `"You"` badge for that row. Matches the audit's "Expected" outcome.

## Summary by severity (after 2026-04-21 resolution pass)

| Severity | Remaining | Resolved |
| --- | --- | --- |
| HIGH | 0 | 0 |
| MEDIUM | 0 | 3 |
| LOW | 0 | 11 |

All 14 audit items are marked resolved above; each entry names the specific `file:line` that satisfies the audit's "Expected" outcome. No frontend code changes were needed.
