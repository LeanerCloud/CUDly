# Known Issues: Scheduler / Collection Pipeline

> **Audit status (2026-04-21):** `2 needs triage · 0 resolved (new file)`

Surfaced during the 2026-04-21 production troubleshooting that
followed the Azure pager fix (commit `cbd1bdb86`) and the dedupe-by-
natural-key fix (commit `9fa4170a1`). Both items live in the
collection pipeline (`internal/scheduler/scheduler.go` +
`internal/config/store_postgres_recommendations.go`).

## ~~MEDIUM: `fanOutPerAccount` swallows per-account errors and reports the provider as successful~~ — RESOLVED

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

**Status:** ✔️ Resolved

**Resolved by:** `internal/scheduler/scheduler.go::fanOutPerAccount` now returns `([]RecommendationRecord, accountOutcome)` where `accountOutcome` carries `{SucceededCount, FailedCount, LastErr}`. The same `sync.Mutex` that guards the recs accumulator now also guards the outcome counters — single critical section, no second mutex. `LastErr` is formatted with the failed account's Name + ExternalID so the freshness banner can surface useful context. `collectAWSRecommendations`, `collectAzureRecommendations`, `collectGCPRecommendations` all check `outcome.FailedCount == len(accounts) && len(accounts) > 0` and return `errAllAccountsFailed(...)` in that case; `CollectRecommendations` then routes the provider into `failedProviders` (line ~167), `joinProviderErrors` aggregates, and `SetRecommendationsCollectionError` persists. AWS's ambient-credential fallback (`collectAWSAmbient`, len(accounts)==0) is untouched. Three new tests in `scheduler_test.go`: `TestFanOutPerAccount_AllAccountsFail`, `TestFanOutPerAccount_PartialSuccess`, `TestFanOutPerAccount_ZeroAccounts`. The existing `TestFanOutPerAccount_RespectsParallelismLimit` was updated for the new two-return signature.

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

## ~~LOW: Natural-key dedupe drops non-winning term/payment variants~~ — RESOLVED

**File**: `internal/config/store_postgres_recommendations.go::dedupeByNaturalKey` (added in commit `9fa4170a1`) + the `recommendations` table's UNIQUE constraint

**Description**: Commit `9fa4170a1` introduced `dedupeByNaturalKey` to fix a real production crash:

```text
ERROR: ON CONFLICT DO UPDATE command cannot affect row a second time (SQLSTATE 21000)
```

The crash happened because Azure's reservation recommendations API returns multiple `term` + `payment_option` variants per `(account, provider, service, region, resource_type)` SKU. The DB's natural-key UNIQUE INDEX doesn't include `term` or `payment_option`, so two variants in one batch collide on `ON CONFLICT`.

The dedupe keeps the highest-savings variant per natural key. This is correct for *today*'s UI (one row per resource shape, sorted by savings) but it silently drops the rest. If the UI ever wants to render a "1yr vs 3yr" toggle or a payment-option chooser, the data is no longer in the cache — the backend would have to refetch from Azure on every render.

**Impact**: Latent. No user-facing bug today because the UI doesn't render per-term variants. Becomes a bug the moment a per-term feature lands and the implementer assumes the DB has all variants.

**Status:** ✔️ Resolved

**Resolved by:** new migration `000032_recommendations_add_term_payment_to_key.up.sql` adds `term INT NOT NULL DEFAULT 0` and `payment_option TEXT NOT NULL DEFAULT ''` columns (metadata-only ALTER), runs a `DO $$ ... RAISE NOTICE` pre-flight that warns on any pre-existing 7-tuple duplicates, then swaps the unique index `recommendations_natural_key_idx` from the 5-column shape to the 7-column shape `(account_key, provider, service, region, resource_type, term, payment_option)`. Pre-migration legacy rows land at `(0, '')` defaults — uniform suffix because the prior dedupe guaranteed at-most-one row per old natural key — and naturally age out via `UpsertRecommendations`' `collected_at < $now` eviction on the next scheduler tick. The `.down.sql` reverses in DROP-INDEX → DROP-COLUMNS → CREATE-INDEX order.

