# Known Issues: Config Types and PostgreSQL Store

> **Audit status (2026-04-20):** `0 still valid · 7 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~CRITICAL: `SavePurchaseHistory` silently drops `cloud_account_id`~~ — RESOLVED

**File**: `internal/config/store_postgres.go:812-847`
**Description**: The historic INSERT column list lacked `cloud_account_id`; the `queryPurchaseHistory` SELECT similarly omitted it. Every save discarded the field.
**Impact**: Multi-cloud account attribution permanently lost on every purchase history write.
**Status:** ✔️ Resolved

**Resolved by:** `96c1aceab` — the INSERT (line 815-819) and both SELECTs (`GetPurchaseHistory` line 852-854, `GetAllPurchaseHistory` line 867-869) now include `cloud_account_id`; the scan adds `cloudAccountID sql.NullString` (line 889).

## ~~HIGH: `TransitionRIExchangeStatus` is not atomic despite its comment~~ — RESOLVED

**File**: `internal/config/store_postgres.go:1051-1096`
**Description**: The old implementation did a status-read + expiry-read + UPDATE in three round-trips without a transaction — concurrent callers could double-transition.
**Impact**: Double-approval risk for RI exchanges.
**Status:** ✔️ Resolved

**Resolved by:** `96c1aceab` — now uses a single `UPDATE ... WHERE id = $1 AND status = $2 AND (expires_at IS NULL OR expires_at > NOW()) RETURNING ...`. Zero-row results are diagnosed by `diagnoseTransitionFailure` (line 1080), which distinguishes "not found" vs "expired" vs "wrong status" with one targeted query.

## ~~HIGH: `SaveRIExchangeRecord` does not persist `created_at` / `updated_at`~~ — RESOLVED

**File**: `internal/config/store_postgres.go:935-984`
**Description**: The old INSERT omitted `created_at`/`updated_at` columns; struct values were silently discarded (or rejected, depending on DB defaults).
**Impact**: Timestamp mismatches between Go struct and DB row.
**Status:** ✔️ Resolved

**Resolved by:** INSERT now includes `created_at, updated_at` (line 953) with matching args (lines 975-976), and the function explicitly normalises `record.CreatedAt` / `record.UpdatedAt` before executing (lines 941-945).

## ~~MEDIUM: `TransitionRIExchangeStatus` expiry check uses fragile inverted-error idiom~~ — RESOLVED

**File**: `internal/config/store_postgres.go:1079-1096`
**Description**: The old expiry guard treated `pgx.ErrNoRows` as the "not expired" success path.
**Impact**: Inverted-error idiom made the guard easy to break on refactor.
**Status:** ✔️ Resolved

**Resolved by:** The new atomic UPDATE (see HIGH above) removed the guard entirely. `diagnoseTransitionFailure` now explicitly scans a boolean `(expires_at IS NOT NULL AND expires_at <= NOW())` (line 1084), so the expiry vs not-found logic is no longer hidden behind inverted-`ErrNoRows`.

## ~~MEDIUM: `GetPendingExecutions` is unbounded~~ — RESOLVED

**File**: `internal/config/store_postgres.go:673-688`
**Description**: The query previously had no `LIMIT` clause, so every scheduler tick could load an arbitrary number of rows into memory.
**Impact**: Unbounded memory consumption in large deployments.
**Status:** ✔️ Resolved

**Resolved by:** The query now ends with `ORDER BY scheduled_date ASC LIMIT 1000` (line 683-684).

## ~~LOW: `ListServiceConfigs` is unbounded~~ — RESOLVED

**File**: `internal/config/store_postgres.go:275-326`
**Description**: `SELECT … FROM service_configs ORDER BY provider, service` had no `LIMIT`; inconsistent with sibling list queries.
**Status:** ✔️ Resolved

**Resolved by:** Added `LIMIT 1000` to the query. The doc comment now explains why 1000 is roughly three orders of magnitude above the realistic upper bound (bounded providers × services × per-service variants stays under ~150 even with generous growth), and that the cap is defence-in-depth against a compromised admin inserting pathological rows.

### Original implementation plan

**Goal:** Cap `ListServiceConfigs` with an explicit limit for consistency and defence-in-depth.

**Files to modify:**

- `internal/config/store_postgres.go:275-326` — add `LIMIT 1000` (or document the choice)
- `internal/config/store_postgres_test.go` — optional test asserting the limit

**Steps:**

1. Append `LIMIT 1000` to the query.
2. Add a short comment explaining why 1000 is enough (service × provider combinations).

**Test plan:**

- Existing tests should still pass; no behavioural change for realistic data volumes.

**Verification:**

- `go test ./internal/config/...`

**Effort:** `small`

## ~~LOW: `ListCloudAccounts` ILIKE binds the same value twice as two parameters~~ — RESOLVED

**File**: `internal/config/store_postgres.go:1481-1488`
**Description**: The `filter.Search` branch bound the `like` string twice (`$i` and `$i+1`) and incremented `i` by 2, making future filter additions prone to offset mistakes.
**Status:** ✔️ Resolved

**Resolved by:** Changed the ILIKE clause to reference the same `$N` twice (Postgres allows parameter reuse) and appended the `like` value once. `i` increments by 1 as for every other filter branch. Two pgxmock tests were updated (`TestPGXMock_ListCloudAccounts_WithSearchFilter`, `TestPGXMock_ListCloudAccounts_WithAllFilters`) to reflect the new single-arg shape.

### Original implementation plan

**Goal:** Reduce the index arithmetic by binding `like` once.

**Files to modify:**

- `internal/config/store_postgres.go:1481-1488`

**Steps:**

1. Change the clause to `(ca.name ILIKE $%d ESCAPE '\\' OR ca.external_id ILIKE $%d ESCAPE '\\')` using `i` twice.
2. Append `like` to `args` once and increment `i` by 1.
3. Update subsequent filter branches that rely on `i` offsets.

**Edge cases the fix must handle:**

- Additional future filters — make sure they read the correct `$N`.

**Test plan:**

- Existing `ListCloudAccounts` search tests must continue to pass.

**Verification:**

- `go test ./internal/config/...`

**Effort:** `small`
