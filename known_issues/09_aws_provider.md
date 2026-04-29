# Known Issues: AWS Provider

> **Audit status (2026-04-20):** `0 still valid · 6 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~CRITICAL: Race Condition on p.cfg via Concurrent IsConfigured Calls~~ — RESOLVED

**File**: `providers/aws/provider.go:124-159`
**Description**: `IsConfigured()` previously mutated `p.cfg` on every successful call. Concurrent callers (`GetCredentials`, `ValidateCredentials`, `GetServiceClient`, `GetRecommendationsClient`) raced on that write with no mutex.
**Impact**: Data race; non-deterministic panics or corrupted config state in production.
**Status:** ✔️ Resolved

**Resolved by:** `b539422dd` — `AWSProvider` now holds `cfgOnce sync.Once` and `cfgErr error` (line 68-69). `IsConfigured()` runs `loadConfig` at most once via `cfgOnce.Do` (line 125); subsequent calls are lock-free reads of `cfgErr`. No data race possible.

## ~~HIGH: GetAccounts Uses p.cfg Without Checking IsConfigured First~~ — RESOLVED

**File**: `providers/aws/provider.go:246-275`
**Description**: `GetAccounts` used to build an STS client directly from `p.cfg` without checking `IsConfigured()`; pre-config calls produced opaque SDK errors.
**Impact**: Confusing authentication errors instead of a clear "AWS is not configured" message.
**Status:** ✔️ Resolved

**Resolved by:** `b539422dd` — `GetAccounts` now has `if !p.IsConfigured() { return nil, fmt.Errorf("AWS is not configured") }` at line 247-249 before any SDK use.

## ~~HIGH: GetRegions Uses p.cfg Without Checking IsConfigured First~~ — RESOLVED

**File**: `providers/aws/provider.go:278-318`
**Description**: Same class of bug as `GetAccounts` — the EC2 client was built from `p.cfg` without a prior `IsConfigured()` check.
**Impact**: Same as `GetAccounts`.
**Status:** ✔️ Resolved

**Resolved by:** `b539422dd` — `GetRegions` has the `if !p.IsConfigured()` guard at line 279-281.

## ~~MEDIUM: appendOrgAccounts Silently Drops Mid-Pagination Errors~~ — RESOLVED

**File**: `providers/aws/provider.go:213-243`
**Description**: A mid-pagination error from `paginator.NextPage` returned the already-accumulated accounts with no error and no log message. The comment said "not in an org or no permissions" but the path also swallowed throttling and network errors, producing silently incomplete account lists.
**Status:** ✔️ Resolved

**Resolved by:** `appendOrgAccounts` now returns `([]Account, error)` and classifies the error via `errors.As(err, &smithy.APIError)`. The new `orgListAccountsSilentErrorCodes` map (`AWSOrganizationsNotInUseException`, `AccessDeniedException`) marks the only two codes treated as silent fallbacks; everything else (throttling, network, opaque errors) is logged via `logging.Warnf` and propagated. `GetAccounts` returns the error to the caller so an incomplete list can't slip into the purchase flow as if it were complete. Locked down by four new sub-tests in `TestAWSProvider_GetAccounts_WithMock` covering the not-in-org, access-denied, throttle-mid-page, and opaque-error cases.

### Original implementation plan

**Goal:** Distinguish "access denied / not in org" (silent) from "transient or unknown" (loud).

**Files to modify:**

- `providers/aws/provider.go:213-243` — refine the error classification
- `providers/aws/provider_test.go` — new test cases

**Steps:**

1. At the `err != nil` branch, use `errors.As` to check for `*types.AWSOrganizationsNotInUseException` and access-denied variants (`*smithy.GenericAPIError` with codes `AccessDeniedException`, `AWSOrganizationsNotInUseException`). Return the accumulated list silently only for those.
2. For any other error, log via `logging.Warnf` and return the error to the caller. Decide whether `GetAccounts` should propagate the error or just stop pagination — propagate is safer (incomplete data is dangerous for purchase flows).
3. Consider a config flag (`AWS_ORG_LIST_PARTIAL_OK`) to allow the current silent behaviour for users who intentionally run with limited permissions.

**Edge cases the fix must handle:**

- First page succeeds, second page throttles → return error (partial list unsafe).
- First page returns `AWSOrganizationsNotInUseException` → return just the single-account list silently.
- Permission denied on page 1 → same as current behaviour (silent fallback to single account).

**Test plan:**

- `TestAppendOrgAccounts_NotInOrg_ReturnsCallerOnly` — asserts silent behaviour.
- `TestAppendOrgAccounts_ThrottleMidPage_ReturnsError` — asserts error propagation.
- `TestAppendOrgAccounts_AccessDeniedPage1_ReturnsCallerOnly` — asserts the historical silent path.

**Verification:**

- `go test ./providers/aws/...`

**Effort:** `small`

## ~~LOW: Test Uses nil Context for GetServiceClient~~ — RESOLVED

**File**: `providers/aws/provider_test.go:233, 264`
**Description**: Two test cases passed `nil` as the `context.Context` to `GetServiceClient`. Works today because the function ignores `ctx`, but any future refactor calling `ctx.Done()` would panic.
**Status:** ✔️ Resolved

**Resolved by:** Replaced both `nil` calls with `context.Background()`.

### Original implementation plan

**Goal:** Pass `context.Background()` consistently.

**Files to modify:**

- `providers/aws/provider_test.go:233, 264`

**Steps:**

1. Replace `p.GetServiceClient(nil, ...)` with `p.GetServiceClient(context.Background(), ...)` at both call sites.

**Test plan:**

- Existing tests continue to pass; no new tests required.

**Verification:**

- `go test ./providers/aws/...`

**Effort:** `small`

## ~~LOW: GetCredentials Source Type Detection Is Fragile~~ — RESOLVED

**File**: `providers/aws/provider.go:162-186`
**Description**: Source detection compared `creds.Source` against hardcoded literals `"SharedConfigCredentials"` and `"AssumeRoleProvider"` — SDK internals with no stable contract. An SDK rename would silently downgrade the credential-source UX.
**Status:** ✔️ Resolved

**Resolved by:** Extracted the literals into named constants `awsSourceSharedConfigCredentials` and `awsSourceAssumeRoleProvider` with a doc comment explaining they are SDK internals to re-audit on every aws-sdk-go-v2 bump. Added `TestGetCredentials_SourceMapping` covering shared-config → file, assume-role → IAM-role, unknown → environment, and empty → environment. The test is the guard rail: an SDK rename now causes a visible test failure rather than a silent UX regression.

### Original implementation plan

**Goal:** Make the SDK coupling explicit so an SDK upgrade is a compile-or-test failure rather than a silent misclassification.

**Files to modify:**

- `providers/aws/provider.go:162-186` — move literals to named constants and add a comment
- `providers/aws/provider_test.go` — add a test that panics/fails if the SDK constant is renamed

**Steps:**

1. Define `const (awsSourceSharedConfig = "SharedConfigCredentials"; awsSourceAssumeRole = "AssumeRoleProvider")` with a comment noting they are SDK internals and must be re-verified on every `aws-sdk-go-v2` upgrade.
2. Use the constants in the switch.
3. Add a table-driven test that exercises each known source value and asserts the mapping; annotate it "if this fails, re-audit sdk internals".

**Edge cases the fix must handle:**

- SDK emits a new source string — default case still returns `CredentialSourceEnvironment`; consider returning a new `CredentialSourceUnknown` to surface the unknown case.

**Test plan:**

- `TestGetCredentials_SourceMapping` — loops through known sources, asserts mapping.

**Verification:**

- `go test ./providers/aws/...`

**Effort:** `small`
