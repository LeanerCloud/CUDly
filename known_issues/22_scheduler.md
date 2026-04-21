# Known Issues: Scheduler / Collection Pipeline

> **Audit status (2026-04-21):** `2 needs triage · 0 resolved (new file)`

Surfaced during the 2026-04-21 production troubleshooting that
followed the Azure pager fix (commit `cbd1bdb86`) and the dedupe-by-
natural-key fix (commit `9fa4170a1`). Both items live in the
collection pipeline (`internal/scheduler/scheduler.go` +
`internal/config/store_postgres_recommendations.go`).

## MEDIUM: `fanOutPerAccount` swallows per-account errors and reports the provider as successful

**File**: `internal/scheduler/scheduler.go::fanOutPerAccount` (≈ lines 298-326) + `CollectRecommendations` loop (≈ lines 161-176)

**Description**: `fanOutPerAccount` runs the per-account fetch in `errgroup` goroutines and on failure does:

```go
recs, err := fn(gctx, acct)
if err != nil {
    logging.Errorf("%s account %s (%s): %v", providerLabel, acct.Name, acct.ExternalID, err)
    return nil // never fail the whole group — partial is fine
}
```

The top-level `collectAzureRecommendations` (and the GCP equivalent) then returns the resulting slice with `err == nil`, so the outer loop in `CollectRecommendations` adds the provider to `successfulProviders`. `persistCollection` then evicts every stale row for that provider — even when every account inside it failed.

Net effect: an Azure tenant where *all* subscriptions hit a credential or permission error produces:

- the provider's existing rows wiped (because the provider is "successful" with zero new rows);
- no `last_collection_error` set;
- no operator-visible signal beyond the per-account `[ERROR]` lines in the Lambda log.

**Impact**: A region-wide credential expiry, a tenant-wide IAM regression, or even a typo in a single subscription's federation config can silently zero-out the Recommendations page for that provider with no banner. Inferring the failure mode requires log spelunking. The current GCP `serene-bazaar-666` 403 is masked by exactly this path (see logs `2026-04-21T16:28:22Z`+ — `[ERROR] GCP account ... Required 'compute.regions.list' permission` is logged but the provider is still reported as successful in the next "Collected N recommendations" line).

**Status:** ❓ Needs triage

### Implementation plan

**Goal:** A provider whose every account failed should NOT be added to `successfulProviders` and SHOULD set a per-provider entry in `last_collection_error` so the freshness banner surfaces "Azure: 0/3 accounts collected (last error: …)".

**Files to modify:**

- `internal/scheduler/scheduler.go::fanOutPerAccount` — change the return to also surface a count of `(succeeded, failed)` accounts plus the most-recent error string. Avoid switching to "fail the whole group" — partial success is genuinely valuable.
- `internal/scheduler/scheduler.go::CollectRecommendations` — when a provider returns `0/N succeeded`, treat the provider as failed (not in `successfulProviders`), and pass the error string into `persistCollection`'s failure map.
- `internal/scheduler/scheduler.go::persistCollection` — extend `SetRecommendationsCollectionError` (or a sibling) so the per-provider failure is persisted alongside or in place of the global error string.

**Steps:**

1. Define a small `accountOutcome` struct — `{Recs []RecommendationRecord, FailedCount int, LastErr string}`.
2. `fanOutPerAccount` returns `accountOutcome` instead of just the slice. Failures are counted under a mutex.
3. The three top-level `collect{AWS,Azure,GCP}Recommendations` callers translate `outcome.FailedCount == len(accounts) && len(accounts) > 0` into an error returned up the stack so `CollectRecommendations` routes the provider into `failedProviders`.
4. Update tests: `TestScheduler_CollectRecommendations_AllProviders` and the Azure/GCP fanOut tests need a "all accounts failed" case.

**Edge cases:**

- Zero registered accounts AND ambient credentials available (current AWS fallback) — must keep treating as success.
- Partial success (1/3 accounts succeeded) — should remain a `successfulProviders` entry; the per-provider banner can show "2 accounts failed" as a soft warning rather than blanking the data.

**Test plan:**

