# Known Issues: Frontend Settings

> **Audit status (2026-04-20):** `2 still valid · 4 resolved · 1 not applicable · 0 partially fixed · 0 moved · 0 needs triage`

## ~~HIGH: Unchecked null dereferences via forced element casts~~ — RESOLVED

**File**: `frontend/src/settings.ts:352-358, 403-412`
**Description**: Multiple `document.getElementById()` call sites were cast directly to concrete element types without `| null`, so a missing element produced an uncaught `TypeError` instead of a silent no-op.
**Impact**: Form submission could fail silently in the middle of a save if any one element was missing due to a refactor/typo.
**Status:** ✔️ Resolved

**Resolved by:** Introduced three helpers at the top of `settings.ts`: `byId<T>(id)` (null-safe lookup), `setInputValue(id, value)` (write-site), and `setInputChecked(id, checked)` (checkbox write-site). All write-site forced casts in `openAccountModal`, `populateAwsAccountFields`, and the Azure/GCP branches now route through `setInputValue`/`setInputChecked`. All read-site forced casts in `buildAccountRequest` and `saveAccountCredentialsIfFilled` use `byId<T>(...)?.value ?? ''` or `?.checked ?? default`. The remaining casts of the form `as HTMLXElement | null` are already nullable-typed and safely use optional chaining. TypeScript + Jest (1097 tests) pass.

### Original implementation plan

**Goal:** Every `document.getElementById` cast in account modal code returns a nullable type and accesses `.value`/`.checked` through optional chaining.

**Files to modify:**

- `frontend/src/settings.ts:352-358, 403-412` — replace forced casts in `openAccountModal` and `populateAwsAccountFields`.
- Search the file for `getElementById('...') as HTML` without `| null` — there are likely more call sites elsewhere in the Azure/GCP helper branches.
- `frontend/tests/settings.test.ts` (new or existing) — add a JSDOM test that removes one of the IDs and verifies no throw.

**Steps:**

1. Introduce a small helper: `function byId<T extends HTMLElement>(id: string): T | null { return document.getElementById(id) as T | null; }` at the top of `settings.ts`.
2. Replace every `(document.getElementById('X') as HTMLInputElement).value = v` with `const el = byId<HTMLInputElement>('X'); if (el) el.value = v;` or the existing `?.value` idiom used by newer code (lines 380-388).
3. Replace every `.checked` write with the same null-safe pattern.
4. Treat read-site casts the same way — `(byId<HTMLInputElement>('X'))?.value ?? ''`.

**Edge cases the fix must handle:**

- Elements that are conditionally rendered (e.g. provider-specific blocks hidden via CSS).
- Dirty-tracking snapshot in `getFieldValue` — already null-safe; do not regress.

**Test plan:**

- Add `openAccountModal_missingElement_doesNotThrow` — render the modal HTML without `account-id`, assert `openAccountModal('aws')` does not throw.
- Existing happy-path tests continue to pass.

**Verification:**

- `cd frontend && npx tsc --noEmit`
- `cd frontend && npm test`
- Manual: open the account modal with DevTools, temporarily delete `#account-id` from the DOM, re-open modal, confirm no console exception.

**Effort:** `small`

## ~~HIGH: Wrong field used to initialise `account-aws-bastion-role-arn` in edit mode~~ — NOT APPLICABLE

**File**: `frontend/src/settings.ts:407`
**Description**: The original write-up assumed there were two separate backend fields — a general `aws_role_arn` and a distinct `aws_bastion_role_arn` — and flagged the frontend for pre-populating the bastion input from the general field.
**Status:** 🚫 Not applicable — there is only one backend column.

**Rationale:** `config.CloudAccount.AWSRoleARN` (see `internal/config/types.go:225`) is the single target role assumed by CUDly regardless of the auth-mode path used to get there: direct `role_arn`, `bastion` (target role assumed via bastion creds), or `workload_identity_federation` (role assumed via OIDC). Only one input is ever visible at a time (via `updateAwsAuthModeFields`), and the save path writes whichever input is visible back to `req.aws_role_arn`. Pre-filling all three inputs with the same stored value is intentional so switching auth modes mid-edit does not blank the field that just became visible. Added an inline comment in `populateAwsAccountFields` documenting the rationale so the next reader doesn't reach the same wrong conclusion.

### Original implementation plan

