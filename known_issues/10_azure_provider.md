# Known Issues: Azure Provider

> **Audit status (2026-04-20):** `3 still valid · 5 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## CRITICAL: Recommendation converters ignore the API response entirely

**File**: `providers/azure/services/compute/client.go:654-668`, `database/client.go:575-`, `cache/client.go:570-`, `cosmosdb/client.go:589-`
**Description**: All four `convert*Recommendation` functions receive an `armconsumption.ReservationRecommendationClassification` argument but never inspect it. Each returns a stub with only `Provider`, `Service`, `Account`, `Region` filled in; `ResourceType`, `Count`, `CommitmentCost`, `Term`, and `PaymentOption` are constants or zero.
**Impact**: Every `GetRecommendations` call returns useless stub objects. Any purchase flow driven from these will attempt to buy empty resource types with quantity 0.
**Status:** ✅ Still valid

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

## HIGH: Azure Retail Prices API pagination never followed

**File**: `providers/azure/services/compute/client.go:602-632` (and the parallel `fetchAzurePricing` in `database/client.go`, `cache/client.go`, `cosmosdb/client.go`)
**Description**: `fetchAzurePricing` issues one GET to `https://prices.azure.com/api/retail/prices?...`, decodes the response into `AzureRetailPrice`, and returns it. `NextPageLink` exists on the struct but is never inspected or followed. The Azure Retail Prices API paginates at 100 items per page.
**Impact**: For common SKUs with many pricing rows, the specific entry needed for a size/term/region combo can sit on page 2+, causing `"no on-demand pricing found"` errors or wrong price estimates.
**Status:** ✅ Still valid

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

## HIGH: Recommendations fetched once per region (60+) for a subscription-scoped API

**File**: `providers/azure/recommendations.go:26-75`
**Description**: `GetRecommendations` iterates every Azure region and calls each service's `GetRecommendations` once per region. The underlying `armconsumption` API is subscription-scoped — the full result set is returned regardless of region, so ~60 regions × 3 services = 180 API calls that all produce identical results.
**Impact**: 60x API call multiplication; duplicate recommendations in the output; rate-limit risk.
**Status:** ✅ Still valid

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
