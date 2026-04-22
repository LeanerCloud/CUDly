# Known Issues: Exchange / Offering catalogue

> **Audit status (2026-04-22):** `1 needs triage · 0 resolved (new file)`

Surfaced during the cross-family RI follow-up (`cd440d9ea`,
`feat(exchange): cross-family RI alternatives for specialty + legacy`).
That commit unblocked specialty (`p*/g*/hpc*`) and legacy (`m4/c4/r3`)
families by extending the `peerFamilyGroups` allowlist + a local
dollar-units pre-filter. The hardcoded family list works but has known
limits — this file tracks the migration to a generic, AWS-driven
catalogue.

## MEDIUM: Cross-family alternatives still constrained by hardcoded `peerFamilyGroups`

**File**: `pkg/exchange/reshape.go::peerFamilyGroups` (lines ≈ 87-129) +
`pkg/exchange/reshape.go::candidateFamilies` +
`providers/aws/services/ec2/client.go::FindConvertibleOfferings`

**Description**: `peerFamilyGroups` is a static map keyed by EC2
family. Any new family AWS launches (e.g. `m8g`, `c8gn`, future
specialty shapes) is invisible to the reshape recommender until a
human edits this map. The `candidateFamilies` lookup is also
all-or-nothing: a family either has a hand-curated peer group or
returns nil — there's no "ask AWS what's exchangeable" path.

The `FindConvertibleOfferings` call already enumerates offerings via
`DescribeReservedInstancesOfferings`, but only for the hand-picked
candidate set produced by `peerFamilyGroups`. Inverting that — let
AWS tell us what's available, derive families dynamically — is the
generic shape.

**Impact (today)**:

- New AWS instance families don't surface as exchange targets until
  someone updates the allowlist, even though they're objectively
  exchangeable per AWS's runtime rules.
- The legacy-family entries (m4/c4/r3) had to be added by hand;
  similar future legacy generations will need the same manual touch.
- Operators running in non-mainstream regions (where AWS's offering
  catalogue differs) see suggestions for shapes that may not be
  available locally — `FindConvertibleOfferings` filters them out at
  the offering layer, but the candidate enumeration is still global.
- The dollar-units pre-filter (`passesDollarUnitsCheck`) is the right
  long-term gate, but it's currently gated *behind* the family
  allowlist — alternatives that would pass the units check still get
  excluded if their family isn't whitelisted upstream.

**Impact (latent / scaling)**:

- Each new specialty shape AWS introduces (G6, P6, Trn2, Inf3, …)
  needs a code change. With the post-2024 cadence of EC2 family
  launches, this becomes a maintenance treadmill.
- The `FindConvertibleOfferings` per-rec API fan-out doesn't scale
  cleanly to "show me everything AWS would let me exchange to" — it
  was designed assuming a small candidate set.

**Status**: not yet triaged.

### Implementation plan

**Goal:** Replace `peerFamilyGroups` + per-rec `FindConvertibleOfferings`
with a Postgres-backed convertible-offerings cache populated by the
scheduler. Reshape recommendations enumerate candidates by querying
the cache (filtered by region / scope / duration / currency); the
existing `passesDollarUnitsCheck` filter remains the gate. EC2 family
strings disappear from the policy code entirely — they become an
implicit attribute of whatever rows AWS lists.

**Files to modify (and create):**

- New migration `internal/database/postgres/migrations/000034_convertible_offerings_cache.up.sql` (and `.down.sql` reverse):

  ```sql
  CREATE TABLE convertible_offerings_cache (
      offering_id           TEXT NOT NULL,
      region                TEXT NOT NULL,
      instance_type         TEXT NOT NULL,
      product_description   TEXT NOT NULL DEFAULT 'Linux/UNIX',
      tenancy               TEXT NOT NULL DEFAULT 'default',
      scope                 TEXT NOT NULL DEFAULT 'Region',
      duration_seconds      BIGINT NOT NULL,
      payment_option        TEXT NOT NULL,
      effective_monthly_cost NUMERIC(18,6) NOT NULL,
      normalization_factor  NUMERIC(10,4) NOT NULL,
      currency_code         TEXT NOT NULL DEFAULT 'USD',
      fetched_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
      collected_at          TIMESTAMPTZ NOT NULL,
      PRIMARY KEY (region, instance_type, product_description, tenancy,
                   scope, duration_seconds, payment_option, currency_code)
  );

  CREATE INDEX convertible_offerings_cache_region_dur_idx
      ON convertible_offerings_cache (region, duration_seconds, scope);
  ```

  Natural key matches the inputs to `passesDollarUnitsCheck` — same
  granularity AWS uses for offering uniqueness. `collected_at` enables
  the same stale-row-eviction pattern as `recommendations` (delete
  rows where `collected_at < $now` after a successful refresh).