**Goal:** The bastion role ARN input is populated from the correct backend field (or left blank in edit mode), and saving does not clobber the stored bastion role.

**Files to modify:**

- `frontend/src/api/accounts.ts` — confirm/extend `CloudAccount` to expose the bastion target role (e.g. `aws_bastion_role_arn`) if the backend returns it. If the backend does not currently return it, align with backend types first.
- `frontend/src/settings.ts:407` — stop reusing `aws_role_arn`; either use the correct field or leave the input blank on edit.
- `frontend/src/settings.ts` — update `buildAccountRequest` (around the bastion save branch) to send the corrected field name.

**Steps:**

1. Grep the backend Go types for the bastion role ARN field name (`bastion_role_arn` is likely).
2. If present in backend response, add `aws_bastion_role_arn?: string` to `CloudAccount` and `CloudAccountRequest`.
3. Change line 407 to read from the correct source; if omitted for security (same as access keys), leave the input blank and show placeholder text instead.
4. Ensure the submit path writes the field only when the user typed a value, to avoid erasing the stored role when the user didn't touch the field.

**Edge cases the fix must handle:**

- Fresh account creation (account is undefined) — field must start empty.
- Editing a bastion-mode account where the user doesn't retype the role — must not send empty string.
- Switching auth modes mid-edit — only submit the bastion role when `aws_auth_mode === 'bastion'`.

**Test plan:**

- Unit test `populateAwsAccountFields_editMode_usesBastionRoleArn` — mock account with different `aws_role_arn` and bastion role; assert bastion field != role field.
- Regression test that the bastion role is preserved when editing without typing.

**Verification:**

- `cd frontend && npm test`
- Manual: create bastion account, edit it, confirm bastion role field is populated correctly or blank; save; confirm backend still has original bastion role.

**Related issues:** `06_frontend_api_layer.md` (types alignment)

**Effort:** `small`

## HIGH: Race condition — multiple concurrent save submissions with no in-flight guard

**File**: `frontend/src/settings.ts:893-941`
**Description**: `saveGlobalSettings` has no in-progress flag, no button disable, no debounce. Rapid clicks of the Save button launch concurrent `updateConfig` + `updateServiceConfig` batches. Last-write-wins ordering is non-deterministic because per-service saves run through `Promise.all`.
**Impact**: Duplicate API calls; under concurrent submission the final persisted state may be a mix of both forms. Particularly harmful when the user makes changes, clicks Save, then quickly edits again and clicks Save again.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Only one save operation is in flight at a time; the Save button is disabled while saving and re-enabled in `finally`.

**Files to modify:**

- `frontend/src/settings.ts:893-941` — add an in-flight guard around `saveGlobalSettings`.
- `frontend/src/settings.ts` (wiring) — disable/re-enable the save button.

**Steps:**

1. Add a module-level `let saveInFlight = false;` near the other module state (around line 10).
2. At the top of `saveGlobalSettings`, return early if `saveInFlight` is `true`.
3. Set `saveInFlight = true` before the `try`, clear it in a `finally` block.
4. In the same `try`/`finally`, toggle `button.disabled = true/false` for the Save button (grab via `e.submitter` if available, else a stable ID).
5. If the form triggers the save via Enter key, ensure the button disable still works — `e.submitter` may be null, so also disable by ID.

**Edge cases the fix must handle:**

- Save button re-enabled on both success and error paths.
- Concurrent submission via keyboard Enter — guarded by `saveInFlight` flag even if button lookup fails.

**Test plan:**

- JSDOM test: dispatch two submit events back-to-back; assert `updateConfig` API mock was called only once.
- Manual: open DevTools Network, click Save rapidly 3×, confirm only one PUT `/config` request fires.

**Verification:**

- `cd frontend && npm test`
- Manual save flow as above.

**Effort:** `small`

## ~~MEDIUM: `propagateTermToServices` uses wrong ID for Azure CosmosDB~~

**File**: `frontend/src/settings.ts:714`
**Description**: Used `'azure-cosmos-term'` but `SERVICE_FIELDS` declares `'azure-cosmosdb-term'`.
**Status:** ✔️ Resolved

**Resolved by:** `0cb45a370` — `propagateTermToServices` (settings.ts:762-769) now iterates `SERVICE_FIELDS`, eliminating the typo'd ID.

## ~~MEDIUM: `propagateTermToServices` omits several service IDs~~

