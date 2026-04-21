# UX Review — CUDly Dashboard (April 2026)

> **Audit status (2026-04-20):** `6 still valid · 8 resolved · 0 partially fixed · 0 moved · 0 needs triage`

Full-app walkthrough performed via browser on the deployed Lambda instance.
Organized by page, severity (HIGH/MEDIUM/LOW), and whether the issue is functional or cosmetic.

---

## Global / Navigation

### LOW: No favicon or page-specific title prefix

The browser tab says "CUDly -- Dashboard" etc. but there's no favicon, so tabs are hard to distinguish visually when multiple are open.

**Status:** ✅ Still valid (a `favicon.svg` is linked in `index.html:10` but no static asset ships — verify during fix).

#### Implementation plan

**Goal:** Every CUDly tab has a recognisable favicon and a title prefix that identifies the current view.

**Files to modify:**

- `frontend/public/favicon.svg` (new) — simple SVG mark.
- `frontend/src/index.html:10-11` — confirm link+title references.
- `frontend/src/index.ts` (tab activation) — update `document.title` on tab change.

**Steps:**

1. Design/drop a 32×32 SVG favicon — brand-colour tile with "C" or commitment glyph.
2. Verify the `<link rel="icon" href="/favicon.svg">` resolves (check bundler config — Vite/ESBuild copy from `public/`).
3. On tab activation, set `document.title = \`CUDly · \${tabName}\`;` so cross-tab browsing is unambiguous.

**Test plan:** manual — open two tabs on different pages; confirm distinct titles and visible favicon.

**Verification:** `cd frontend && npm run build && open dist/index.html` then inspect the tab.

**Effort:** `small`

---

## Dashboard

### MEDIUM: Provider dropdown appears blank

**Location**: Dashboard filter bar, left side.
**Description**: The Provider dropdown shows an empty selected value with no visible label or placeholder. Users can't tell what it's filtering by or whether "all" is the default. Every other page's Provider dropdown (Recommendations, Purchase Plans, etc.) shows a readable default like "All Providers".
**Expected**: Show "All Providers" as the default visible selection, matching the other pages.

**Status:** ✅ Still valid — the HTML source shows `<option value="">All Providers</option>` (index.html:41), so the default exists; the bug is JS code that programmatically sets the value to something not in the list or strips the selected option on load. Reproduce before fixing.

#### Implementation plan

**Goal:** Dashboard provider dropdown shows "All Providers" on first render, matching Recommendations page.

**Files to modify:**

- `frontend/src/dashboard.ts` (or equivalent) — locate the code that populates/selects the provider dropdown.
- Possibly `frontend/src/state.ts` — confirm default filter value is `''` not `undefined`.

**Steps:**

1. Reproduce the bug in a fresh browser with DevTools; inspect `#dashboard-provider-filter` to confirm `select.value === ''` but no option shows `selected`.
2. If the code clears and rebuilds options on mount, ensure `''` option is always re-added first.
3. Initialise `select.value = ''` after population; remove any stray `selectedIndex = -1`.

**Test plan:** JSDOM unit test asserts `select.value === ''` and `select.selectedOptions[0].text === 'All Providers'` after dashboard mount.

**Verification:** `cd frontend && npm test && npm run dev`; visit `/` with empty storage.

**Effort:** `small`

### ~~LOW: "Potential Savings by Service" chart shows $0-$1 scale with no data~~ — RESOLVED