- New `internal/config/store_postgres_offerings.go`:

  ```go
  type OfferingCacheEntry struct {
      OfferingID            string
      Region                string
      InstanceType          string
      ProductDescription    string
      Tenancy               string
      Scope                 string
      DurationSeconds       int64
      PaymentOption         string
      EffectiveMonthlyCost  float64
      NormalizationFactor   float64
      CurrencyCode          string
      FetchedAt             time.Time
  }

  type OfferingFilter struct {
      Region          string
      DurationSeconds int64
      Scope           string
      Tenancy         string
      ProductDescription string
      CurrencyCode    string
  }

  // ListOfferings returns offerings matching the filter. Empty filter
  // fields mean "no constraint on that field". Used by the reshape
  // layer to enumerate cross-family candidates without the legacy
  // peerFamilyGroups allowlist.
  ListOfferings(ctx context.Context, f OfferingFilter) ([]OfferingCacheEntry, error)

  // UpsertOfferings replaces the offerings for the given (region, scope,
  // duration_seconds) tuple atomically — same shape as
  // UpsertRecommendations' eviction-by-collected_at pattern but scoped
  // to the per-region per-term partition.
  UpsertOfferings(ctx context.Context, collectedAt time.Time, region string, durationSeconds int64, scope string, entries []OfferingCacheEntry) error
  ```

- New `internal/scheduler/scheduler.go::CollectConvertibleOfferings`:
  - Iterates AWS regions enabled by the global config (or configured
    via a new `CUDLY_OFFERING_CACHE_REGIONS` env var to scope smaller
    deployments).
  - Per region, for each `(scope ∈ {Region, Availability Zone},
    duration_seconds ∈ {1y, 3y})` tuple, calls
    `DescribeReservedInstancesOfferings` with `OfferingClass=convertible`,
    paged. ONE pager per (region, scope, duration) — not per instance
    type. Today's `FindConvertibleOfferings` already pages; the new
    populator just drops the `instance-type` filter so AWS returns
    everything.
  - Builds `[]OfferingCacheEntry` and calls `UpsertOfferings`.
  - Wired into the existing 15-min scheduler tick (or a separate,
    longer cadence — pricing changes are rare; hourly is plenty).

- Refactor `pkg/exchange/reshape.go`:
  - **Delete** `peerFamilyGroups` and `candidateFamilies`.
  - **Delete** the `alternativesForTarget(family, targetSize)` hardcoded
    expansion at `analyzeRI`. Replace with a `nil` placeholder; the
    enrichment layer (`AnalyzeReshapingWithOfferings`) populates it.
  - Extend `OfferingLookup`:

    ```go
    // OfferingLookup now resolves "what convertible offerings are
    // available in this region for this term/scope?" rather than the
    // old "look up these specific instance types". Implementations
    // hit the offerings cache (Postgres) instead of the live AWS
    // API, so per-recommendation latency is ~1 SQL query instead of
    // an N×M API fan-out.
    type OfferingLookup func(ctx context.Context, filter OfferingFilter) ([]OfferingOption, error)
    ```

  - `AnalyzeReshapingWithOfferings` builds the filter from the source
    RI's region / scope / duration / currency, calls the lookup once
    per distinct filter, then applies `passesDollarUnitsCheck` to
    every returned offering. Same source-side info plumbing as
    today (`RIInfo.MonthlyCost / CurrencyCode / NormalizationFactor`).

- Refactor the AWS provider side:
  - `providers/aws/services/ec2/client.go::FindConvertibleOfferings`
    becomes the cache populator's helper, not a per-rec API. Keep it
    callable for one-off use but mark it as such; the production
    path is the cache.
  - `internal/api/handler_ri_exchange.go::convertToExchangeTypes` and
    its server-side equivalent thread the new cache-backed
    `OfferingLookup` instead of the live `FindConvertibleOfferings`
    closure.

- Update `internal/config/interfaces.go::StoreInterface` with
  `ListOfferings` + `UpsertOfferings` signatures. Update mocks in:
  - `internal/mocks/stores.go`
  - `internal/api/mocks_test.go`
  - `internal/server/test_helpers_test.go`
  - `internal/purchase/mocks_test.go`
  - `internal/scheduler/scheduler_test.go`
  - `internal/analytics/collector_test.go`

  (Same six mock surfaces touched in commit `e7d6e5b66` for the
  Upsert signature change.)

**Steps:**

1. Land the migration first (separate commit). Empty cache + no
   readers means it's a no-op until the populator ships.
2. Land the cache store (`store_postgres_offerings.go`) + populator in
   the scheduler (`CollectConvertibleOfferings`). Wire it into the
   scheduler tick. Cache fills on the first tick after deploy.
3. Land the reshape refactor: delete `peerFamilyGroups`, switch
   `OfferingLookup` shape, plumb the cache-backed lookup through the
   handlers. Existing tests for cross-family / dollar-units behaviour
   migrate to use a fake `ListOfferings` instead of the static
   allowlist.
4. Drop `FindConvertibleOfferings`'s per-rec API path from the hot
   request path; keep the function for the populator but document
   the change.

Three-commit sequencing means production is never in a state where
the readers expect a populated cache that doesn't exist.

**Edge cases:**

- **Cold-start: cache empty.** First scheduler tick after deploy hasn't
  run yet → reshape recommendations have no candidates. Fallback:
  if `ListOfferings` returns empty, fall back to today's hardcoded
  `peerFamilyGroups` shape for one release cycle (delete in a
  follow-up commit once the cache is reliably populated). Or
  block-and-populate inline on the first reshape request, with a
  ~5-second timeout — pick the simpler one based on cold-start UX.
