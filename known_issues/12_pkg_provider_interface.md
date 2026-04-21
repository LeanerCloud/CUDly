# Known Issues: Provider Interface

> **Audit status (2026-04-20):** `0 from original audit · 6 resolved · 0 partially fixed · 0 moved · 1 new item surfaced during 2026-04-21 audit review`

## ~~HIGH: Azure `GetSupportedServices` advertises `ServiceNoSQL` but `GetServiceClient` has no handler~~

**File**: `providers/azure/provider.go:314-322` and `providers/azure/provider.go:324-352`
**Description**: `GetSupportedServices()` returned `common.ServiceNoSQL`, but the `GetServiceClient` switch had no `case common.ServiceNoSQL:` branch.
**Impact**: Callers iterating `GetSupportedServices()` and calling `GetServiceClient` for each service got a runtime error for `ServiceNoSQL`.
**Status:** ✔️ Resolved

**Resolved by:** `2fd9d0324` — `GetServiceClient` (provider.go:340-351) now has `case common.ServiceNoSQL: return NewCosmosDBClient(p.cred, subscriptionID, region), nil`.

## ~~HIGH: GCP has `cloudstorage` and `memorystore` clients but they are unreachable via the interface~~

**File**: `providers/gcp/provider.go:378-402`
**Description**: `GetSupportedServices()` returned only `[ServiceCompute, ServiceRelationalDB]`. Fully-implemented clients at `services/cloudstorage/` and `services/memorystore/` were unreachable.
**Status:** ✔️ Resolved

**Resolved by:** `2fd9d0324` — `GetSupportedServices` (provider.go:379-386) now includes `ServiceCache` and `ServiceStorage`, and `GetServiceClient` (provider.go:389-402) wires `memorystore.NewClient` and `cloudstorage.NewClient` into the switch.

## ~~HIGH: `ProviderConfig.Profile` overloaded with undocumented provider-specific semantics~~ — RESOLVED

**File**: `pkg/provider/interface.go:74-90`
**Description**: `Profile` carried three different meanings: AWS profile name, Azure subscription ID, GCP project ID. Godoc explained the overload but there were no per-provider fields; callers had to know which provider was in use and interpret `Profile` accordingly.
**Impact**: Easy to silently get the wrong subscription/project when a caller assumed `Profile` was always an AWS profile.
**Status:** ✔️ Resolved

**Resolved by:** `ProviderConfig` now exposes typed identity fields — `AWSProfile`, `AzureSubscriptionID`, `GCPProjectID` — and typed pre-resolved credential slots — `AzureTokenCredential` and `GCPTokenSource` (both `any` so `pkg/provider` keeps no Azure/GCP SDK deps; providers type-assert to the expected concrete type and silently ignore mismatches). `Profile` is marked `// Deprecated:` with godoc explaining the precedence: typed field wins; `Profile` is the documented fallback.

Each provider's `NewProvider` reads the typed field through a small `resolveXProfile` / `resolveAzureSubscriptionID` / `resolveGCPProjectID` helper, falling back to `Profile` only when the typed field is empty. GCP additionally honours `GCPTokenSource` by appending `option.WithTokenSource(ts)` to `clientOpts` and forwarding the same opts into `getDefaultProject` so the lookup uses caller-provided credentials. Azure honours `AzureTokenCredential` by storing it directly on the provider (skipping the lazy `DefaultAzureCredential` initialisation in `GetCredentials`).

Tests added:

- `providers/aws/provider_test.go::TestNewAWSProvider` — table cases for typed-precedence and typed-alone.
- `providers/azure/provider_test.go::TestNewAzureProvider` — table cases for typed-precedence and typed-alone.
- `providers/azure/provider_test.go::TestNewAzureProvider_TokenCredentialInjection` — nil credential, valid `azcore.TokenCredential`, and wrong-typed credential (silently ignored).
- `providers/gcp/provider_test.go::TestNewProvider_ProjectIDResolution` — typed-precedence, typed-alone, deprecated-fallback, nil-config, empty-config.

