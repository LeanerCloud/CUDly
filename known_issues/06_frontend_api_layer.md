# Known Issues: Frontend API Layer

> **Audit status (2026-04-20):** `0 still valid · 12 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~CRITICAL: Double Response Body Read in setupAdmin~~

**File**: `frontend/src/api/auth.ts:136-147`
**Description**: `setupAdmin` calls `response.json()` twice on the same `Response` object. The second call always fails with a `TypeError: body stream already read`.
**Impact**: Every successful admin setup call throws an uncaught runtime error. The auth token is never set. Admin setup is completely broken.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — function now branches on `response.ok` and reads the body once per branch (auth.ts:140-151).

## ~~CRITICAL: CreatePlanRequest Field Name Mismatches with Backend PlanRequest~~

**File**: `frontend/src/api/types.ts:86-98`
**Description**: `CreatePlanRequest` sends `payment_option`, `coverage`, `notify_days`, but the backend expects `payment`, `target_coverage`, `notification_days_before`.
**Impact**: Payment option, coverage target, and notification days chosen by the user are silently dropped on every plan creation.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `CreatePlanRequest` (types.ts:98-105) now uses `notification_days_before`, `services`, and `ramp_schedule` matching backend `PlanRequest`.

## ~~CRITICAL: Plan Interface Shape Incompatible with Backend Response~~

**File**: `frontend/src/api/types.ts:69-84`
**Description**: The `Plan` interface declared flat top-level fields that did not exist on the backend.
**Impact**: Plans UI showed blank values for payment option, coverage, and notification days for every plan.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `Plan` (types.ts:84-96) now has `services: Record<string, ServiceConfig>` and `ramp_schedule: PlanRampSchedule`.

## ~~CRITICAL: Recommendation Interface Completely Mismatches Backend~~

**File**: `frontend/src/api/types.ts:45-58`
**Description**: Frontend used `current_cost`, `recommended_cost`, `term_years`, `payment_option` — backend returns `savings`, `upfront_cost`, `monthly_cost`, `term`, `payment`, etc.
**Impact**: Every field access returned `undefined`; recommendations display and purchase execution were broken.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `Recommendation` (types.ts:46-64) now matches `RecommendationRecord` with `savings`, `upfront_cost`, `monthly_cost`, `term`, `payment`, `count`, `resource_type`, etc.

## ~~CRITICAL: DashboardSummary Interface Completely Mismatches Backend~~

**File**: `frontend/src/api/types.ts:23-31`
**Description**: Frontend declared `total_savings`, `monthly_savings`, `active_plans`, etc., none of which exist on the backend.
**Impact**: Dashboard showed all zeros/undefined for every metric.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `DashboardSummary` (types.ts:23-32) now has `potential_monthly_savings`, `total_recommendations`, `active_commitments`, `committed_monthly`, `current_coverage`, `target_coverage`, `ytd_savings`, `by_service`.

## ~~HIGH: UpcomingPurchase Interface Mismatches Backend~~

**File**: `frontend/src/api/types.ts:33-42`
**Description**: Frontend used `id`, `plan_id`, `status`; backend exposes `execution_id`, `step_number`, `total_steps`.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `UpcomingPurchase` (types.ts:34-43) now uses `execution_id`, adds `step_number` and `total_steps`, and drops `plan_id`/`status`.

## ~~HIGH: requestPasswordReset Silently Swallows All Errors~~

**File**: `frontend/src/api/auth.ts:71-81`
**Description**: Function never checked `response.ok`; server errors were invisible.
**Impact**: User saw a false "email sent" confirmation on 4xx/5xx responses.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `requestPasswordReset` (auth.ts:77-86) now checks `!response.ok` and throws with the parsed error message.

## ~~HIGH: getSavingsAnalytics Drops account_ids Filter~~

**File**: `frontend/src/api/history.ts:32-41`
**Description**: `account_ids` declared on `SavingsAnalyticsFilters` but never appended to query params.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `getSavingsAnalytics` (history.ts:39) now joins `account_ids` into the query string when present, mirroring `getHistory`.

## ~~HIGH: CloudAccount.aws_bastion_account_name Wrong JSON Key~~

**File**: `frontend/src/api/accounts.ts:20`
**Description**: Frontend used `aws_bastion_account_name` while backend JSON key is `bastion_account_name`.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `CloudAccount` (accounts.ts:20) and `CloudAccountRequest` (accounts.ts:48) both use `bastion_account_name`.

## ~~HIGH: PurchaseHistory.executed_at Does Not Exist in Backend~~

**File**: `frontend/src/api/types.ts:101-113`
**Description**: Frontend used `executed_at`/`status`/`error`; backend has `timestamp` and no status/error.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `PurchaseHistory` (types.ts:108-126) uses `timestamp` and drops `status`/`error`.

## ~~MEDIUM: Config Interface Missing Backend Fields~~

**File**: `frontend/src/api/types.ts:124-133`
**Description**: Missing `approval_required`, `default_ramp_schedule`, and RI exchange fields; `notification_email` was non-optional despite backend `omitempty`.
**Impact**: Saving config overwrote `approval_required` and RI exchange settings.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `Config` (types.ts:137-154) adds `approval_required`, `default_ramp_schedule`, and six RI exchange fields; `notification_email` is now `notification_email?: string`.

## ~~MEDIUM: ServiceConfig Missing ramp_schedule Field~~

**File**: `frontend/src/api/types.ts:135-142`
**Description**: Missing `ramp_schedule: string`.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — `ServiceConfig` (types.ts:156-170) now has `ramp_schedule?: string`.

## ~~MEDIUM: updateConfig Sends Wrong Type Shape to Backend~~

**File**: `frontend/src/api/settings.ts:29-33`
**Description**: PUT request silently zeroed out backend-only fields.
**Status:** ✔️ Resolved

**Resolved by:** `110094e98` — because `Config` now carries every backend field (including optional ones), `updateConfig` (settings.ts:29-34) no longer clobbers `approval_required`, `default_ramp_schedule`, or RI exchange settings.