- `TestFanOutPerAccount_AllAccountsFail` — 3 accounts, all return errors. Assert outcome reports 3 failures + the last error string.
- `TestScheduler_CollectRecommendations_ProviderFullyFailed` — Azure with 2 mock accounts both returning credential errors. Assert provider lands in `failedProviders` map and `successfulProviders` does NOT include it.
- `TestPersistCollection_PerProviderError` — verify the per-provider failure surfaces via the freshness API.

**Verification:** `go test -short ./internal/scheduler/... ./internal/config/...`

**Effort:** `medium` (touches the scheduler API surface; needs care to avoid regressing partial-success behaviour).

## LOW: Natural-key dedupe drops non-winning term/payment variants

**File**: `internal/config/store_postgres_recommendations.go::dedupeByNaturalKey` (added in commit `9fa4170a1`) + the `recommendations` table's UNIQUE constraint

**Description**: Commit `9fa4170a1` introduced `dedupeByNaturalKey` to fix a real production crash:

```text
ERROR: ON CONFLICT DO UPDATE command cannot affect row a second time (SQLSTATE 21000)
```

The crash happened because Azure's reservation recommendations API returns multiple `term` + `payment_option` variants per `(account, provider, service, region, resource_type)` SKU. The DB's natural-key UNIQUE INDEX doesn't include `term` or `payment_option`, so two variants in one batch collide on `ON CONFLICT`.

The dedupe keeps the highest-savings variant per natural key. This is correct for *today*'s UI (one row per resource shape, sorted by savings) but it silently drops the rest. If the UI ever wants to render a "1yr vs 3yr" toggle or a payment-option chooser, the data is no longer in the cache — the backend would have to refetch from Azure on every render.

**Impact**: Latent. No user-facing bug today because the UI doesn't render per-term variants. Becomes a bug the moment a per-term feature lands and the implementer assumes the DB has all variants.

**Status:** ❓ Needs triage

### Implementation plan

**Goal:** Broaden the natural key to `(account_key, provider, service, region, resource_type, term, payment_option)` and drop the dedupe call. This stores every variant exactly once and keeps `ON CONFLICT` collision-free.

**Files to modify:**

- `internal/database/postgres/migrations/000028_*.up.sql` (new) — add `term` and `payment_option` to the `recommendations` UNIQUE INDEX. Use a transactional `DROP INDEX … REPLACE INDEX` pattern so the table never has zero unique constraints (even briefly — concurrent writes during the swap would otherwise race).
- `internal/config/store_postgres_recommendations.go::insertRecommendationsBatch` — extend the `ON CONFLICT (...)` column list to match.
- `internal/config/store_postgres_recommendations.go::UpsertRecommendations` — remove the `dedupeByNaturalKey` wrap.
- `internal/config/store_postgres_recommendations_dedup_test.go` — delete (no longer needed) once the schema migration lands.

**Steps:**

1. Land the migration first in a separate commit so the new UNIQUE constraint is in place before any writer expects it.
2. Update the upsert + remove the dedupe in a follow-up commit.
3. Delete the dedupe helper + its tests in a third commit.

**Edge cases:**

- Existing rows in production already have de-duped data. The migration must NOT fail if duplicate `(account, provider, service, region, type, term, payment_option)` rows somehow exist. Add a pre-flight `SELECT ... GROUP BY ... HAVING COUNT(*) > 1` log so the operator can spot any pre-existing duplicates before the constraint goes on.
- AWS already populates `term` consistently; this expansion just unlocks the Azure variants.
- Frontend filtering by `term` / `payment` becomes naturally efficient (push down to SQL), which is a net win.

**Test plan:**

- New integration test in `store_postgres_recommendations_test.go::TestUpsertRecommendations_StoresAllTermVariants` — three Azure recs for the same VM in 1yr/3yr/3yr-no-upfront variants; assert all three round-trip through `ListStoredRecommendations`.
- Migration test asserting the new index name + columns.

**Verification:** `go test -tags=integration -count=1 ./internal/config/... ./internal/database/postgres/migrations/...`

**Effort:** `medium` (cross-cuts schema + write path + remove the dedupe helper; needs the multi-commit sequencing above so production is never in an inconsistent state).