Existing callers that still use `Profile` continue to work unchanged.

### Implementation plan

**Goal:** `ProviderConfig` has typed per-provider identity fields (`AWSProfile`, `AzureSubscriptionID`, `GCPProjectID`) so callers cannot confuse them, while preserving backwards compatibility with existing `Profile` users.

**Files to modify:**

- `pkg/provider/interface.go:74-90` — add typed fields; keep `Profile` as deprecated alias.
- `providers/aws/provider.go`, `providers/azure/provider.go`, `providers/gcp/provider.go` — read the typed field first, fall back to `Profile`.
- Callers that construct `ProviderConfig` — migrate to the typed field over time.

**Steps:**

1. Add `AWSProfile string`, `AzureSubscriptionID string`, `GCPProjectID string` to `ProviderConfig` alongside `Profile`.
2. In each provider's `NewProvider`, read the typed field; if empty, fall back to `Profile` so existing callers keep working.
3. Update godoc to mark `Profile` as deprecated.
4. File a follow-up ticket to migrate all callers and remove `Profile` in a major version bump.

**Edge cases the fix must handle:**

- Callers that set both `Profile` and the typed field — typed field wins.
- Provider registry factories that operate generically — no change needed; only the provider-specific `NewProvider` functions consult the new fields.

**Test plan:**

- Per provider: add unit test asserting the typed field takes precedence over `Profile`.

**Verification:**

- `go test ./pkg/... ./providers/...`

**Related issues:** `11_gcp_provider.md#MEDIUM: NewProvider with config.Profile Ignores Custom Client Options` — the same refactor should also add `GCPTokenSource` and analogous Azure token credential.

**Effort:** `medium`

## ~~MEDIUM: `Registry.GetProvider` silently discards factory errors~~ — RESOLVED

**File**: `pkg/provider/registry.go:52-70`
**Description**: `GetProvider` previously returned `Provider` (single value) and `nil` on miss — callers couldn't distinguish "not registered" from "factory failed". Logging had been added earlier but the signature still hid the distinction.
**Status:** ✔️ Resolved

**Resolved by:** `GetProvider` now returns `(Provider, error)`. "not registered" produces `"provider %s not registered"`; factory failures produce `"provider %s factory failed: %w"` wrapping the underlying error. The single non-test caller (`DetectProvider` in `pkg/provider/credentials.go`) was updated to surface the wrapped error directly. Two existing registry tests (`TestRegistry_GetProvider`, `TestRegistry_GetProvider_FactoryError`) now assert the error messages in addition to the nil result. The `TestDetectProvider_NotFound` assertion was updated from "not found" to "not registered".

### Original implementation plan

**Goal:** `GetProvider` returns an error so callers can distinguish and surface the root cause.

**Files to modify:**

- `pkg/provider/registry.go:52-70` — change signature to `GetProvider(name string) (Provider, error)`.
- Every caller of `GetProvider` across the repo — handle the error.

**Steps:**

1. Change the signature: return `nil, fmt.Errorf("provider %s not registered", name)` on miss, `nil, fmt.Errorf("provider %s factory failed: %w", name, err)` on factory error.
2. Grep for `registry.GetProvider(` and update each caller.
3. Keep the `log.Printf` or remove it once callers handle the error themselves (prefer the latter — avoid double logging).

**Edge cases the fix must handle:**

- `GetAllProviders` iterating the registry — propagate factory errors up rather than skip silently; surface them in `DetectAvailableProviders`' structured output.

**Test plan:**

- `TestGetProvider_ReturnsFactoryError` — register a factory that returns an error; assert `GetProvider` surfaces the wrapped error.
- `TestGetProvider_ReturnsNotRegisteredError` — assert error message for unknown provider.

**Verification:**

- `go test ./pkg/...`

**Effort:** `medium` (wide caller update)

## ~~MEDIUM: GCP `GetAccounts` omits `Provider` field on returned Account structs~~

**File**: `providers/gcp/provider.go:278-302`
**Description**: `Provider: common.ProviderGCP` was never set on accounts; downstream code routing on `account.Provider` fell through to ambient credentials.
**Status:** ✔️ Resolved