`internal/config/store_postgres_recommendations.go::insertRecommendationsBatch` now binds `rec.Term` + `rec.Payment` (already populated by the scheduler's `convertRecommendations`), bumps `colsPerRow` 9 → 11, and the `ON CONFLICT (...)` column list matches the new 7-column key. `UpsertRecommendations` no longer wraps the input through `dedupeByNaturalKey` — the broader key makes the dedupe unnecessary.

**Helper removed:** the `dedupeByNaturalKey` function and its test file `internal/config/store_postgres_recommendations_dedup_test.go` were deleted once migration 000032 was confirmed clean in production. The doc-comment above `UpsertRecommendations` now points at the migration as the reason the dedupe is no longer needed.

New integration test `TestPostgresStore_UpsertRecommendations_StoresAllTermVariants` writes three Azure variants (1yr-upfront, 3yr-upfront, 3yr-no-upfront) for the same SKU and asserts all three round-trip as distinct rows. Pre-fix this would have collapsed to 1.

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

## ~~MEDIUM: Eviction wipes failed accounts' rows when ANY account in the same provider succeeds~~ — RESOLVED

**File**: `internal/config/store_postgres_recommendations.go::UpsertRecommendations` (eviction `DELETE FROM recommendations WHERE collected_at < $1 AND provider = ANY($2)`) + `internal/scheduler/scheduler.go::persistCollection`

**Description**: Sibling bug to the `fanOutPerAccount` partial-account-error fix above. Even after the per-provider error tracking landed, the eviction predicate was still scoped to `provider = ANY($2)`. So when 1 of 3 Azure subscriptions succeeded, all three subscriptions' previous-cycle rows were evicted — the dashboard for the failed two went blank until the next successful collect for them.

Concretely: an Azure tenant with subs `A`, `B`, `C`. Cycle N succeeds for all three. Cycle N+1 only succeeds for `A` (e.g. `B`'s federation token expires). The eviction query deletes every `provider='azure' AND collected_at < $now` row, taking `B`'s and `C`'s with it.

**Impact**: A single per-account credential expiry could blank ~⅔ of the dashboard for an Azure tenant for the rest of a release cycle (until the next successful cycle for the remaining accounts), even though the operator-visible signal said "Azure: 1/3 accounts collected".

**Status:** ✔️ Resolved

**Resolved by:** the eviction predicate is now scoped to (provider, account_key) pairs via a new `[]SuccessfulCollect{Provider, *CloudAccountID}` parameter on `UpsertRecommendations`. The scheduler builds the slice from `accountOutcome.SucceededAccountIDs` (extended in this commit) and passes it through `persistCollection`. nil `CloudAccountID` is converted to `uuid.Nil` at the Go boundary so the join matches the generated `account_key` column for ambient-credential rows; `uuid.Nil` is distinct from any real account UUID, so ambient and registered identities are evicted independently.

The new SQL eviction is `(provider, account_key) IN (SELECT p, a FROM unnest($2::text[], $3::uuid[]) AS t(p, a))`, with two parallel arrays bound from the `[]SuccessfulCollect` slice. Empty `successfulCollects` (every provider + every account failed) makes the IN-list empty → eviction is a no-op → stale rows from the last successful run remain visible.

**Tests** (in `internal/config/store_postgres_recommendations_test.go`):

- `TestPostgresStore_UpsertRecommendations_AccountScopedEviction` — two Azure accounts, partial-collect at t1 only includes acct-1; assert acct-2's rows survive.
- `TestPostgresStore_UpsertRecommendations_AmbientAndRegisteredCoexist` — ambient (nil CloudAccountID) + registered AWS row at t0; partial-collect at t1 only includes the registered account; assert ambient row survives.

The two existing tests (`PartialCollect` for cross-provider and `EvictsStaleInSuccessfulProvider` for same-account stale eviction) were updated to the new `[]SuccessfulCollect` shape and continue to pass.
