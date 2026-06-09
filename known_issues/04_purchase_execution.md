# Known Issues: Purchase Execution

> **Audit status (2026-04-20):** `0 still valid · 7 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~CRITICAL: Root execution record saved with zero-purchase data after multi-account fan-out~~ — RESOLVED

**File**: `internal/purchase/manager.go:122-142`
**Description**: `ProcessScheduledPurchases` previously always called `SavePurchaseExecution` on the root `exec` after `executePurchase` returned, even when per-account fan-out had already saved one record per account — creating a ghost root record showing zero purchases.
**Impact**: Audit dashboard showed plan executions as "completed" with zero purchases and zero savings whenever multi-account fan-out was used.
**Status:** ✔️ Resolved

**Resolved by:** `9531681a4` — `executePurchase` now returns a `wasMultiAccount bool`; `executeAndFinalize` (line 122) guards the root save with `if !wasMultiAccount { m.config.SavePurchaseExecution(ctx, exec) }` so the root record is only written in the single-account path.

## ~~CRITICAL: Silent credential fallback causes purchases in the wrong cloud account~~ — RESOLVED

**File**: `internal/purchase/execution.go:91-134`, `139-199`
**Description**: `resolveAWSProvider`/`resolveAzureProvider`/`resolveGCPProvider` previously caught credential-resolution errors, emitted a `Warnf`, and returned `nil`, so `executeSinglePurchase` fell through to ambient credentials and purchased RIs/SPs in the wrong account.
**Impact**: Commitments silently made in the wrong AWS/Azure/GCP account; coverage gaps with no visible error.
**Status:** ✔️ Resolved

**Resolved by:** `9531681a4` — `resolveAccountProvider` (line 139) and its three sub-functions now return errors instead of nil; `executeForAccount` (line 102) checks the error, marks the record `failed`, saves it, and returns the error so the caller surfaces the failure. No ambient fallback on error.

## ~~HIGH: `GetPlanAccounts` error silently falls back to single-account with ambient credentials~~ — RESOLVED

**File**: `internal/purchase/execution.go:43-52`
**Description**: A transient `GetPlanAccounts` error previously dropped the code into the single-account branch with ambient credentials.
**Impact**: Transient DB errors caused real purchases in the wrong account, with the full un-split recommendation set.
**Status:** ✔️ Resolved

**Resolved by:** `9531681a4` — `executePurchase` now returns the error (line 47) on `GetPlanAccounts` failure; the single-account fallback only runs when `err == nil && len(accounts) == 0`.

## ~~HIGH: Credential fallback not reflected in audit record~~ — RESOLVED

**File**: `internal/purchase/execution.go:122-133`
**Description**: `SavePurchaseExecution` failures in `executeForAccount` previously logged `AUDIT LOSS` and continued, returning `nil`.
**Impact**: Silent purchases with no audit trail.
**Status:** ✔️ Resolved

**Resolved by:** `9531681a4` — `executeForAccount` now returns `fmt.Errorf("AUDIT LOSS: failed to save execution record for account %s: %w", …)` on save failure, and the multi-account summary bubbles that up to `executePurchase`.

## ~~MEDIUM: `updatePlanProgress` called even when multi-account fan-out fails partially~~ — RESOLVED

**File**: `internal/purchase/manager.go:136-140`
**Description**: `updatePlanProgress` was previously called regardless of outcome, incrementing `CurrentStep` even on partial multi-account failure.
**Impact**: Failed-account allocations permanently skipped.
**Status:** ✔️ Resolved

**Resolved by:** `9531681a4` — `executeAndFinalize` now wraps the progress call in `if execErr == nil { m.updatePlanProgress(...) }`.

## ~~MEDIUM: No test coverage for credential-fallback-to-ambient scenario~~ — RESOLVED

**File**: `internal/purchase/execution_test.go`
**Description**: `TestManager_ExecutePurchase_MultiAccount` only covered the happy path. There was no test asserting that a credential-resolution error marks the execution failed and never invokes the provider factory.
**Status:** ✔️ Resolved

**Resolved by:** Added `TestExecuteForAccount_CredentialFailure_MarksFailed` — table-driven across AWS access_keys, Azure client_secret, and GCP service_account paths. The credential store returns no data, which triggers the "no credentials stored" error in each resolver. The test asserts `executePurchase` returns an error containing "credential resolution failed", that `SavePurchaseExecution` is still called with `Status == "failed"` (audit record preserved), and that `providerFactory.CreateAndValidateProvider` is **never** invoked (no ambient fallback). Locks down the invariant from `9531681a4`.

### Original implementation plan

**Goal:** Lock down the "no ambient fallback on credential failure" invariant with an explicit test.

**Files to modify:**

- `internal/purchase/execution_test.go` — add `TestExecuteForAccount_CredentialFailure_MarksFailed`

**Steps:**

1. Build a `MockCredentialStore` whose `LoadRawFn` returns a non-nil error.
2. Construct a Manager with access_keys mode accounts in a plan.
3. Call `executePurchase`; assert the returned error wraps "credential resolution failed" and references the account ID.
4. Assert `providerFactory.CreateAndValidateProvider` is **never** invoked (no ambient fallback).
5. Assert the saved `PurchaseExecution` has `Status == "failed"` and `Error` contains the credential error.

**Edge cases the fix must handle:**

- AWS, Azure, and GCP variants — one subtest each (parametrise by auth mode).
- Ensure SavePurchaseExecution is still called (audit record) despite the failure.

**Test plan:**

- New table-driven test with three cases, one per cloud provider.

**Verification:**

- `go test ./internal/purchase/...`

**Related issues:** `04_purchase_execution#CRITICAL-silent-credential-fallback` (fixed; this is its regression guard).

**Effort:** `small`

## ~~LOW: `planProvider` result is computed but never used~~ — RESOLVED

**File**: `internal/purchase/execution.go:201-211`, referenced only in `internal/purchase/execution_test.go:464`
**Description**: `planProvider` was unreferenced outside its own test. Dead code.
**Status:** ✔️ Resolved

**Resolved by:** Deleted `planProvider` and the orphaned `TestPlanProvider` test. Multi-account fan-out (`executePurchase`) reaches the right provider via the per-account `CloudAccount.Provider` field, so no caller needed to parse the plan's service-key prefix. `go vet`/`go test ./...` confirm nothing else referenced the function.

### Original implementation plan

**Goal:** Remove the dead function or wire it into a real caller.

**Files to modify:**

- `internal/purchase/execution.go:201-211` — remove function (preferred)
- `internal/purchase/execution_test.go:~464` — remove the orphaned test
- Any call site that ought to use `planProvider` for provider selection

**Steps:**

1. Grep for potential callers (`planProvider`, `Services map key parsing`, `provider:service`).
2. Decision: if no legitimate caller wants it, delete both the function and its test. Otherwise, wire it in (likely in the ramp scheduler or account filter) and update its test to cover the real path.

**Edge cases the fix must handle:**

- Plans with services keyed without provider prefix — existing function returns `""`; make sure any new caller tolerates that.

**Test plan:**

- If deleted: rely on `go vet`/`go test` to confirm nothing else referenced it.
- If wired: assert the new caller's behaviour via its own test.

**Verification:**

- `go build ./...` and `go test ./...`

**Effort:** `small`