**Resolved by:** `2fd9d0324` — `GetAccounts` (provider.go:280-299) now sets `Provider: common.ProviderGCP` on every `common.Account` (both the normal loop and the fallback default account).

## ~~LOW: Duplicate `ServiceType` constant values not enforced~~ — RESOLVED

**File**: `pkg/common/types.go:57-58`
**Description**: `ServiceOpenSearch` and `ServiceElasticsearch` were independent `const ServiceType = "opensearch"` declarations. Intentional aliasing, but a future const declared with the same value but different intent would get no compile error.
**Status:** ✔️ Resolved

**Resolved by:** `ServiceElasticsearch = ServiceOpenSearch` — typed alias instead of duplicate string literal. The Go const block correctly preserves the `ServiceType` type. A future declaration of a third const with `"opensearch"` as the value would now require explicit intent rather than silent aliasing.

### Original implementation plan

**Goal:** `ServiceElasticsearch` is defined as a named alias of `ServiceOpenSearch`, so future duplicates with a different intent trigger a compile error.

**Files to modify:**

- `pkg/common/types.go:57-58` — change `ServiceElasticsearch ServiceType = "opensearch"` to `ServiceElasticsearch = ServiceOpenSearch`.

**Steps:**

1. Replace the raw string literal with a typed reference to `ServiceOpenSearch`.
2. Run `go vet` and the full test suite to ensure no downstream code depends on `ServiceElasticsearch` being a distinct `const` declaration rather than an alias.

**Edge cases the fix must handle:**

- Go's `const` block allows `ServiceElasticsearch = ServiceOpenSearch` without re-typing; confirm the resulting type remains `ServiceType`.

**Test plan:**

- Existing tests pass.
- Optional: `TestServiceElasticsearchIsAliasOfOpenSearch` — `require.Equal(common.ServiceOpenSearch, common.ServiceElasticsearch)`.

**Verification:**

- `go test ./pkg/...`

**Effort:** `small`

## LOW: Typed credential slots silently discard wrong-type values (found during 2026-04-21 audit review)

**File**: `pkg/provider/interface.go` (`GCPTokenSource any`, `AzureTokenCredential any`) + the three provider `NewProvider` functions that consume them
**Description**: The typed credential slots `AzureTokenCredential any` and `GCPTokenSource any` (added in commit `f0a9da7e8`) are `any` rather than concrete SDK types to keep `pkg/provider` free of Azure/GCP SDK deps. Each provider's `NewProvider` type-asserts to the expected concrete type (`azcore.TokenCredential` / `oauth2.TokenSource`); if the assertion fails, the slot is silently ignored and the provider falls back to ambient credentials (`DefaultAzureCredential` / ADC). The godoc mentions the behaviour, but a production caller that mis-wires the slot (e.g. passes a *string or a typo'd interface) will see "permission denied" errors at request time with no hint that their credential injection was silently dropped.
**Impact**: Debuggability gap. A caller who thinks they supplied custom credentials but actually passed the wrong type sees the confusing "ADC unavailable" error in environments where ADC genuinely is unavailable (Lambda / Cloud Run with no ambient GCP creds).
**Status:** ❓ Needs triage

### Implementation plan

**Goal:** Wrong-typed credential slot values are logged at `Warnf` level so mis-wirings surface in logs even though they don't break the pipeline.

**Files to modify:**

- `providers/gcp/provider.go::NewProvider` — inside the type-assert branch, if `config.GCPTokenSource != nil` AND the assert fails, `logging.Warnf("gcp provider: config.GCPTokenSource is %T, expected oauth2.TokenSource — ignoring", config.GCPTokenSource)`.
- `providers/azure/provider.go::NewAzureProvider` — analogous for `AzureTokenCredential` → `azcore.TokenCredential`.

**Steps:** two analogous 3-line edits; no tests required beyond eyeballing the log output in manual verification.

**Verification:** build-only.

**Effort:** `small`.
