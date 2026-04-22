# Known Issues: Exchange / Cross-family alternatives source

> **Audit status (2026-04-22):** `1 needs triage · 0 resolved (new file)`

Surfaced during the cross-family RI follow-up (`cd440d9ea`,
`feat(exchange): cross-family RI alternatives for specialty + legacy`).
That commit unblocked specialty (`p*/g*/hpc*`) and legacy (`m4/c4/r3`)
families by extending the `peerFamilyGroups` allowlist + a local
dollar-units pre-filter, and falls back to a per-recommendation
`DescribeReservedInstancesOfferings` API call to enrich pricing on the
candidate set. Both the static allowlist and the per-rec offering
enumeration are unnecessary — CUDly already has the data needed to
produce cross-family suggestions without any of it.

## MEDIUM: Replace `peerFamilyGroups` + offering enumeration with the existing RI purchase-recommendation cache

**File**: `pkg/exchange/reshape.go::peerFamilyGroups` +
`pkg/exchange/reshape.go::candidateFamilies` +
`pkg/exchange/reshape.go::AnalyzeReshapingWithOfferings` +
`providers/aws/services/ec2/client.go::FindConvertibleOfferings`

**Description**: Today's reshape recommendation pipeline:

1. Take an underutilised convertible RI (input from
   `ListConvertibleReservedInstances` via `DescribeReservedInstances`).
2. Pick candidate target families from the hardcoded `peerFamilyGroups`
   allowlist.
3. Call `FindConvertibleOfferings` (a `DescribeReservedInstancesOfferings`
   API call) to enrich the candidates with pricing.
4. Filter via `passesDollarUnitsCheck` and surface the survivors.

Steps 2 and 3 are unnecessary because CUDly's scheduler already
collects AWS's *own* RI purchase recommendations for the workload via
the Cost Explorer `GetReservationPurchaseRecommendation` API
(`providers/aws/recommendations/client.go`) and persists them in the
`recommendations` table as `RecommendationRecord` rows. Those
recommendations are AWS's official advice on what to buy for the
observed usage — strictly more relevant than enumerating every
offering in the family. The reshape page should pair underutilised
convertible RIs against AWS's purchase recommendations, gated by
`passesDollarUnitsCheck`. No allowlist, no SKU catalogue, no per-rec
offering API call needed.

**Why this is the right shape**:

- AWS itself decides what's worth recommending for the workload — we
  inherit that judgement instead of approximating it with a static
  family list.
- The data already lives in Postgres (`recommendations` table); the
  reshape page becomes a JOIN between underutilised convertibles and
  the cached recommendations.
- New EC2 families surface automatically as soon as Cost Explorer
  starts recommending them — no code change needed.
- Cross-family is implicit: Cost Explorer recommendations span families
  whenever the recommended target differs from what the user owns.
- Per-rec API fan-out goes to zero. The dashboard's reshape page
  becomes one SQL read instead of N×M offering enumeration calls.

**Why the previous "offering catalogue cache" plan was wrong**:

Previous draft of this file proposed populating a separate
`convertible_offerings_cache` table from
`DescribeReservedInstancesOfferings`. That re-implements what Cost
Explorer's recommendation engine already does, at significant cost
(new migration, new store, new scheduler hook, new failure surfaces)
and with strictly less signal — the offering catalogue lists every
shape AWS sells, not the ones AWS thinks fit your workload.

**Impact (today)**:

- New EC2 families are invisible until someone updates
  `peerFamilyGroups`.
- Reshape page fans out a `FindConvertibleOfferings` call per
  recommendation. Each call hits AWS's EC2 API for offering
  enumeration even though the same recommendation data is already in
  the cache.
- Specialty/legacy alternatives surface only because of a hand-curated
  group list; whenever AWS launches a new generation, that hand-
  curation has to repeat.

**Status**: not yet triaged.

### Implementation plan

**Goal**: rewire `AnalyzeReshapingWithOfferings` (and its callers in
`internal/api/handler_ri_exchange.go` /
`internal/server/handler_ri_exchange.go`) to source candidates from the
already-populated `recommendations` table filtered to
`(provider=aws, service=ec2)`. Apply `passesDollarUnitsCheck` against
each candidate using fields the recommendation already carries. Delete
the static family allowlist and the per-rec offering enumeration.

**Files to modify**:

- `pkg/exchange/reshape.go`:
  - **Delete** `peerFamilyGroups` map + `candidateFamilies` lookup +
    `alternativesForTarget(family, targetSize)` helper.
  - **Replace** `OfferingLookup func(ctx, []string) ([]OfferingOption, error)`
    with a recommendation-driven lookup:

    ```go
    // PurchaseRecLookup returns AWS RI purchase recommendations
    // applicable to the source RI's region (and optionally service /
    // currency). Implementations read from the cached `recommendations`
    // table — no live AWS API call. Each returned OfferingOption maps
    // to one RecommendationRecord: ResourceType → InstanceType,
    // computed effective monthly cost = (UpfrontCost / term_months)
    // + MonthlyCost, NormalizationFactor derived from the size table.
    type PurchaseRecLookup func(ctx context.Context, region, currencyCode string) ([]OfferingOption, error)
    ```

  - `AnalyzeReshapingWithOfferings` becomes
    `AnalyzeReshapingWithRecs(ctx, ris, util, threshold, lookup)`. For
    each underutilised RI: call lookup once per distinct (region,
    currency) tuple, filter results via the existing
    `passesDollarUnitsCheck` (unchanged), populate
    `AlternativeTargets`. The source RI's primary `TargetInstanceType`
    is unchanged — the existing same-family rightsizing logic doesn't
    depend on the lookup.
  - The base `AnalyzeReshaping` (no lookup) returns recommendations
    with `AlternativeTargets = nil`. Callers that don't supply a
    lookup (e.g. `auto.go`) keep today's behaviour: the auto-exchange
    pipeline acts on `TargetInstanceType` only.

- New `internal/api/exchange_lookup.go` (or extend the existing
  handler file): a `purchaseRecLookupFromStore(store config.StoreInterface) exchange.PurchaseRecLookup` adapter that:
  - Calls `store.ListStoredRecommendations(ctx, RecommendationFilter{Provider: "aws", Service: "ec2", Region: region})`.
  - Maps each `RecommendationRecord` → `exchange.OfferingOption`:

    ```go
    // termMonths derives from RecommendationRecord.Term (years).
    // 1y → 12, 3y → 36. Cost Explorer recommendations use whole-year
    // terms; reject anything outside {1, 3} as a defensive guard.
    termMonths := rec.Term * 12
    effectiveMonthly := (rec.UpfrontCost / float64(termMonths)) + rec.MonthlyCost
    nf := exchange.NormalizationFactorForSize(sizeOf(rec.ResourceType))
    return exchange.OfferingOption{
        InstanceType:         rec.ResourceType,
        OfferingID:           rec.ID, // reuse the rec ID as a stable handle
        EffectiveMonthlyCost: effectiveMonthly,
        NormalizationFactor:  nf,
        CurrencyCode:         "USD", // Cost Explorer recs are USD-only today
    }
    ```

  - Filters out recommendations whose currency or region doesn't match
    the source RI (a no-op today since both sides are USD per region,
    but pinned for forward-compat).

- `internal/api/handler_ri_exchange.go::convertToExchangeTypes` and
  `internal/server/handler_ri_exchange.go::convertForAutoExchange`:
  thread the new `purchaseRecLookupFromStore(h.store)` into
  `AnalyzeReshapingWithRecs` instead of the live
  `FindConvertibleOfferings` closure.

- `providers/aws/services/ec2/client.go::FindConvertibleOfferings`:
  becomes dead code on the reshape path. Two choices:
  - **Delete it** entirely if no other caller exists. Pre-flight grep
    for `FindConvertibleOfferings` confirms.
  - **Keep it** for one-off use (e.g. an admin debug endpoint) but
    drop it from the reshape pipeline.

  Recommended: delete unless grep shows another consumer.

**No new schema, no new store API, no scheduler hook needed.**
The `recommendations` table already exists, is populated by the
scheduler tick, and supports the filter we need.

**Steps**:

1. Single commit: delete `peerFamilyGroups` / `candidateFamilies` /
   `alternativesForTarget`, switch the lookup signature, add the
   store-backed adapter, update tests, delete
   `FindConvertibleOfferings` call from the handler hot path. Pure
   refactor with no production migration needed.

   Optional follow-up (separate commit) if `FindConvertibleOfferings`
   has no other callers: delete it from the AWS provider client.

**Edge cases**:

- **Cache empty (cold start, before first scheduler tick).**
  `ListStoredRecommendations` returns nothing → reshape page shows
  primary target only, no alternatives. Same UX as a region with no
  AWS-recommended buys today. Acceptable: the scheduler tick is
  ~15 min and the freshness banner already surfaces "no
  recommendations yet" state.
- **Source RI's region has no AWS purchase recommendations.** AWS
  hasn't recommended anything for the workload — the dashboard
  showing zero alternatives is correct: there's nothing to buy that
  AWS thinks fits.
- **Source RI is in a region/currency the recs don't cover.** Filter
  on the source side; alternatives are an empty slice. Same UX as
  cache-empty.
