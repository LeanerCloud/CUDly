# Known Issues: Azure Provider

> **Audit status (2026-04-25):** `2 follow-ups from CRITICAL rewrite · 13 resolved · 0 partially fixed · 0 moved · 1 narrower follow-up surfaced (compute Details fields require pkg/common type extension)`

## ~~CRITICAL: Recommendation converters ignore the API response entirely~~ — RESOLVED

**File**: `providers/azure/services/compute/client.go::convertAzureVMRecommendation`, `database/client.go::convertAzureSQLRecommendation`, `cache/client.go::convertAzureRedisRecommendation`, `cosmosdb/client.go::convertAzureCosmosRecommendation`
**Description**: All four `convert*Recommendation` functions received an `armconsumption.ReservationRecommendationClassification` argument but never inspected it. Each returned a stub with only `Provider`, `Service`, `Account`, `Region` (from the client, not the response) filled in; `ResourceType`, `Count`, `OnDemandCost`, `CommitmentCost`, `EstimatedSavings` were zero and `Term` was hardcoded to "1yr".
**Impact**: Every `GetRecommendations` call returned useless stub objects. Purchase flows driven from these would attempt to buy empty resource types with quantity 0.
**Status:** ✔️ Resolved

**Resolved by:** New shared helper `providers/azure/internal/recommendations/converter.go::Extract` owns the type ladder and every nil-pointer guard. Handles BOTH `*LegacyReservationRecommendation` (EA / older MCA subscriptions) and `*ModernReservationRecommendation` (newer MCA billing accounts) — Azure picks the shape based on the subscription's billing account type, so supporting both is mandatory (MCA customers would otherwise see zero Azure recs). Each of the four service converters now calls `recommendations.Extract(azureRec)` and returns nil when the helper returns nil (unusable payload — caller filters it out) or builds a fully-populated `common.Recommendation` when extraction succeeds. Unknown future kinds log a `Warnf` and return nil — they're filtered without breaking the pipeline.

Fields populated:

