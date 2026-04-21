# Known Issues: GCP Provider

> **Audit status (2026-04-20):** `0 from original audit · 8 resolved · 0 partially fixed · 0 moved · 2 new items surfaced during 2026-04-21 audit review`

## ~~CRITICAL: Missing Pagination in `getDefaultProject`~~ — RESOLVED

**File**: `providers/gcp/provider.go:414-435`
**Description**: `getDefaultProject` previously called `req.Do()` once, fetching only the first page of projects. In organisations with more than ~500 projects an active project on page 2+ was invisible and the function falsely reported "no active GCP projects found".
**Status:** ✔️ Resolved

**Resolved by:** `getDefaultProject` now uses `service.Projects.List().Pages(ctx, ...)`. The page callback returns the new sentinel `errStopProjectPagination` as soon as it finds the first ACTIVE project, so iteration short-circuits without fetching unnecessary pages. `errors.Is(err, errStopProjectPagination)` distinguishes the success-stop from real API errors, which propagate wrapped.

### Original implementation plan

**Goal:** `getDefaultProject` paginates across all pages of `projects.list` and returns the first active project, or delegates to the same paginated helper that `GetAccounts` uses.

**Files to modify:**

- `providers/gcp/provider.go:414-435` — replace the one-page call with `Pages(ctx, func(resp *cloudresourcemanager.ListProjectsResponse) error {...})`.
- `providers/gcp/provider_test.go` — new mock-based pagination test (see also issue `11_gcp_provider.md#LOW: Missing Test Coverage for getDefaultProject Pagination`).

**Steps:**

1. Refactor `getDefaultProject` to call `service.Projects.List().Pages(ctx, func(resp) error { for _, p := range resp.Projects { if p.LifecycleState == "ACTIVE" { foundID = p.ProjectId; return errStopPagination } } return nil })`.
2. Return the first active project immediately via a sentinel error to avoid fetching remaining pages.
3. Alternatively, extract the project-list logic into a single shared helper used by both `getDefaultProject` and `realResourceManagerService.ListProjects`.

**Edge cases the fix must handle:**

- Zero projects across all pages — return the existing error.
- Only inactive projects — same behaviour (not found).
- Transient API error mid-pagination — propagate with wrap.

**Test plan:**

- `TestGetDefaultProject_PaginatesToSecondPage` — inject a mock that returns 500 inactive projects on page 1 and 1 active on page 2; assert active ID returned.
- `TestGetDefaultProject_NoActiveProjects` — assert existing error.

**Verification:**

- `cd providers/gcp && go test ./...`

**Related issues:** `11_gcp_provider.md#LOW: Missing Test Coverage for getDefaultProject Pagination`

**Effort:** `small`

## ~~HIGH: Iterator Errors Silently Produce Partial Results in Three GetRecommendations~~ — RESOLVED

**File**: `providers/gcp/services/computeengine/client.go:210-213`, `providers/gcp/services/cloudsql/client.go:168-170`, `providers/gcp/services/memorystore/client.go:168-170`
**Description**: All three `GetRecommendations` implementations broke out of the iterator loop on a non-Done error and returned partial recommendations with nil error.
**Status:** ✔️ Resolved

**Resolved by:** All three iterator loops now `return nil, fmt.Errorf("<service>: iterate recommendations: %w", err)` instead of `break`. The existing memorystore "handles iterator error gracefully" subtest is renamed to "propagates iterator errors (no silent swallow)" and now asserts `wantErr: true`.

### Original implementation plan

**Goal:** All three iterator loops return the error instead of silently breaking, so callers see transient failures and retry.

**Files to modify:**

- `providers/gcp/services/computeengine/client.go:210-213` — change `break` to `return nil, fmt.Errorf("failed to iterate recommendations: %w", err)`.
- `providers/gcp/services/cloudsql/client.go:168-170` — same fix.
- `providers/gcp/services/memorystore/client.go:168-170` — same fix.
- Service-specific unit tests — add an error-propagation case per client.

**Steps:**