- **Term mismatch.** A 3y convertible RI vs a 1y purchase
  recommendation: `passesDollarUnitsCheck` will mostly reject
  mismatched terms because the per-month amortisation differs by 3×.
  No special-casing needed; the units check is the gate.
- **Currency mismatch.** The adapter filters by currency at the SQL
  layer; the dollar-units check guards against a mismatched-pair
  comparison even if the filter slips. Same belt-and-braces as today.
- **Stale recommendations.** Cost Explorer recommendations are
  refreshed on the scheduler tick. Stale data could surface a target
  that's no longer in AWS's recommendation set, but the
  `auto.go::IsValidExchange=false` skip path catches any actual
  exchange failure at execution time — same guarantee that ships
  today for the offerings-based path.
- **Recommendations from a different account in the same region.**
  Already namespaced in the recs table by `cloud_account_id`. The
  reshape page is per-account so the filter naturally scopes — but
  the adapter must include the source account ID in the
  `RecommendationFilter` to avoid leaking cross-account suggestions.
  Verify by grepping `RecommendationFilter` callers in the handler
  layer.
- **`auto.go` impact when `peerFamilyGroups` is deleted.** auto.go
  currently calls the base `AnalyzeReshaping` (no lookup). With
  `peerFamilyGroups` gone, base returns `AlternativeTargets=nil`. The
  pre-flight grep needs to confirm auto.go acts on `TargetInstanceType`
  only — if it ever consumed alternatives, this would be a
  behaviour change. Quick check:
  `grep -rn "AlternativeTargets" internal/ pkg/`.

**Test plan**:

Unit tests in `pkg/exchange/`:

- `TestAnalyzeReshapingWithRecs_RecommendationDrivenAlternatives` —
  given a fake `PurchaseRecLookup` returning recommendations spanning
  multiple families (m5, c5, r5 in one region), assert reshape
  surfaces the cross-family ones that pass the units check.
- `TestAnalyzeReshapingWithRecs_EmptyLookupReturnsNoAlternatives` —
  pinned cold-start UX: no alternatives, primary target unchanged.
- `TestAnalyzeReshapingWithRecs_AppliesDollarUnitsFilter` — lookup
  returns one rec whose units fail the check and one that passes;
  assert only the passing one appears.
- Migrate the existing `TestAnalyzeReshapingWithOfferings_*` family
  tests to use the new lookup signature. Behaviour assertions stay
  identical (same rule, different source of candidates).
- Delete `TestCandidateFamilies_*` tests entirely — the function is
  gone.

Handler tests in `internal/api/handler_ri_exchange_test.go` and
`internal/server/handler_ri_exchange_test.go`:

- `TestPurchaseRecLookupFromStore_RegionFilter` — seed two regions
  worth of recs in the mock store; assert the adapter only returns
  rows for the requested region.
- `TestPurchaseRecLookupFromStore_AccountFilter` — multi-account
  setup; assert the adapter scopes to the source account ID.
- `TestPurchaseRecLookupFromStore_NoRecsReturnsEmpty` — empty store;
  no error, empty slice.

**Verification**:

```bash
(cd "$CUDLY" && go build ./... && go test -short -count=1 -race ./pkg/exchange/... ./internal/api/... ./internal/server/...)
```

Post-deploy verification:

- Hit the reshape page for an instance type AWS launched after the
  legacy-family allowlist was last hand-curated; verify alternatives
  appear without a code change.
- For an underutilised convertible RI in a region where Cost Explorer
  is recommending a different family, verify the alternative shows up
  in the dashboard.
- Tail logs during a reshape page load — no
  `DescribeReservedInstancesOfferings` calls should fire.

**Effort**: `small` — single refactor commit, no schema changes, no
new background jobs, no new failure surfaces. Estimate 0.5-1 day
end-to-end including test updates.

**Cross-references**:

- Commit `cd440d9ea` (`feat(exchange): cross-family RI alternatives
  for specialty + legacy`) — original hardcoded fix this re-scoping
  replaces.
- `passesDollarUnitsCheck` (introduced in `cd440d9ea`) — runtime gate
  that survives unchanged through this refactor; the only thing that
  changes is where candidates come from.
- `providers/aws/recommendations/client.go::GetReservationRecommendations`
  — the Cost Explorer call that already populates the
  `recommendations` table; no change needed, only consumed.
- `internal/config/store_postgres_recommendations.go::ListStoredRecommendations`
  — the read path the adapter calls.
- Top-level `known-issues.md` line ~149 — the original cross-family
  follow-up that `cd440d9ea` resolved.
