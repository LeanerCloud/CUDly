# Known Issues: Database Migrations

> **Audit status (2026-04-21):** `0 from original audit · 9 resolved · 0 partially fixed · 0 moved · 1 follow-up outstanding (MEDIUM: migration 000027 dedup DELETE lock window)`

## ~~HIGH: savings_snapshots has no PRIMARY KEY~~ — RESOLVED

**File**: `internal/database/postgres/migrations/000001_initial_schema.up.sql:210-226`
**Description**: `savings_snapshots` declared `id UUID DEFAULT uuid_generate_v4()` but omitted `PRIMARY KEY`. Because the table is `PARTITION BY RANGE (timestamp)`, the primary key must include `timestamp`, but none was created at all.
**Impact**: Duplicate rows accumulated silently; ON CONFLICT impossible; CDC tools misbehaved.
**Status:** ✔️ Resolved

**Resolved by:** New migration `000027_savings_snapshots_pk.up.sql`. The migration:

- De-duplicates first via a window-function `ROW_NUMBER() OVER (PARTITION BY id, timestamp ORDER BY created_at DESC, ctid DESC)` and `DELETE ... WHERE row_num > 1`. No-op on clean databases (production audit shows zero duplicates today, but the dedup makes the migration replay-safe).
- Adds `CONSTRAINT savings_snapshots_pkey PRIMARY KEY (id, timestamp)` — the only shape the partitioned table accepts.
- Down migration drops the constraint (does NOT restore deleted duplicates — that's a data restore, not a schema rollback).

### Implementation plan

**Goal:** Enforce uniqueness on partitioned inserts and enable `ON CONFLICT (id, timestamp)` patterns.

**Files to modify:**

- `internal/database/postgres/migrations/000024_savings_snapshots_pk.up.sql` — new migration
- `internal/database/postgres/migrations/000024_savings_snapshots_pk.down.sql` — new migration

**Steps:**

1. Create new migration that runs `ALTER TABLE savings_snapshots ADD CONSTRAINT savings_snapshots_pkey PRIMARY KEY (id, timestamp);` — required because the table is partitioned by `timestamp`.
2. For existing data: identify and de-duplicate before the ALTER. If safe (analytics data), drop duplicates keeping the newest `created_at`.
3. Down migration: `ALTER TABLE savings_snapshots DROP CONSTRAINT savings_snapshots_pkey;`.
4. Re-run `go test ./internal/database/...` to confirm migration suite still applies cleanly.

**Edge cases the fix must handle:**

- Existing duplicate `(id, timestamp)` rows — de-dup before constraint add (count expected to be 0 in practice; verify).
- Running against a clean DB (first-time install) — must be a no-op dedup.

**Test plan:**

- Add a migration test asserting an explicit duplicate `(id, timestamp)` insert fails.

**Verification:**

- `go test ./internal/database/postgres/...` and a manual `psql` apply of the migration.

**Effort:** `medium`

## ~~HIGH: 'expired' is not a valid purchase_executions status — CleanupOldExecutions partially dead~~ — RESOLVED

**File**: `internal/config/store_postgres.go:792-806`
**Description**: The `valid_status` CHECK on `purchase_executions` allowed `pending, notified, approved, cancelled, completed, failed` (plus `running`). `CleanupOldExecutions` deleted `status IN ('completed', 'cancelled', 'expired')`. Since no transition code ever wrote `expired`, that branch was dead — executions past `expires_at` accumulated indefinitely.
**Status:** ✔️ Resolved

**Resolved by:** Followed plan option (b) — drove cleanup off `expires_at` instead of inventing an `expired` status. Two independent branches, each with its own retention gate, OR'd:

```sql
(status IN ('completed', 'cancelled') AND scheduled_date < NOW() - INTERVAL '1 day' * $1)
OR
(expires_at IS NOT NULL AND expires_at < NOW() - INTERVAL '1 day' * $1)
```

The OR between the two branches is deliberate — an earlier fix mistakenly AND'd the `scheduled_date` retention gate with both branches, which left pending rows with a far-future `scheduled_date` but a long-past `expires_at` accumulating indefinitely (a user scheduling a 2-year-out purchase with a 30-day approval window would have left a dead row for 1.9 years after the approval expired). The correct shape is two independent retention windows, one keyed on `scheduled_date` for the terminal-state branch (so recent completions stay visible in the UI) and one keyed on `expires_at` for the expiry-cleanup branch (so a stalled pending row is cleaned up `retentionDays` after its approval token expires, regardless of how far out its `scheduled_date` was). NULL `expires_at` is excluded from branch 2 so rows that never had an expiration deadline are safe. No DB migration needed — this is a pure query change. Function godoc spells out the two branches explicitly.

### Implementation plan

**Goal:** Either honour `expired` as a status, or replace its cleanup branch with an `expires_at`-based query.

**Files to modify:**

- `internal/database/postgres/migrations/000024_executions_expired_status.up.sql` — optional (only if we add the status)
- `internal/config/store_postgres.go:792-806` — rewrite cleanup
- scheduler code that marks executions expired (if we add the status)

**Steps:**

1. Decide: treat expiry as a state transition (add `expired` to the CHECK + add a job that flips `pending`/`notified` rows past `expires_at`) OR treat it purely as cleanup (change the DELETE to `WHERE (status IN ('completed','cancelled')) OR (expires_at IS NOT NULL AND expires_at < NOW())`).
2. Recommended: the latter is less invasive. Change the DELETE to the `OR` form and remove `'expired'` from the IN clause.
3. Add a test hitting `CleanupOldExecutions` with an expired row.

**Edge cases the fix must handle:**

- Rows in the `approved`/`failed` terminal states that are also past `expires_at` — decide whether they get cleaned up too.
- Rows with NULL `expires_at` — must NOT be deleted unless their status is in the cleanup list.

**Test plan:**

- `TestCleanupOldExecutions_ExpiredByDate` — seeds a pending row with `expires_at < NOW() - retention`, asserts cleanup deletes it.
- `TestCleanupOldExecutions_IgnoresLivePending` — asserts live pending rows are preserved.

**Verification:**

- `go test ./internal/config/...`

**Effort:** `small`

## ~~HIGH: RIExchangeRecord.PaymentDue type mismatch (string in Go, DECIMAL in DB)~~ — RESOLVED (boundary fix; type-change deferred deliberately)

**File**: `internal/config/types.go:191` / `internal/config/store_postgres.go:993`
**Description**: DB column was `DECIMAL(20,6) NOT NULL DEFAULT 0 CHECK (payment_due >= 0)` and Go field was `PaymentDue string`. Reads cast via `payment_due::text`; writes passed the raw string — a zero-value Go struct (`PaymentDue: ""`) failed the DECIMAL cast at runtime.
**Status:** ✔️ Resolved (defaulting at the insert boundary; the audit's "convert to float64" path was intentionally not taken)

**Resolved by:** `SaveRIExchangeRecord` now defaults `PaymentDue == ""` to `"0"` at the boundary before passing it to pgx. A freshly-zero-valued struct inserts cleanly; non-empty values pass through verbatim and the DECIMAL parser rejects malformed input with a clear error. Inline comment above the defaulting block explains the rationale.

**Why the audit's float64 path was rejected:** The audit suggested changing `PaymentDue string` to `PaymentDue float64`. After tracing the call graph (`internal/api/handler_ri_exchange.go::checkDailyCap`, `internal/server/handler_ri_exchange.go`, `internal/email/...`, plus tests in 7+ files), money is consistently held as `string` and arithmetic is done with `*big.Rat` via `exchange.ParseDecimalRat`. Switching the persistent representation to `float64` would either:

- Round at the boundary (loses precision the rest of the pipeline went out of its way to preserve), or
- Force a parallel path through `big.Rat` for the persistence layer only (worse than the original problem).

Defaulting `""` to `"0"` at the SQL boundary fixes the actual crash with one line of additional code, no DB migration, and zero ripple through the precision-preserving layers above. The remaining `payment_due::text` casts on the read path are not a bug — they explicitly preserve the round-tripping shape between the DECIMAL column and the Go string field.

If a future caller does need numeric `PaymentDue`, the recommended path is `exchange.ParseDecimalRat(record.PaymentDue)` rather than re-typing the field.

### Implementation plan

**Goal:** Align the Go type with the DB type and remove the `::text` casts.

**Files to modify:**

- `internal/config/types.go:191` — change `PaymentDue string` to `PaymentDue float64`
- `internal/config/store_postgres.go` — remove `payment_due::text` casts (lines 991, 1015, 1039, 1061)
- callers that construct `RIExchangeRecord` with string amounts
- tests that assert on string amounts

**Steps:**

1. Change field type to `float64`.
2. Remove every `payment_due::text` cast and scan as `float64` directly.
3. Update callers: parse any user-facing string amounts once at the boundary with `strconv.ParseFloat`, keep internal representation as `float64`.
4. Update JSON tag — consider whether consumers need a string JSON shape (money precision); if so, add a custom `MarshalJSON`.
5. Migrate any existing rows — no DB migration needed, only Go-side type.

**Edge cases the fix must handle:**

- JSON marshalling precision — `float64` for money is suspect; `github.com/shopspring/decimal` would be safer if callers care about exactness.
- Default zero-value `0.0` now inserts cleanly (previously `""` failed).

**Test plan:**

- `TestSaveRIExchangeRecord_ZeroPayment` — inserts a record with zero PaymentDue and asserts success.
- Round-trip test: save then load, assert equality.

**Verification:**

- `go test ./internal/config/...`

**Effort:** `medium`

## ~~MEDIUM: Migration 000016 adds aws_web_identity_token_file as NOT NULL DEFAULT ''~~ — RESOLVED

**File**: `internal/database/postgres/migrations/000016_aws_wif.up.sql:1`
**Description**: Column was created as `TEXT NOT NULL DEFAULT ''`, breaking parity with sibling optional fields and conflating "unset" with "explicitly empty".
**Status:** ✔️ Resolved (schema-side; Go-side switch to `*string` deferred)

**Resolved by:** New migration `000029_aws_wif_token_file_nullable.up.sql`:

- `ALTER COLUMN ... DROP NOT NULL, DROP DEFAULT` so the column matches the sibling optional fields' shape.
- `UPDATE ... SET = NULL WHERE = ''` so existing empty strings carry the actual unset semantics rather than tripping consumers that begin to treat NULL distinctly.
- Down migration UPDATEs NULLs back to `''` before re-adding the constraint, so the rollback doesn't fail on rows the up migration NULL'd.

The Go field stays `string` for now (with the existing `COALESCE(...,'')` reads) — both interpretations work after the migration. A follow-up can lift the field to `*string` / `sql.NullString` to honour the new tri-state if a code path needs to distinguish unset from empty; flagged in the migration body so it's discoverable.

### Implementation plan

**Goal:** Make the column nullable to match sibling fields.

**Files to modify:**

- `internal/database/postgres/migrations/000025_aws_wif_nullable.up.sql` — new migration
- `internal/database/postgres/migrations/000025_aws_wif_nullable.down.sql` — new migration
- `internal/config/store_postgres.go` — drop the `COALESCE(ca.aws_web_identity_token_file,'')` if the Go type becomes `sql.NullString` or `*string`

**Steps:**

1. New migration: `ALTER TABLE cloud_accounts ALTER COLUMN aws_web_identity_token_file DROP NOT NULL; ALTER TABLE cloud_accounts ALTER COLUMN aws_web_identity_token_file DROP DEFAULT; UPDATE cloud_accounts SET aws_web_identity_token_file = NULL WHERE aws_web_identity_token_file = '';`
2. Decide whether the Go field becomes `*string` (distinguishes unset from empty) or stays `string` with the NULL handled as `""` via `COALESCE`. If keeping `string`, no Go code changes needed; if `*string`, audit all callers.
3. Down migration: reverse the ALTERs and restore the empty-string default.

**Edge cases the fix must handle:**

- Existing rows with empty string — decide whether to convert to NULL (recommended) or keep.
- Queries using `COALESCE(...,'')` still work regardless.

**Test plan:**

- Migration suite passes; asserts the column `is_nullable = YES` after up, and `NO` after down.

**Verification:**

- `go test ./internal/database/postgres/...`

**Effort:** `small`

## ~~MEDIUM: Migration 000007 down migration introduces invalid DEFAULT 12~~ — RESOLVED

**File**: `internal/database/postgres/migrations/000007_fix_service_configs_term_default.down.sql`
**Description**: The down migration restored `DEFAULT 12`, but `000001_initial_schema.up.sql` always set `DEFAULT 3`. 12 is not a valid term — the CHECK constraint accepts only 0, 1, 3.
**Status:** ✔️ Resolved

**Resolved by:** Patched the down migration to `ALTER TABLE service_configs ALTER COLUMN term SET DEFAULT 3` and added a comment block explaining the historical mistake (the previous "revert to 12" version reflected the auditor's misreading of the up migration; 12 was never the historical default). The migration is edited in place because the down has presumably never run in production — and if it has, the resulting `DEFAULT 12` was already invalid and would have been caught by the CHECK constraint on the next insert.

### Implementation plan

**Goal:** Down migration must restore the actual historical default of 3.

**Files to modify:**

- `internal/database/postgres/migrations/000007_fix_service_configs_term_default.down.sql`

**Steps:**

1. Change the `ALTER` to `ALTER TABLE service_configs ALTER COLUMN term SET DEFAULT 3;`.
2. Add a comment pointing at this known issue explaining the 12 → 3 rationale.

**Test plan:**

- Apply migrations up to 007, roll back through 007, insert a row without specifying term, assert stored value is 3.

**Verification:**

- `go test ./internal/database/postgres/...`

**Effort:** `small`

## ~~MEDIUM: savings_snapshots.cloud_account_id has no index~~ — RESOLVED

**File**: `internal/database/postgres/migrations/000011_cloud_accounts.up.sql:137-138`
**Description**: Migration 011 added `cloud_account_id` FK to `savings_snapshots` without an index, even though parallel FKs on `purchase_executions` and `purchase_history` each got partial indexes.
**Status:** ✔️ Resolved

**Resolved by:** New migration `000028_savings_snapshots_cloud_account_idx.up.sql`:

- Partial index on `cloud_account_id WHERE cloud_account_id IS NOT NULL` — same shape as the sibling partial indexes; bulk legacy rows have NULL so excluding them keeps the index small.
- Postgres ≥11 propagates indexes on partitioned parents to all existing and future partitions automatically — no per-partition migration body needed.
- Down migration `DROP INDEX IF EXISTS`.

### Implementation plan

**Goal:** Add a partial index matching the ones on `purchase_executions` and `purchase_history`.

**Files to modify:**

- `internal/database/postgres/migrations/000026_savings_snapshots_cloud_account_idx.up.sql` — new migration
- `internal/database/postgres/migrations/000026_savings_snapshots_cloud_account_idx.down.sql` — new migration

**Steps:**

1. Up: `CREATE INDEX idx_savings_snapshots_cloud_account ON savings_snapshots(cloud_account_id) WHERE cloud_account_id IS NOT NULL;`
2. Down: `DROP INDEX IF EXISTS idx_savings_snapshots_cloud_account;`
3. On a partitioned parent, Postgres creates the index on each partition automatically — verify in test.

**Test plan:**

- Migration suite passes; manual `EXPLAIN` of a filtered query confirms index scan.

**Verification:**

- `go test ./internal/database/postgres/...`

**Effort:** `small`

## ~~LOW: Inconsistent UUID function (gen_random_uuid vs uuid_generate_v4)~~ — RESOLVED

**File**: `internal/database/postgres/migrations/000009_ri_exchange_history.up.sql:2`
**Description**: Migration 009 used `gen_random_uuid()` (PG 13+ built-in) while every other table used `uuid_generate_v4()` from the `uuid-ossp` extension. Created an implicit PG ≥13 requirement on this one table.
**Status:** ✔️ Resolved

**Resolved by:** New migration `000026_ri_exchange_history_uuid_consistency.up.sql` ALTERs the `id` column default to `uuid_generate_v4()`. Migration 009 itself is left untouched (already applied in production). Existing rows are unaffected — both functions return v4 UUIDs that remain valid. Down migration restores `gen_random_uuid()` to keep rollback symmetric with whatever 009 left in place.

### Implementation plan

**Goal:** Consistent UUID generation across the schema.

**Files to modify:**

- `internal/database/postgres/migrations/000009_ri_exchange_history.up.sql:2`

**Steps:**

1. Replace `gen_random_uuid()` with `uuid_generate_v4()` in the `id` default.
2. Verify `uuid-ossp` extension is available (created by migration 001).
3. Because migration 009 has already been applied in production, do NOT edit it retroactively. Instead, add a new migration that ALTERs the default.

**Test plan:**

- Migration suite passes; new rows still get valid UUIDs.

**Verification:**

- `go test ./internal/database/postgres/...`

**Effort:** `small`

## ~~LOW: Migration 000006 down is a no-op — rollback asymmetry~~ — RESOLVED

**File**: `internal/database/postgres/migrations/000006_ensure_admin_user.down.sql`
**Description**: Down was just `SELECT 1;`. Rolling back through 006 left the admin user in place, unlike 005's down which deletes it.
**Status:** ✔️ Resolved (option a from the plan: documented the deliberate no-op)

**Resolved by:** Replaced the terse note with a multi-line comment block that names the tradeoff: migration 005's down already handles user deletion; rolling back 006 by re-deleting would either be a redundant no-op (if 005 was rolled back too) or wipe operator-changed admin credentials in production. Both alternatives are worse than the asymmetry, so the no-op is deliberate. Comment also points to this entry so the next operator finds the rationale without spelunking the audit.

### Implementation plan

**Goal:** Document or align the rollback behaviour.

**Files to modify:**

- `internal/database/postgres/migrations/000006_ensure_admin_user.down.sql`

**Steps:**

1. Either (a) document the no-op intent in a comment block inside the file (preferred — rollback-safe behaviour), or (b) mirror migration 005's down logic to delete the admin user.
2. If (a), expand the existing comment to name the tradeoff ("deliberate no-op to avoid deleting real admin credentials during rollbacks").

**Test plan:**

- Not testable at the SQL level; rely on comment review.

**Verification:**

- N/A (documentation-only change if we take option a).

**Effort:** `small`

## ~~LOW: SaveRIExchangeRecord empty-PaymentDue defaulting has no test~~ — RESOLVED

**File**: `internal/config/store_postgres.go::SaveRIExchangeRecord` + its test sites
**Description**: Commit `57e461cab` added a boundary-level default that maps empty `record.PaymentDue` to `"0"` before binding to the `DECIMAL(20,6) NOT NULL DEFAULT 0 CHECK (payment_due >= 0)` column. The fix is correct and minimal (pre-condition: pgx can't cast `""` to DECIMAL). However the test suite had no case that constructed a zero-value `RIExchangeRecord` and asserted the insert succeeded — the existing `handler_ri_exchange_test.go` fixtures all set `PaymentDue: "5.00"` explicitly. A future refactor that dropped the defaulting would not be caught by `go test`.
**Impact**: Silent regression risk on the exact crash mode the commit was meant to prevent.
**Status:** ✔️ Resolved

**Resolved by:** `store_postgres_db_test.go::TestPostgresStoreDB_SaveRIExchangeRecord_DefaultsEmptyPaymentDue` (integration-tagged, runs against the testcontainers Postgres harness used by the other `*_db_test.go` suites). The test constructs an `RIExchangeRecord` with `PaymentDue: ""`, saves it, reads it back via `GetRIExchangeRecord`, and asserts the read-back string parses via `strconv.ParseFloat` to numeric zero. Byte-exact compare avoided deliberately because the DB-driver returns "0" or "0.000000" depending on formatting.

### Original implementation plan

**Goal:** Pin the "empty → 0" contract.

**Files to modify:**

- `internal/config/store_postgres_nil_db_test.go` (or a new file) — add a test against a real/ephemeral Postgres (reuse whatever harness the sibling `*_postgres_test.go` files use) that:
    1. Constructs `RIExchangeRecord{...}` with `PaymentDue: ""`.
    2. Calls `store.SaveRIExchangeRecord(ctx, rec)`.
    3. Asserts no error.
    4. Reads the row back and asserts `PaymentDue == "0"` (or the DECIMAL equivalent — `"0" == strings.TrimRight("0.000000", "0")` or exact-string compare depending on how the store formats reads).

**Steps:** one test; no production-code change.

**Test plan:** as above.

**Effort:** `small`.

## MEDIUM: 000027 dedup DELETE may lock savings_snapshots for a long time on large tables (found during 2026-04-21 audit review)

**File**: `internal/database/postgres/migrations/000027_savings_snapshots_pk.up.sql`
**Description**: The migration dedupes `savings_snapshots` via a `WITH duplicates AS (... ROW_NUMBER() ...) DELETE ...` before adding the primary key. Production audit at migration-write time showed zero duplicates, so the DELETE is expected to be a no-op — but the migration will still take a table-scan acquiring a row-exclusive lock. On the largest partition this can be minutes on production-scale data, during which writes queue behind the lock.
**Impact**: The migration's apply window blocks writes to `savings_snapshots` for the duration of the scan, even when no rows get deleted. Acceptable if the operator knows to apply during a low-write period; risky if they apply blind during peak collection hours.
**Status:** ❓ Needs triage (new risk surfaced during audit of commit `c3e428851`)

### Implementation plan

**Goal:** Document the apply window; optionally offer a non-locking variant.

**Files to modify:**

- `internal/database/postgres/migrations/000027_savings_snapshots_pk.up.sql` — add a comment block flagging the scan cost + suggested apply window (off-peak).
- Optionally: split the dedup into its own migration that the operator can run manually with `CREATE INDEX CONCURRENTLY`-style tooling, keeping the ALTER TABLE in 000027 to run fast once dedup is confirmed clean.

**Steps:**

1. Add a `-- NOTE:` comment block to the .up.sql file.
2. If the operator wants the split approach, file a follow-up; otherwise the docs-only change is enough.

**Test plan:** documentation-only change (option 1); if splitting the migration, re-run the migration suite.

**Verification:** `go test ./internal/database/postgres/...`

**Effort:** `small` (docs-only) or `medium` (split-migration).