1. For each of the three clients, change the non-Done error branch from `break` to `return nil, fmt.Errorf(...)`.
2. Wrap the upstream error so the caller can identify the service (`"computeengine: iterate recommendations: %w"` etc.).
3. Update any test that expected partial results on error to expect the error.

**Edge cases the fix must handle:**

- `iterator.Done` handling must remain a `break`.
- Callers that swallow errors at a higher level — confirm the scheduler surfaces the error rather than silently retrying forever.

**Test plan:**

- Per client: `TestGetRecommendations_IteratorErrorReturnsError` — inject an iterator that returns an error on the second `Next()` call; assert the function returns that error wrapped, with no partial recommendations.

**Verification:**

- `cd providers/gcp && go test ./...`

**Effort:** `small`

## ~~HIGH: Nil Pointer Dereference on SQL `ListInstances` Response~~

**File**: `providers/gcp/services/cloudsql/client.go:203`
**Description**: Accessed `instances.Items` without checking whether `instances` itself is nil.
**Impact**: Process panic when the project has no SQL instances.
**Status:** ✔️ Resolved

**Resolved by:** `2fd9d0324` — nil guard added at cloudsql/client.go:203-205 (`if instances == nil { return commitments, nil }`).

## ~~HIGH: `memorystore.GetExistingCommitments` Uses `ReservedIpRange` as Commitment Proxy~~

**File**: `providers/gcp/services/memorystore/client.go:212`
**Description**: Used `instance.ReservedIpRange != ""` to identify committed instances; that field is the VPC peering CIDR, not a commitment flag.
**Impact**: Every VPC-peered Redis instance was classified as committed-use; the commitments list was completely wrong.
**Status:** ✔️ Resolved

**Resolved by:** `2fd9d0324` — `memorystore.GetExistingCommitments` (memorystore/client.go:181-187) now returns `nil, nil` with a comment explaining GCP Memorystore Redis has no commitment-status API.

## ~~MEDIUM: `GetCredentials` Closes Injected Test Client via `IsConfigured`~~ — RESOLVED

**File**: `providers/gcp/provider.go:224-248`
**Description**: `IsConfigured` and `ValidateCredentials` always `defer projectsClient.Close()` regardless of whether the client was injected via `SetProjectsClient`. Tests that called either function twice hit a closed connection on the second call.
**Status:** ✔️ Resolved

**Resolved by:** Both `IsConfigured` and `ValidateCredentials` now track `injected := p.projectsClient != nil` and only `defer projectsClient.Close()` when `!injected`. The injector retains lifecycle ownership of the test client. Updated godocs document the contract so the next reader doesn't reintroduce the unconditional Close.

### Original implementation plan

**Goal:** Injected test clients survive `IsConfigured()`; production behaviour (closing the internally-constructed client) is unchanged.

**Files to modify:**

- `providers/gcp/provider.go` — track whether the client was injected; only close if not.
- `providers/gcp/provider_test.go` — add a test that calls `IsConfigured()` twice with an injected mock and asserts `Close` was not invoked.

**Steps:**

1. Add `injectedProjectsClient bool` to `GCPProvider`; set `true` in `SetProjectsClient`.
2. In `IsConfigured` and `ValidateCredentials`, only `defer projectsClient.Close()` when `!p.injectedProjectsClient` — otherwise the injector owns lifecycle.
3. Document the lifetime contract in the `SetProjectsClient` godoc comment.

**Edge cases the fix must handle:**

- Same pattern for `regionsClient` and `resourceManagerService` if they have the same bug — audit while touching the file.

**Test plan:**

- `TestIsConfigured_InjectedClientNotClosed` — set mock via `SetProjectsClient`; call `IsConfigured()` twice; assert `Close()` call count == 0.

**Verification:**

- `cd providers/gcp && go test ./...`

**Effort:** `small`

## ~~MEDIUM: `GroupCommitments` Generates Non-Unique Names Under Nanosecond Resolution~~