- **Region mismatch: the source RI's region isn't in the
  CUDLY_OFFERING_CACHE_REGIONS list.** Same fallback as cold-start; log
  a WARN so operators see the gap.
- **Term mismatch: the source RI is 3y but the cache only has 1y rows
  for that region.** `passesDollarUnitsCheck` would reject most cross-
  term comparisons anyway (different upfront amortisation horizons),
  but the populator should cover both terms by default.
- **Currency: source RI is USD, cache row is USD — currency guard is
  a no-op.** No new behaviour. The `currency_code` PK component lets
  GovCloud / China partitions coexist if those ever flow through.
- **Stale cache: pricing changes between AWS publish and our refresh
  tick.** The `auto.go::IsValidExchange=false` skip path catches this
  at exchange time — same belt-and-braces guarantee that ships today.
- **Cache size: ~hundreds of offerings per region per term.** A single
  region's full catalogue (1y + 3y, Region + AZ scope) is on the
  order of 1-2k rows. Total across ~20 active regions: ~30-40k rows.
  Negligible for Postgres.
- **Concurrent populator runs (Lambda re-entrance).** The
  `UpsertOfferings` ON CONFLICT + per-(region, scope, duration)
  eviction follows the same pattern as `UpsertRecommendations` — safe
  under concurrent runs because the eviction window is the
  per-partition `collected_at` watermark.

**Test plan:**

Unit tests in `pkg/exchange/`:

- `TestAnalyzeReshapingWithOfferings_GenericLookup_NoFamilyAllowlist`
  — given a fake `ListOfferings` that returns offerings for a brand-new
  family (`m99`), assert reshape surfaces it as an alternative when
  the units check passes. Pins that the family-allowlist deletion
  doesn't accidentally re-introduce a hardcoded list elsewhere.
- `TestAnalyzeReshapingWithOfferings_EmptyCache_FallsBack` — assert
  whatever cold-start behaviour we pick (fallback or inline-populate)
  is exercised.
- Migrate the existing `TestAnalyzeReshapingWithOfferings_*` family
  tests to use the new lookup signature. Behaviour assertions stay
  identical.

Integration tests in `internal/config/store_postgres_offerings_test.go`
(new, `//go:build integration`):

- `TestPostgresStore_UpsertOfferings_RoundTrip` — write a small set,
  list with each filter combination, assert correct rows.
- `TestPostgresStore_UpsertOfferings_PartitionEviction` — seed (us-east-1,
  1y, Region) + (us-east-1, 3y, Region); refresh only the 1y partition;
  assert 3y rows survive (parallel to the account-scoped eviction
  pattern from commit `e7d6e5b66`).
- `TestPostgresStore_ListOfferings_FilterPushdown` — exercise each
  field of `OfferingFilter`.

Scheduler tests in `internal/scheduler/scheduler_test.go`:

- `TestCollectConvertibleOfferings_SuccessfulRegions` — mock per-region
  pagers, assert the right tuples land in the cache.
- `TestCollectConvertibleOfferings_PartialRegionFailure` — one region's
  pager returns an error; assert the other regions' rows land and the
  failed region's previous-cycle rows survive (consistent with the
  account-scoped eviction guarantees).

**Verification:**

```bash
(cd "$CUDLY" && go build ./... && go test -short -count=1 -race ./pkg/exchange/... ./internal/config/... ./internal/scheduler/...)
(cd "$CUDLY" && go test -tags=integration -count=1 ./internal/config/...)
```

Post-deploy verification:

- After the migration ships, `\d convertible_offerings_cache` shows the
  PK + region/duration index.
- After the populator ships, `SELECT region, duration_seconds, scope, COUNT(*)
  FROM convertible_offerings_cache GROUP BY 1,2,3` shows non-zero rows
  per (region, term, scope) within ~15 min of deploy.
- Hit the reshape page for an instance type AWS launched after the
  legacy-family allowlist was last hand-curated; verify alternatives
  appear without a code change.

**Effort:** `medium-large` — three-commit sequence (migration first;
then store + populator; then reshape refactor). Six mock surfaces to
touch, but the underlying dollar-units logic + plumbing types from
commit `cd440d9ea` are reused as-is. Estimate 3-5 days end-to-end
with the migration applied to staging first.

**Cross-references:**

- Commit `cd440d9ea` (`feat(exchange): cross-family RI alternatives for
  specialty + legacy`) — original hardcoded fix this generic shape
  replaces.
- `passesDollarUnitsCheck` — the runtime gate that survives unchanged
  through this refactor.
- `internal/scheduler/scheduler.go::accountOutcome.SucceededAccountIDs`
  - `internal/config/store_postgres_recommendations.go::SuccessfulCollect`
  — same partition-scoped eviction pattern this cache reuses for
  per-(region, term) refresh.
- Top-level `known-issues.md` line ~149 — the original cross-family
  follow-up that `cd440d9ea` resolved (and that this file extends).