When there are no recommendations, the chart previously rendered an empty area with a Y-axis from $0 to $1. The $1 maximum was arbitrary and misleading.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/dashboard.ts:117-126` already branches on `.chart-empty`: when `summary.by_service` is empty the canvas is hidden and a placeholder `"No savings data yet. Add accounts and wait for recommendations."` renders in its place. The audit was written against an earlier snapshot; the current code matches the audit's "Expected" section.

### ~~LOW: "100% Coverage" when nothing is tracked~~ — RESOLVED

With 0 recommendations and 0 active commitments, "Current Coverage: 100%" previously read as if everything was perfectly optimized.

**Status:** ✔️ Resolved

**Resolved by:** Verified during 2026-04-21 exploration — `frontend/src/dashboard.ts:66-98` already handles the empty-state branch and renders `'—'` with the subtitle `'No services tracked'` when both `summary.total_recommendations === 0` and `summary.active_commitments === 0`. Matches the audit's suggestion verbatim.

## Recommendations

No functional issues found. Layout is clean, filters work, empty state message is appropriate.

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

### LOW: Exchange Quote form uses example-looking placeholders

"ri-0123456789abcdef0" and "offering-id" look like placeholder text but appear as actual input values. Users might think they need to clear these before entering real data, or might accidentally submit them.
**Suggestion**: Use HTML `placeholder` attribute instead of pre-filled `value`, so the hints disappear on focus.

**Status:** ✅ Still valid

#### Implementation plan

**Goal:** Exchange Quote form inputs use the `placeholder` attribute; no real `value` is set on page load.

**Files to modify:** `frontend/src/index.html` (RI Exchange section — grep for `ri-0123` / `offering-id`).

**Steps:**

1. Find the inputs; replace `value="ri-0123..."` with `placeholder="ri-0123..."` and remove any initial `value`.
2. Do the same for `offering-id`.
3. Confirm form submit validation (empty → show error) still works.

**Test plan:** manual — load page, confirm inputs are empty with greyed-out placeholder.

**Effort:** `small`

---

## Settings > General

No issues found. Layout is clean, all fields have info tooltips, enabled providers checkboxes work.

---

## Settings > Purchasing

### LOW: Azure and GCP Credentials show "Not Configured" separately from Accounts

The Purchasing tab shows per-provider credential status (AWS: "Configured (IAM Role)", Azure: "Not Configured", GCP: "Not Configured"), while the Accounts tab shows working account entries for Azure and GCP (with "Account created" status). This can confuse users into thinking Azure/GCP aren't working when they actually are — it's just that per-provider purchasing credentials haven't been explicitly set here.
**Suggestion**: Cross-reference the Accounts tab's account status, or show "Using account credentials" when an account with credentials exists.

**Status:** ✅ Still valid — the recent `d81175f05 refactor(settings): remove dead azure/gcp credentials ui` removed the dead fields but left the status row. Confirm during fix.

#### Implementation plan

**Goal:** Purchasing tab credential status reflects configured Accounts when no provider-level credentials are set.

**Files to modify:** `frontend/src/settings.ts` (provider status block).

**Steps:**

1. On load, fetch the accounts list and count per-provider configured accounts.
2. If provider has no direct credential but ≥1 configured account, show "Using account credentials (N accounts)".
3. Preserve "Configured (IAM Role)" styling for the AWS case.

**Test plan:** unit test with AWS=0 accounts, Azure=1 account scenarios; assert status strings.

**Effort:** `small`

---

## Settings > Accounts

### MEDIUM: Section description still says "pending registrations"

**Location**: Top of the Accounts section: "Onboard new cloud accounts via federation IaC, review pending registrations, and manage per-provider accounts."
**Description**: The "Account Registrations" fieldset was renamed from "Pending Registrations" but the parent section description still uses the old wording.
**Fix**: Update to "review account registrations" or just "review registrations".

**Status:** ✅ Still valid

#### Implementation plan

**Goal:** Section description uses the current "account registrations" terminology.

**Files to modify:** `frontend/src/index.html` (Accounts section intro paragraph).

**Steps:**

1. Grep the HTML for "pending registrations"; replace with "account registrations".
2. Check for any other copy referencing "pending registrations" elsewhere (tooltips, help text).

**Effort:** `small`

### LOW: Target cloud pills far from the label

The "Target cloud:" label is left-aligned while the pill buttons (AWS / Azure / GCP) are right-aligned, creating a wide visual gap. On wide screens this disconnect is pronounced.
**Suggestion**: Either left-align the pills near the label, or use a full-width row with the label and pills on the same line closer together.

**Status:** ✅ Still valid

#### Implementation plan

**Goal:** The "Target cloud" label and its pill buttons sit as a visually-connected unit.

**Files to modify:** CSS for the federation panel target selector; HTML may need a wrapper `<div>`.

**Steps:**

1. Wrap `<label>` and pill group in a flex container with `gap: 0.5rem` and `justify-content: flex-start`.
2. Remove any `margin-left: auto` on the pills.

**Effort:** `small`

### LOW: Format button widths are inconsistent

The download format buttons (Terraform bundle, CLI script, CloudFormation, etc.) have a `min-width` but don't stretch to fill the row evenly.

**Status:** ✅ Still valid

#### Implementation plan

**Goal:** Format buttons are the same width per row regardless of how many are present.

**Files to modify:** CSS for the federation format selector.

**Steps:** apply `display: flex; gap: 0.5rem;` to the button row and `flex: 1 1 0; min-width: 140px;` to each button so they grow equally but never below the readable minimum.

**Effort:** `small`

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

## Summary by severity (remaining — after 2026-04-21 resolution pass)

| Severity | Remaining | Key items |
| --- | --- | --- |
| HIGH | 0 | — |
| MEDIUM | 2 | Blank provider dropdown · "pending registrations" description |
| LOW | 4 | Favicon/title prefix · Exchange Quote placeholders · Azure/GCP credentials shown separately · Target-cloud pill alignment / format button widths |

(8 of the original 14 items were pre-verified resolved by earlier frontend work — see the `~~RESOLVED~~` entries above.)