- `Region` — outer `LegacyReservationRecommendation.Location` (subscription-scope API, so was `""` since commit `f05376acc` removed the region loop; now reads from the response as intended).
- `ResourceType` — `NormalizedSize` first; falls back to `SKUProperties[name==SKUName].Value`; last resort is the first non-empty property value.
- `Count` — `int(*RecommendedQuantity)` (Go truncates float→int toward zero, matching round-down for non-negative qty).
- `OnDemandCost` — `CostWithNoReservedInstances`.
- `CommitmentCost` — `TotalCostWithReservedInstances`.
- `EstimatedSavings` — `NetSavings` pass-through (existing downstream consumers treat this field as monthly; the Advisor path's annualSavingsAmount/12 convention from commit `2c0bb9102` does NOT apply here because the reservation API returns lookback-period savings).
- `Term` — `"P1Y"`→`"1yr"`, `"P3Y"`→`"3yr"`, missing→`"1yr"` default (matches the stub's invariant so downstream code still sees a non-empty term), unknown→pass through + `logging.Warnf`.

`Details` intentionally left nil — none of the previous stubs populated it; per-service `Details` construction is tracked as a deliberate follow-up (see "MEDIUM: Azure converters leave `common.Recommendation.Details` nil" below).

Existing stub-pinning tests in all four services (`TestComputeClient_ConvertAzureVMRecommendation` and analogues) passed nil and asserted a non-nil stub return — these are now replaced with `_NilGuards` tests (assert nil → nil) plus `_PopulatesAllFields` tests built from `mocks.BuildLegacyReservationRecommendation(...)` fixtures. `recommendations.Extract` has its own table tests covering nil/wrong-type/nil-props input, term normalisation, float→int truncation, and the ResourceType fallback ladder.

Risk audit: grepped `internal/scheduler/`, `internal/api/`, `internal/purchase/` for `.Count == 0`, `.ResourceType == ""`, `.CommitmentCost == 0` patterns — found none, so no downstream code was relying on the stub's zero-valued fields.

`cd providers/azure && go test -short ./...` passes across the whole tree.

### Implementation plan

**Goal:** Make the converters actually read the Azure response.

**Files to modify:**

- `providers/azure/services/compute/client.go:654-668`
- `providers/azure/services/database/client.go:575-`
- `providers/azure/services/cache/client.go:570-`
- `providers/azure/services/cosmosdb/client.go:589-`
- corresponding `*_test.go` files

**Steps:**

1. In each converter, type-assert `azureRec` to `*armconsumption.SharedReservationRecommendationProperties` (and handle `SingleReservationRecommendationProperties` for single-scope results if that path is exercised).
2. Extract `InstanceFlexibilityGroup`/`SKUProperties.Name` for `ResourceType`, `RecommendedQuantity` for `Count`, `NetSavings`/`CostWithNoReservedInstances` for cost fields, `Term` for `Term`, and the `Location` for `Region`.
3. Handle nil pointers defensively — Azure SDK returns pointer-heavy structs. Skip the rec or default on nil.
4. Update the unit tests to fixture realistic Azure responses (the mocks/azure_mocks.go already type the payload correctly).
5. Add a nil-azureRec test per converter to lock down the defensive behaviour.

**Edge cases the fix must handle:**

- Azure returns both Single- and Shared-scope results; both classifications must be handled.
- Nil `Properties` — return nil so the caller can skip the rec.
- `Term` string format ("P1Y"/"P3Y") — convert to the internal `"1yr"/"3yr"` format used elsewhere.

**Test plan:**

- New fixture-based test per converter asserting `ResourceType`, `Count`, `CommitmentCost`.
- Existing nil-argument tests continue to pass and now assert nil-return.

**Verification:**

- `go test ./providers/azure/...`

**Related issues:** `10_azure_provider#LOW-extractRegionFromResourceID` — Advisor path is a separate but related stub.

**Effort:** `large`

## ~~CRITICAL: Non-UUID reservation order IDs in database and cache purchase paths~~ — RESOLVED

**File**: `providers/azure/services/database/client.go:245`, `cache/client.go:243`
**Description**: Both clients previously built reservation order IDs with `fmt.Sprintf("sql-reservation-%d", time.Now().Unix())` / `"redis-reservation-%d"`. The Azure Reservations API requires a GUID — every purchase returned HTTP 400 `InvalidReservationOrderId`.
**Impact**: All SQL and Redis reservation purchases previously failed.
**Status:** ✔️ Resolved

**Resolved by:** `635a2a2a4` — both clients now use `reservationOrderID := uuid.New().String()`, matching the compute client pattern.

## ~~HIGH: Azure Retail Prices API pagination never followed~~ — RESOLVED

**File**: `providers/azure/services/compute/client.go:602-632` (and the parallel `fetchAzurePricing` in `database/client.go`, `cache/client.go`, `cosmosdb/client.go`)
**Description**: `fetchAzurePricing` issued a single GET to the Retail Prices API and decoded one page. `NextPageLink` (present on the struct in three of the four services already; added to compute as part of this fix) was never followed. The API paginates at 100 items per page, so any SKU/term/region combo that landed on page 2+ produced "no on-demand pricing found" or a wrong estimate.
**Status:** ✔️ Resolved

**Resolved by:** Each of the four `fetchAzurePricing` methods (compute, database, cache, cosmosdb) now walks `NextPageLink` until it's empty. Each implements the same loop in place rather than extracting a shared helper — the four `AzureRetailPrice` types have different per-service `Items` shapes and unifying them would have been a larger refactor than the bug warranted. The loop has three guards:

- Safety cap (`retailPricesMaxPages = 50`, ≈5000 items) defends against a server bug returning a `NextPageLink` that never empties.
- A `seen` map of URLs detects self-referential `NextPageLink` and aborts with a clear error instead of looping forever.
- HTTP errors and non-200 responses include the page index so logs pinpoint where pagination failed (rather than masking the failure as "first page failed").

The compute `AzureRetailPrice` type was extended with the `NextPageLink string \`json:"NextPageLink"\`` field that the other three services already had.

Tests added (`providers/azure/services/compute/client_test.go`):

- `TestFetchAzurePricing_FollowsNextPageLink` — mock HTTP returns two pages; assert items from both are merged in order, and exactly two HTTP calls happen.
- `TestFetchAzurePricing_RejectsSelfReferentialNextPageLink` — mock returns the initial URL as its own `NextPageLink`; assert the error message names the failure mode rather than looping forever.

The other three services share the same code shape; covering compute is sufficient to pin the contract. `go test -short ./providers/azure/...` passes for the whole tree.

### Implementation plan

**Goal:** Follow `NextPageLink` until empty, across all four service clients.

**Files to modify:**

- `providers/azure/services/compute/client.go:602-632`
- `providers/azure/services/database/client.go` (equivalent `fetchAzurePricing`)
- `providers/azure/services/cache/client.go`
- `providers/azure/services/cosmosdb/client.go`
- corresponding tests

**Steps:**

1. Extract a shared pagination helper (e.g. into `providers/azure/internal/pricing/retail_prices.go`) rather than duplicating the loop four times — see **Related issues**.
2. In the helper: loop while `priceData.NextPageLink != ""`, issue the next GET, append `Items`, guard against infinite loops with a page cap (say 50).
3. Set a sensible per-request timeout (not the whole pagination) to avoid a single slow page stalling all callers.
4. Update each `fetchAzurePricing` to call the shared helper.

**Edge cases the fix must handle:**

- Pagination pointing back to the same URL (shouldn't happen but could loop) — break on repeat.
- Server returning a non-200 mid-pagination — return the partial error, don't silently accept partial data.
- Very large SKU catalogues — cap pages at 50 and log a warning if hit.

**Test plan:**

- `TestFetchAzurePricing_FollowsNextPageLink` — seed an HTTP test server returning two pages and asserting both are loaded.
- `TestFetchAzurePricing_ErrorMidPagination` — asserts the error is returned.

**Verification:**

- `go test ./providers/azure/...`

**Related issues:** This overlaps with the duplicated logic across the four service clients — combining the fix with an extract-to-helper refactor is sensible.

**Effort:** `medium`

## ~~HIGH: Pagination errors in commitment collection silently swallowed~~ — RESOLVED

**File**: `providers/azure/services/compute/client.go:190-208`, `database/client.go:~185`, `cache/client.go:~183`, `cosmosdb/client.go:~185`, `search/client.go:~183`
**Description**: `collect*Reservations` helpers broke out of the pagination loop on the first error and returned a partial list with nil error, so transient API failures silently truncated commitment data and could trigger duplicate-purchase bugs.
**Status:** ✔️ Resolved

**Resolved by:** Changed all five helpers (compute, database, cache, cosmosdb, and the previously-overlooked search client that had the same shape) to return `([]Commitment, error)` and wrap `pager.NextPage` errors with a service-prefixed context. `GetExistingCommitments` in each service now propagates that error to its caller. Existing `_PagerError` tests updated to assert the error is returned (with matching prefix) and `commitments` is nil; existing `_Empty` tests migrated from "no credentials → silent swallow" to "mock pager with zero pages" so they cover the genuine empty-subscription case without relying on the bug.

### Original implementation plan

**Goal:** Return errors from `collect*Reservations` so the caller sees the failure.

**Files to modify:**

- `providers/azure/services/compute/client.go:190-208`
- `providers/azure/services/database/client.go` (`collectSQLReservations`)
- `providers/azure/services/cache/client.go` (`collectRedisReservations`)
- `providers/azure/services/cosmosdb/client.go` (`collectCosmosReservations`)
- callers of each
- tests for each helper

**Steps:**

1. Change each helper's signature to return `([]common.Commitment, error)`.
2. On page error, return the error (wrapped with service name) and the commitments collected so far — or return nil and let the caller decide. Recommended: return error + nil commitments so callers can't accidentally use partial data.
3. Update callers: at the call site, decide whether to treat the error as fatal (most cases) or to log+continue (explicit opt-in for "best effort" reads, if any such caller exists).

**Edge cases the fix must handle:**

- Authentication errors vs transient 5xx — don't distinguish; just fail. Callers can retry.
- Empty subscription (no reservations) — `pager.More()` returns false; still nil error.

**Test plan:**

- `TestCollectVMReservations_PaginationError` — asserts error is returned, no commitments.
- Parallel tests for the other three services.

**Verification:**

- `go test ./providers/azure/...`

**Effort:** `medium`

## ~~HIGH: Recommendations fetched once per region (60+) for a subscription-scoped API~~ — RESOLVED

**File**: `providers/azure/recommendations.go:26-75`
**Description**: `GetRecommendations` iterated every Azure region and called each service's `GetRecommendations` once per region. The underlying `armconsumption` API is subscription-scoped — the full result set is returned regardless of region, so ~60 regions × 3 services = 180 API calls that all produced identical results.
**Status:** ✔️ Resolved

**Resolved by:** Removed the outer region loop in `GetRecommendations`. Each service client (compute, database, cache) is now called exactly once with an empty region (`""`) — the API returns subscription-wide results in a single call. Errors per service are logged and skipped rather than aborting the whole call (preserves the previous best-effort semantics).

The `getRegions` helper became unused and was removed; the `AzureProvider.GetRegions` path it depended on is still available for callers that genuinely need region listings (UI dropdowns, etc.).

The per-recommendation `Region` field is now whatever the per-service converter sets — currently `c.region`, which is `""` after this change. Properly populating `Region` from the response payload is the converter work tracked in the `CRITICAL: Recommendation converters ignore the API response entirely` item below; this change is the prerequisite that makes that work meaningful (otherwise the converter would have to overwrite a wrong region 60 times).

### Implementation plan

**Goal:** Call each service once with subscription scope, then filter by region.

**Files to modify:**

- `providers/azure/recommendations.go:26-75`
- `providers/azure/services/compute/client.go` (confirm the API is subscription-scoped)
- `providers/azure/services/database/client.go`
- `providers/azure/services/cache/client.go`
- `providers/azure/recommendations_test.go`

**Steps:**

1. Remove the outer `for _, region := range regions` loop in `GetRecommendations`.
2. Call each service's `GetRecommendations` once, without a region scope (pass empty string if the signature requires it; otherwise update the signature).
3. Update each service's `GetRecommendations` to populate the region from the response payload, not the client's stored `c.region`.
4. If filtering by region is still needed for the UI, do it post-fetch via a simple slice filter.

**Edge cases the fix must handle:**

- Subscriptions with no recommendations — return empty slice.
- Responses where the per-item region is missing — fall back to the Advisor region extraction (see LOW issue below) or leave blank.

**Test plan:**

- `TestGetRecommendations_SingleCallPerService` — mock the API to return two recommendations with different regions; assert exactly one call per service and both recs appear.

**Verification:**

- `go test ./providers/azure/...`

**Effort:** `medium`

## ~~HIGH: Data race on `AzureProvider.cred` via `IsConfigured` side-effect~~ — RESOLVED

**File**: `providers/azure/provider.go:136-158`
**Description**: `IsConfigured()` previously wrote `p.cred = cred` with no mutex, concurrent with reads from `ValidateCredentials`/`GetServiceClient`/`GetRecommendationsClient`.
**Impact**: Data race; undefined behaviour under the Go memory model.
**Status:** ✔️ Resolved

**Resolved by:** `94d19a3b8` — `AzureProvider` now has `credOnce sync.Once` (line 90) and `credErr error` (line 91); `IsConfigured` runs credential loading at most once via `credOnce.Do` (line 143). `SetCredential` bypass for tests is explicit and documented.

## ~~MEDIUM: `GetRegions` discards the root cause error from `GetAccounts`~~ — RESOLVED

**File**: `providers/azure/provider.go:250-258`
**Description**: When `GetAccounts` returned an error, `GetRegions` previously replaced it with a generic "no Azure subscriptions found" message, losing the root cause.
**Status:** ✔️ Resolved

**Resolved by:** `94d19a3b8` — line 254 now wraps the error with `%w`: `return nil, fmt.Errorf("no Azure subscriptions found to query regions: %w", err)`.

## ~~LOW: `extractRegionFromResourceID` always returns empty string~~ — RESOLVED

**File**: `providers/azure/recommendations.go:252-258`
**Description**: The function was a stub that always returned `""`; every Advisor recommendation rendered with blank region.
**Status:** ✔️ Resolved

**Resolved by:** Two-layer fix. `convertAdvisorRecommendation` now prefers `Properties.ExtendedProperties["region"]` / `"location"` (the authoritative source when Azure provides it) before falling back to the parser. `extractRegionFromResourceID` is now a best-effort ARM-ID scanner looking for `/locations/{region}/` or `/location/{region}/` (case-insensitive), returning the next segment, or `""` when no region segment exists. Test table expanded with six new cases covering mid-id, uppercase, singular, trailing-no-slash, and non-ARM-shaped inputs.

### Original implementation plan

**Goal:** Parse the region from the Azure ARM resource ID when present.

**Files to modify:**

- `providers/azure/recommendations.go:252-258`
- `providers/azure/recommendations_test.go`

**Steps:**

1. Most Advisor recommendations carry `Properties.Location` or `Properties.ExtendedProperties["region"]` directly — check those first; they're authoritative.
2. As a fallback, try to parse the ARM resource ID: `/subscriptions/{sub}/resourceGroups/{rg}/providers/{namespace}/{type}/{name}` does not include region, but many ARM IDs for reservation-scope resources embed the location elsewhere in the properties. If the ID doesn't contain region info (the common case), return "".
3. Prefer fixing at the Advisor conversion path: in `convertAdvisorRecommendation` (line 130), read the Advisor properties first before calling this helper.

**Edge cases the fix must handle:**

- IDs without any region info → return "" (unchanged).
- Non-ARM-shaped strings → return "" safely without panic.

**Test plan:**

- `TestExtractRegionFromResourceID_WithLocationField` — asserts region extracted when present in sibling field.
- `TestExtractRegionFromResourceID_MissingRegion` — asserts "" return.

**Verification:**

- `go test ./providers/azure/...`

**Effort:** `small`

## ~~MEDIUM: Azure converters leave `common.Recommendation.Details` nil~~ — RESOLVED (all 4 services)

**File**: `providers/azure/services/{compute,database,cache,cosmosdb}/client.go::convertAzure*Recommendation`
**Description**: After the CRITICAL converter rewrite, the four Azure converters populated every `common.Recommendation` field except `Details` (the per-service polymorphic struct: `ComputeDetails`, `DatabaseDetails`, `CacheDetails`, etc.). UI/CSV/email consumers that wanted service-specific extras (VM family, DB engine/edition, Redis tier, Cosmos throughput) saw `Details == nil` for every Azure rec.
**Impact**: UI consistency between Azure and AWS recommendations was incomplete; some downstream features (e.g. richer email summaries, service-family-aware scoring) silently degraded for Azure.
**Status:** ✔️ Resolved

**Resolved for all four services:**

- `convertAzureVMRecommendation` now sets `Details: common.ComputeDetails{InstanceType: f.ResourceType}`. Platform/Tenancy/Scope are deferred — they require an `armcompute.ResourceSKUsClient.ListByLocation` lookup per SKU, which would produce an N+1 API-call storm on every `GetRecommendations`. Batched enrichment is filed as a new follow-up below.
- `convertAzureSQLRecommendation` now sets `Details: common.DatabaseDetails{Engine: "sqlserver", InstanceClass: f.ResourceType}` via a permissive `detailsFromSQLSKU(sku)` helper. EngineVersion/AZConfig/Deployment require an `armsql` SKU catalogue lookup and are deferred under the same batched-enrichment follow-up.
- `convertAzureRedisRecommendation` now sets `Details: common.CacheDetails{Engine: "redis", NodeType: f.ResourceType}`. Shards count requires an `armredis` catalogue lookup, deferred.

Each converter's godoc now explicitly documents which fields are payload-derivable vs. SDK-lookup territory so a future reader can tell the difference without guessing. Tests in each service's `client_test.go::Test*_ConvertAzure*Recommendation_PopulatesAllFields` now assert Details is non-nil, the concrete type matches, the payload-derived fields are populated, and the deferred fields are intentionally empty.

**Cosmos Details now populated too:** `pkg/common/types.go` gained a new `NoSQLDetails{Engine, APIType, ThroughputUnits}` type (GetServiceType returns `ServiceNoSQL`; GetDetailDescription formats as `"cosmos"` or `"cosmos/<apitype>"` when APIType is set). `convertAzureCosmosRecommendation` sets `Details: detailsFromCosmosSKU(f.ResourceType)` — a permissive parser that extracts the leading integer of the SKU string as `ThroughputUnits` and sets `Engine: "cosmos"`. APIType (sql / mongodb / cassandra / gremlin / table) requires an `armcosmos.DatabaseAccountsClient` lookup and is deferred under the same batched-enrichment follow-up as compute/database/cache fields. Six table-driven cases in `TestDetailsFromCosmosSKU` cover real-looking SKU formats (bare digits, `"1000RUperSecond"`, `"CosmosDB_RU_1000"`, empty).

### New follow-ups surfaced during this resolution

#### ~~MEDIUM: Azure converters leave SKU-lookup Details fields empty (deferred N+1 avoidance)~~ — RESOLVED for 3 of 4 services

**File:** `providers/azure/services/{database,cache,cosmosdb}/client.go::convertAzure*Recommendation`

**Status:** ✔️ Resolved for cache/cosmos/database; compute scoped back (see follow-up below)

**Resolution:** Each of the three database/cache/cosmosdb service clients gained a lazy SKU catalogue cache (`sync.Once`-gated `map[string]<svcSKUEntry>`). The catalogue is fetched ONCE per client lifetime via the existing per-service pager interface — `armredis.Client.NewListBySubscriptionPager` for cache, `armsql.CapabilitiesClient.ListByLocation` for database, `armcosmos.DatabaseAccountsClient.NewListPager` for cosmos. Subsequent converter calls in the same `GetRecommendations` run hit the in-memory map (O(1) lookup), eliminating the N+1 storm. A failed catalogue fetch leaves the cache map nil; converters fall back to the previous empty-Details behaviour with a one-time WARN log — the conversion itself never fails.

Fields now populated from the catalogue:

- **Cache:** `CacheDetails.Shards` from `armredis.Properties.ShardCount` (clustered Premium-tier caches; non-clustered caches keep Shards at zero, which signals "unknown").
- **Database:** `DatabaseDetails.EngineVersion` from the SQL Server version traversed in `armsql.LocationCapabilities.SupportedServerVersions` → `SupportedEditions` → `SupportedServiceLevelObjectives` → `SKU.Name`. AZConfig/Deployment require per-server lookups and remain deferred.
- **Cosmos:** `NoSQLDetails.APIType` (sql / mongodb / cassandra / gremlin / table) resolved by mapping `account.Kind` + `account.Properties.Capabilities` for the single dominant APIType across the subscription's Cosmos accounts. Multi-API-type subscriptions leave APIType empty rather than guessing.

Tests in each service's `client_test.go` cover three new contracts per service:

- `_PopulatesShardsFromSKUCache` / `_PopulatesAPIType` / `_PopulatesEngineVersion` — cache hit populates the previously-deferred field.
- `_PagerErrorFallsBack` / `_CapabilitiesErrorFallsBack` — catalogue-fetch error leaves the field empty + the conversion still succeeds.
- `_FetchedOnce` — many converter calls share a single catalogue fetch (the N+1 invariant).

#### MEDIUM: Azure compute Details vCPU/MemoryGB require ComputeDetails type extension

**File:** `providers/azure/services/compute/client.go::convertAzureVMRecommendation`, `pkg/common/types.go::ComputeDetails`

**Description:** The cache/cosmos/database batched-SKU-catalogue lookup intentionally skipped compute. Reason: `common.ComputeDetails` currently exposes only `InstanceType`, `Platform`, `Tenancy`, `Scope`. The SKU-catalogue enrichments compute would supply (vCPU + MemoryGB from `armcompute.ResourceSKU.Capabilities`) have no struct field to write into. Adding fields to a shared `pkg/common` type touches every cloud provider's converter, the frontend rendering layer, the matchers, and existing snapshot/golden tests across all 3 cloud providers — well outside the scope of a perf change to the Azure converters.

**Proposed fix:** (1) add `VCPU int` + `MemoryGB float64` fields to `common.ComputeDetails`; (2) update AWS + GCP converters to populate them where the SDK already exposes the data; (3) port the same lazy `cachedSKULookup` pattern from cache/cosmos/database into compute and wire the new fields through.

**Status:** ❓ Needs triage

### Original implementation plan

**Goal:** Each Azure service's converter wrapper builds the per-service `Details` struct from the SDK response.

**Files to modify:**

- `providers/azure/services/compute/client.go::convertAzureVMRecommendation` — populate `common.ComputeDetails{InstanceType, Family, vCPU, MemoryGB, ...}` from the `LegacyReservationRecommendationProperties.{NormalizedSize, SKUProperties}` plus an `armcompute.ResourceSKUsClient` lookup if needed for vCPU/memory.
- Same pattern for database (engine/edition from a SQL SKU lookup), cache (Redis tier from SKU name), cosmosdb (throughput unit from SKU).
- `providers/azure/internal/recommendations/converter.go` — keep service-specific Details extraction in the wrappers rather than pushing it into `ExtractedFields` (avoids cross-service coupling; the shared helper stays focused on the uniform fields).
- Test files for each service — add `_PopulatesDetails` cases using extended `mocks.BuildLegacyReservationRecommendation` options.

**Test plan:**

- Per service: a fixture with SKU-specific properties + an assertion that the resulting `Details` is non-nil and has the expected concrete type + fields.

**Verification:**

- `cd providers/azure && go test -short ./...`

**Related issues:** Part of the same audit thread as the CRITICAL converter rewrite above, closed without Details population by design to keep the blast radius bounded.

**Effort:** `medium` (each service is ~30 LOC + a fixture test).

## ~~LOW: Four parallel `AzureRetailPrice` types in azure service clients~~ — RESOLVED

**File**: `providers/azure/services/compute/client.go:124`, `database/client.go:120`, `cache/client.go:118`, `cosmosdb/client.go:121`
**Description**: Each service client defined its own `AzureRetailPrice` struct. Compute exported a typed `AzureRetailPriceItem`; the other three used inline anonymous structs with service-specific fields (`MeterName`, `ProductName`, `ServiceName`, `ArmSKUName`, etc.). After the pagination fix in commit `04b375f68`, the same `NextPageLink` walking loop (with seen-map, page cap, per-page error wrapping) lived in all four `fetchAzurePricing` methods — ~50 LOC of identical boilerplate per service.
**Impact**: Maintenance friction — any future schema change (API version bump, new response field, different auth) would have to be made in four places. Current runtime cost was low (the loop bodies were small, the schemas were stable), but the duplication would ossify as each copy drifted.
**Status:** ✔️ Resolved

**Resolved by:** new `providers/azure/internal/pricing/` package owns the shared walker. Key pieces:

- `pricing.Page[T any]` is the generic envelope (`Items []T`, `NextPageLink string`, `Count int`) — one decode target, one envelope shape, parameterised by the per-service item type.
- `pricing.FetchAll[T any](ctx, httpClient, initialURL, pageTimeout, maxPages)` walks the `NextPageLink` chain and returns the flat `[]T` slice. Enforces a per-page timeout (`DefaultPageTimeout = 10 * time.Second`), a max-pages cap (`DefaultMaxPages = 50`), and a seen-URL guard against self-referential `NextPageLink`. Returns scoped errors naming the page index + timeout duration so operators can distinguish cases in logs.
- `pricing.HTTPClient` is a minimal interface (single `Do(req)` method) declared in the shared package so it has no upstream dependency on per-service types.

Per-service changes:

- `compute/client.go` keeps its exported `AzureRetailPriceItem` named type and passes it as the `T` parameter. `fetchAzurePricing` is now a ~10-line wrapper: build the URL with `$filter` + `api-version`, call `pricing.FetchAll[AzureRetailPriceItem]`, wrap the returned slice in `&AzureRetailPrice{Items: items}`. The per-service `fetchOnePricingPage` helper is gone.
- `database/client.go` lifts its inline anonymous Items struct to a named `DatabaseRetailPriceItem` type. `extractSQLPricing`'s signature becomes `([]DatabaseRetailPriceItem, int)` — simpler and smaller.
- `cache/client.go` same lift → `CacheRetailPriceItem`; `extractRedisPricing` takes the named type.
- `cosmosdb/client.go` same lift → `CosmosRetailPriceItem`; `extractCosmosPricing` takes the named type.

Each service's `AzureRetailPrice` type is preserved as a thin envelope `{ Items []<ServiceItem> }` so the existing `priceData.Items` call sites and `TestAzureRetailPriceStructure` tests keep compiling — the `Count` and `NextPageLink` fields were removed from the envelope (they're never read outside the walker, which now lives in the shared package).

Test coverage:

- `providers/azure/internal/pricing/retail_prices_test.go` — six scenarios: multi-page merge, self-referential NextPageLink, max-pages cap, per-page timeout (verifies the request context carries a deadline and the outer ctx is NOT cancelled on per-page failure), non-OK status codes, zero-maxPages guard.
- `services/compute/client_test.go::TestFetchAzurePricing_WrapperSmokeTest` — one per-service smoke test proving the wrapper builds the URL correctly and re-wraps the returned slice in the service-local envelope. Database/cache/cosmos exercise the same shape through their existing extractor tests.

Before: ~50 LOC of duplicated pagination loop × 4 services = ~200 LOC. After: ~120 LOC in the shared package (including tests) + ~15 LOC wrapper × 4 services = ~180 LOC, no duplication.

### Original implementation plan

**Goal:** Single `providers/azure/internal/pricing/` package owning the shared retail-prices payload + pagination loop, with service-specific extractor helpers on top.

**Files to create/modify:**

- New `providers/azure/internal/pricing/retail_prices.go` with either (a) a generic `Page[T any]` walker using Go 1.18+ type parameters, or (b) a JSON-decoded `map[string]any` walker with per-service typed extractors. Prefer (a) for compile-time safety unless the per-service item shapes prove irreducibly different.
- Each `services/*/client.go` — replace the local `AzureRetailPrice` type and the per-service `fetchAzurePricing` body with a call into the shared package. Keep the service-specific filter-building at the callsite; the shared helper only owns the HTTP round trip, JSON decoding, and pagination loop.
- One test file for the shared package covering: two-page walk merges items correctly, self-referential `NextPageLink` aborts, page cap fires.

**Test plan:**

- Shared-package tests replace `providers/azure/services/compute/client_test.go::TestFetchAzurePricing_*` (those can stay as per-service smoke tests).

**Verification:**

- `cd providers/azure && go test -short ./...`

**Related issues:** Deferred from `10_azure_provider.md::HIGH: Azure Retail Prices API pagination never followed` — closed by `04b375f68` with per-service in-place pagination.

**Effort:** `medium` (4 files + 1 new package + tests).

## ~~MEDIUM: fetchAzurePricing has no per-page timeout~~ — RESOLVED

**File**: `providers/azure/services/{compute,database,cache,cosmosdb}/client.go::fetchAzurePricing`
**Description**: Commit `04b375f68`'s implementation plan promised "a sensible per-request timeout (not the whole pagination) to avoid a single slow page stalling all callers." The shipped code only respected the caller's `ctx` deadline via `http.NewRequestWithContext(ctx, ...)` — there was no per-page timeout. A 30-second caller context applied to a 50-page walk meant one slow page could consume the entire budget and starve the rest.
**Impact**: Pagination runs that should succeed within the caller's overall budget failed partway through when a single page was slow; the resulting error was attributed to the overall caller context rather than the specific slow page, making diagnosis harder.
**Status:** ✔️ Resolved

**Resolved by:** each of the four service clients (`compute`, `database`, `cache`, `cosmosdb`) now exposes a package-level `retailPricesPageTimeout = 10 * time.Second` constant and extracts the per-page GET into a dedicated `fetchOnePricingPage(ctx, pageURL, pageIdx)` method. The helper derives `pageCtx, cancel := context.WithTimeout(ctx, retailPricesPageTimeout)` and `defer cancel()` — the `defer` sits inside the helper (a function body), so each page's context is released at return time without the defer-in-loop leak footgun. The error message on the `c.httpClient.Do(req)` failure path now names the timeout (`"page %d, timeout %s"`) so operators can distinguish a per-page deadline from a network failure. Test coverage added as `TestFetchAzurePricing_PerPageTimeout` in `services/compute/client_test.go` — the other three services share the shape, so one representative test pins the contract (the shared-retail-prices-package refactor tracked in the `LOW` entry below will centralise the test further).

### Original implementation plan

**Goal:** Each GET in the pagination loop has an independent timeout (e.g. 10s), separate from the caller's overall context deadline.

**Files to modify:**

- `providers/azure/services/compute/client.go::fetchAzurePricing` (and the three parallel methods in database, cache, cosmosdb).

**Steps:**

1. Define `const pricingPageTimeout = 10 * time.Second` near the top of each service client (or, if doing the shared-helper refactor in the LOW entry above, in the shared package).
2. Inside the pagination loop, wrap each page with `pageCtx, cancel := context.WithTimeout(ctx, pricingPageTimeout); defer cancel()` (or equivalent idiom — `defer` inside a loop leaks across iterations, so call `cancel()` explicitly at end-of-iteration and before every `return` / `break`).
3. Pass `pageCtx` into `http.NewRequestWithContext(pageCtx, ...)`.
4. Adjust the per-page error message to mention the timeout (`"pricing API call timed out after %s on page %d: %w"`).

**Test plan:**

- `TestFetchAzurePricing_PerPageTimeout` — mock HTTP that sleeps ≥11s on page 2; assert the error surfaces with a recognisable timeout message and does NOT cascade to cancel the caller's outer context.

**Verification:**

- `cd providers/azure && go test -short ./services/compute/...` (other services share the shape — one service's test covers the contract).

**Effort:** `small` (4 identical edits + 1 test).

## ~~LOW: Extract Account field invariant not enforced on RecommendationsClientAdapter~~ — RESOLVED

**File**: `providers/azure/recommendations.go::RecommendationsClientAdapter`
**Description**: After commit `2d98002f8`, each per-service converter populates `Recommendation.Account = c.subscriptionID`. The client's `subscriptionID` was validated non-empty in `AzureProvider.GetRecommendationsClient` (`providers/azure/provider.go` fallback to `accounts[0].ID`), but `RecommendationsClientAdapter` itself had no invariant-check — a direct test or future refactor that constructed the adapter with an empty `subscriptionID` would silently produce recommendations with `Account == ""`, which downstream account-scoping would drop.
**Impact**: Defensive-coding gap. Not a runtime bug (the only production construction path validated), but a regression risk if the construction path changed.
**Status:** ✔️ Resolved

**Resolved by:** `providers/azure/recommendations.go` now exports `NewRecommendationsClientAdapter(cred, subscriptionID)` which rejects an empty `subscriptionID` with `"azure recommendations: subscriptionID is required"`. The package-level `NewRecommendationsClient` factory routes through it and now returns `(provider.RecommendationsClient, error)`; the existing production caller (`AzureProvider.GetRecommendationsClient`) propagates the error. The struct stays public because external tests reference its fields, and the invariant is documented in the godoc. Three test cases added in `services_test.go`: `TestNewRecommendationsClient_RejectsEmptySubscriptionID`, `TestNewRecommendationsClientAdapter_RejectsEmptySubscriptionID`, and the existing `TestNewRecommendationsClient` updated to use the new `(client, err)` signature.

### Original implementation plan

**Goal:** Make the invariant impossible to violate by refactor.

**Files to modify:**

- `providers/azure/recommendations.go::RecommendationsClientAdapter` struct + a constructor function that enforces non-empty subscriptionID.
- `providers/azure/provider.go::GetRecommendationsClient` — use the constructor.

**Steps:**

1. Add a `newRecommendationsClientAdapter(cred, subscriptionID)` function that returns `(*RecommendationsClientAdapter, error)` and rejects empty `subscriptionID` with a clear error.
2. Route all construction through the new helper (grep for `&RecommendationsClientAdapter{`).
3. Keep the struct public (external test files may reference it) but document the invariant in godoc.

**Test plan:**

- `TestNewRecommendationsClientAdapter_RejectsEmptySubscriptionID`.

**Effort:** `small`.