**File**: `frontend/src/settings.ts:710-728`
**Description**: `azure-redis-term`, `gcp-memorystore-term`, `gcp-storage-term` were missing from the propagation list.
**Status:** ✔️ Resolved

**Resolved by:** `0cb45a370` — propagation is now derived from the single `SERVICE_FIELDS` source of truth (settings.ts:23-38), so all declared services are always covered.

## MEDIUM: `saveGlobalSettings` silently resets per-service coverage to global default

**File**: `frontend/src/settings.ts:927`
**Description**: `coverage: settings.default_coverage` is written unconditionally for every service on save. There is no per-service coverage UI field, and `loadedServiceConfigs` is not consulted for the existing per-service coverage override. Every Save overwrites any backend per-service coverage customisation.
**Impact**: Silent data-loss for any operator who set per-service coverage through the API or a future UI. Global changes blast over per-service overrides invisibly.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Per-service coverage is preserved through Save; only services that the user actually touched in the UI are updated.

**Files to modify:**

- `frontend/src/settings.ts:915-930` — carry forward `coverage` from `loadedServiceConfigs` when there is no per-service UI input.
- `frontend/src/settings.ts` (optional) — if a per-service coverage input should exist, add it to `SERVICE_FIELDS`; otherwise leave UI untouched and fix the save path only.

**Steps:**

1. Inside the `SERVICE_FIELDS.map` at line 915, look up the matching `base` config via `loadedServiceConfigs.find(...)` (already done on line 920).
2. Change `coverage: settings.default_coverage` to `coverage: base?.coverage ?? settings.default_coverage`.
3. Do the same for `ramp_schedule`, `include_engines`, etc. (the surrounding comment already warns these are lost; tighten the map to preserve them).

**Edge cases the fix must handle:**

- First-ever save for a service that has no `loadedServiceConfigs` entry — fall back to `default_coverage`.
- User changing the global default coverage — document in UI that per-service overrides are preserved and not mass-updated.

**Test plan:**

- Unit test: `saveGlobalSettings_preservesPerServiceCoverage` — seed `loadedServiceConfigs` with coverage 60 for aws/rds, global default 80; invoke save; assert the mock API was called with 60 for rds and 80 for services missing in the list.

**Verification:**

- `cd frontend && npm test`
- Manual: via API, set aws/rds coverage to 60; open settings UI, click Save without touching anything; verify API call body preserves 60.

**Related issues:** `06_frontend_api_layer.md#MEDIUM: ServiceConfig Missing ramp_schedule Field` (now resolved — same code path should preserve `ramp_schedule` too).

**Effort:** `small`

## LOW: Event listeners never removed

**File**: `frontend/src/settings.ts:576-665, 83-93`
**Description**: `addEventListener` calls with no teardown. In an SPA that navigates away and back to Settings, listeners accumulate — every re-entry registers another handler on the same element.
**Impact**: Duplicate API calls (one per accumulated listener) and growing memory usage over long sessions.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Settings-tab listeners are either registered exactly once or cleaned up with an `AbortController` on tab leave.

**Files to modify:**

- `frontend/src/settings.ts:664-718` (`setupSettingsHandlers`) — accept an `AbortSignal` and pass it to every `addEventListener`.
- `frontend/src/index.ts` or wherever Settings is mounted — create/abort a `settingsAbortController` per tab activation.

**Steps:**

1. Change `setupSettingsHandlers()` signature to `setupSettingsHandlers(signal: AbortSignal)`.
2. Add `{ signal }` as the third argument to every `addEventListener` call.
3. At the caller, create `let settingsController: AbortController | null = null;`. When the Settings tab activates: `settingsController?.abort(); settingsController = new AbortController(); setupSettingsHandlers(settingsController.signal);`.
4. Do the same for `setupDirtyTracking` (settings.ts:92) and any other `addEventListener` in this file.

**Edge cases the fix must handle:**

- First Settings activation — no previous controller to abort.
- Deep-link navigation directly to Settings — controller created on first render.

**Test plan:**

- Unit test: mount Settings tab twice in JSDOM; assert `addEventListener` spy was called on the same element only once after first abort.

**Verification:**

- `cd frontend && npm test`
- Manual: Chrome DevTools Memory profiler — switch to Settings 20×; detached-listeners count should stay flat.

**Effort:** `medium`