**File**: `providers/gcp/services/computeengine/client.go:354-358`
**Description**: `fmt.Sprintf("cud-%s-%d", k.region, time.Now().UnixNano())` inside a map loop — two iterations in the same nanosecond produced duplicate names.
**Status:** ✔️ Resolved

**Resolved by:** `2fd9d0324` — `GroupCommitments` (computeengine/client.go:354-358) now generates the timestamp once outside the loop and appends a monotonically-increasing counter: `fmt.Sprintf("cud-%s-%d-%d", k.region, ts, counter)`.

## ~~MEDIUM: `NewProvider` with `config.Profile` Ignores Custom Client Options~~ — RESOLVED

**File**: `providers/gcp/provider.go:102-125`
**Description**: When `config.Profile` was set, `clientOpts` was initialised empty. Credentials always resolved via ADC regardless of caller intent. `NewProviderWithCredentials` existed but was unreachable from the registry path.
**Impact**: Callers that wanted to inject a service-account token source had to bypass the registry entirely, defeating the registry abstraction.
**Status:** ✔️ Resolved (cross-cuts `12_pkg_provider_interface.md`'s typed-fields refactor)

**Resolved by:** `ProviderConfig` gained an opaque `GCPTokenSource any` slot. `NewProvider` now type-asserts to `oauth2.TokenSource`; on success it appends `option.WithTokenSource(ts)` to `clientOpts` and forwards those opts into `getDefaultProject` so the project-ID lookup uses caller-provided credentials. When the slot is nil or wrong-typed it falls back silently to ADC, preserving prior behaviour. The slot is `any` rather than `oauth2.TokenSource` so `pkg/provider` keeps zero GCP SDK dependencies. See `12_pkg_provider_interface.md` for the broader refactor and tests.

### Implementation plan

**Goal:** `NewProvider(config)` can resolve credentials from a caller-supplied token source without bypassing the registry.

**Files to modify:**

- `pkg/provider/interface.go:74-90` — add `GCPTokenSource oauth2.TokenSource` (or a broader opaque credential slot) to `ProviderConfig`.
- `providers/gcp/provider.go:102-125` — when `config.GCPTokenSource != nil`, append `option.WithTokenSource(config.GCPTokenSource)` to `clientOpts`.
- Registry callers constructing `ProviderConfig` for GCP — wire the token source.

**Steps:**

1. Extend `ProviderConfig` with a GCP-specific token source field (or a generic interface the provider knows how to interpret).
2. In `NewProvider`, build `clientOpts` based on both `Profile` (project ID) and `GCPTokenSource`.
3. Update the registry call sites so operators who have resolved service-account credentials can pass them through.

**Edge cases the fix must handle:**

- `Profile` set but no token source — preserve ADC behaviour.
- Token source set but `Profile` empty — fall back to `getDefaultProject` with the token source installed.

**Test plan:**

- `TestNewProvider_WithTokenSource_UsesProvidedCredentials` — pass a fake token source; assert the underlying client received it.

**Verification:**

- `cd providers/gcp && go test ./...`

**Related issues:** `12_pkg_provider_interface.md#HIGH: ProviderConfig.Profile overloaded with undocumented provider-specific semantics`

**Effort:** `medium`

## ~~LOW: Missing Test Coverage for `getDefaultProject` Pagination~~ — RESOLVED

**File**: `providers/gcp/provider_test.go`
**Description**: No test for `getDefaultProject`. The pagination bug above existed without any test to detect it.
**Status:** ✔️ Resolved

**Resolved by:** Extracted the per-page callback used by `Pages()` into a named helper `findActiveProjectInPage(*string, *ListProjectsResponse) error`, then added `TestFindActiveProjectInPage` covering: page with one ACTIVE (sets out + returns sentinel), page with no ACTIVE projects (returns nil, leaves out untouched so Pages() walks to the next page), empty page, "first ACTIVE wins" within a page, and a multi-page simulation where page 1 has no ACTIVE and page 2 does — pinning the contract that the original bug violated (looking only at page 1). The full `getDefaultProject` integration is exercised implicitly by `NewProvider` callers; standing up a real `cloudresourcemanager` service was not necessary because the only logic the previous bug missed was the cross-page short-circuit, and that callback is now directly tested.

### Implementation plan

**Goal:** Pagination behaviour is pinned by tests so future refactors can't regress.

**Files to modify:**

- `providers/gcp/provider_test.go` — add unit tests.

**Steps:**

1. Add a mock for `service.Projects.List` that can return multi-page responses.
2. Implement both tests described under the CRITICAL issue above.

**Effort:** `small` (coupled with fix above)

## MEDIUM: Iterator-error propagation not unit-tested in computeengine/cloudsql (found during 2026-04-21 audit review)

**File**: `providers/gcp/services/computeengine/client.go`, `cloudsql/client.go` (and their `_test.go` siblings)
**Description**: Commit `f75aa6cf4` changed all three GCP service recommendation iterators (computeengine, cloudsql, memorystore) to return `(nil, fmt.Errorf("<svc>: iterate recommendations: %w", err))` on iterator failures instead of silently breaking out of the loop. The memorystore test file was updated to assert the new behaviour; computeengine and cloudsql were not. If a future refactor re-introduces the "break silently" pattern in either of those services, the test suite won't catch it.
**Impact**: Silent-data-loss regression risk. The shape is identical in all three services so covering one fully and leaving two uncovered is asymmetric.
**Status:** ❓ Needs triage

### Implementation plan

**Goal:** Parity with memorystore's iterator-error test coverage.

**Files to modify:**

- `providers/gcp/services/computeengine/client_test.go` — add `TestComputeEngineClient_GetRecommendations_IteratorError`.
- `providers/gcp/services/cloudsql/client_test.go` — same shape.

**Steps:**

1. Use the existing mock iterator pattern (see memorystore's `client_test.go` for the reference impl — it injects a `*MockRecommendationIterator` that returns an error on `Next()`).
2. Assert the returned error contains the service prefix (`"computeengine: iterate recommendations"` / `"cloudsql: ..."`).
3. Assert the returned slice is nil (no partial data leaks).

**Test plan:** as above.

**Verification:** `cd providers/gcp && go test -short ./services/computeengine/... ./services/cloudsql/...`

**Effort:** `small`.

## MEDIUM: getDefaultProject "no ACTIVE projects" error path lacks test coverage (found during 2026-04-21 audit review)

**File**: `providers/gcp/provider.go::getDefaultProject` + `provider_test.go`
**Description**: After commits `f75aa6cf4` and `f0a9da7e8`, `getDefaultProject` walks pages via `Pages()` and returns `"no active GCP projects found"` when no `LifecycleState == "ACTIVE"` project is seen across all pages. `TestFindActiveProjectInPage` covers the per-page callback, and `TestNewProvider_ProjectIDResolution` covers the typed-field-vs-Profile precedence chain, but no test exercises the full `getDefaultProject` path where the service returns a page of non-ACTIVE projects only and the function should produce the no-active error.
**Impact**: Regression risk — if a future refactor weakens the LifecycleState check (e.g. accepts "DELETE_REQUESTED" as alive), the error path stops firing and callers get a misleading ACTIVE project that's actually tombstoned.
**Status:** ❓ Needs triage

### Implementation plan

**Goal:** Pin the "no ACTIVE projects surface the exact error" contract.

**Files to modify:**

- `providers/gcp/provider_test.go` — add `TestGetDefaultProject_NoActiveProjects`.

**Steps:**

1. Build on the existing `ResourceManagerService` interface mock (`MockResourceManagerService` already exists in `provider_test.go`) or introduce a `cloudresourcemanager.Service` fake that returns a single page containing only non-ACTIVE projects (`DELETE_REQUESTED` / `DELETE_IN_PROGRESS`).
2. Call `getDefaultProject(ctx)` (no opts needed) and assert the returned error matches `"no active GCP projects found"`.

**Test plan:** as above.

**Verification:** `cd providers/gcp && go test -short ./.`

**Effort:** `small`.
