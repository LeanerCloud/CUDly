# Known Issues: API Account Handlers

> **Audit status (2026-04-20):** `0 still valid · 7 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~HIGH: deleteAccount does not verify account existence before deletion~~ — RESOLVED

**File**: `internal/api/handler_accounts.go:397-421`
**Description**: `deleteAccount` called `h.config.DeleteCloudAccount(ctx, id)` directly after UUID validation and admin check without first calling `GetCloudAccount`, so deleting a well-formed UUID that referred to a non-existent account returned HTTP 200 instead of 404.
**Impact**: Callers could not distinguish between "account deleted" and "account never existed."
**Status:** ✔️ Resolved

**Resolved by:** `031b958cf` — adds `GetCloudAccount` existence check at lines 408-414 that returns `errNotFound` when account is missing, matching the `updateAccount` pattern.

## ~~HIGH: saveAccountCredentials returns 500 when credStore is nil, bypassing 404 check~~ — RESOLVED

**File**: `internal/api/handler_accounts.go:442-453`
**Description**: When `h.credStore == nil`, the handler previously returned an unclassified error before the account existence check, so an admin probing credential storage for a non-existent account UUID got a 500 instead of 404.
**Impact**: The 404 path was bypassed when `credStore` is nil, violating the 404-first intent of the existence check.
**Status:** ✔️ Resolved

**Resolved by:** Reordered `saveAccountCredentials` so `GetCloudAccount` runs before the `h.credStore == nil` guard. Missing accounts now return `errNotFound` even when credStore is unconfigured. Locked down by `TestSaveAccountCredentials_NotFound_WithNilCredStore` (asserts 404 + nil result).

### Original implementation plan

**Goal:** 404 responses must win over the `credStore not configured` 500 for non-existent accounts.

**Files to modify:**

- `internal/api/handler_accounts.go:442-453` — move the `h.credStore == nil` guard to after the `acct == nil` check
- `internal/api/handler_accounts_test.go` — add the missing-account-with-nil-credStore test case

**Steps:**

1. In `saveAccountCredentials`, reorder so we first fetch the account (`GetCloudAccount`) and return `errNotFound` when missing; then check `h.credStore == nil` and return a 500.
2. Keep the UUID validation, permission check, body parsing, and `validCredentialTypes` lookup before the account fetch (they produce 400s that should beat 404).

**Edge cases the fix must handle:**

- Valid UUID, missing account, nil credStore → 404 (not 500).
- Valid UUID, existing account, nil credStore → 500 "credential store not configured".
- Malformed UUID or invalid credential_type → 400 regardless of credStore.

**Test plan:**

- New test `TestSaveAccountCredentials_NotFound_WithNilCredStore` — asserts 404 when account missing and `credStore` is nil.
- Existing `TestSaveAccountCredentials_Success` and `TestSaveAccountCredentials_InvalidType` continue to pass.

**Verification:**

- `go test ./internal/api/...`

**Effort:** `small`

## ~~HIGH: ambientCredResult treats empty AWSAuthMode as successful "role assumption"~~ — RESOLVED

**File**: `internal/api/handler_accounts.go:486-503`
**Description**: An AWS account with `AWSAuthMode = ""` (unset/default) and a non-empty `AWSRoleARN` previously returned `OK: true` "role assumption configured". The negative check `!= "access_keys"` matched the empty string.
**Impact**: An operator who created an account without setting `aws_auth_mode` would see the test endpoint report success even though runtime authentication would fail.
**Status:** ✔️ Resolved

**Resolved by:** `031b958cf` — replaces the negative check with an explicit allowlist (`switch acct.AWSAuthMode { case "workload_identity_federation": ...; case "role_arn", "bastion": ... }`) so unset auth modes fall through and return `OK: false`.

## ~~MEDIUM: parseServiceOverridePath silently ignores trailing path segments~~ — RESOLVED

**File**: `internal/api/handler_accounts.go:962-992`
**Description**: The old guard `len(parts) < 4` allowed paths like `uuid/service-overrides/aws/ec2/extra/segments` to pass validation with extra segments silently dropped.
**Impact**: Misrouted requests succeeded rather than returning 400, hiding client-side bugs.
**Status:** ✔️ Resolved

**Resolved by:** `031b958cf` — changed the guard to `if len(parts) != 4` so trailing segments now produce a 400.

## ~~MEDIUM: No size or key validation on CredentialsRequest.Payload before storage~~ — RESOLVED

**File**: `internal/api/handler_accounts.go:424-464`
**Description**: `CredentialsRequest.Payload` was `map[string]interface{}` with only the 1 MB body limit as a guard. The payload was re-marshalled and stored without any per-credential-type schema check, so an admin could store arbitrarily structured JSON blobs that would fail silently at runtime.
**Impact**: Silent misconfiguration slipped through to the resolver.
**Status:** ✔️ Resolved

**Resolved by:** `validateCredentialPayload` added to `internal/api/validation.go` enforces per-`credential_type` schemas: `aws_access_keys`/`azure_client_secret`/`azure_wif_private_key` use a flat required+optional allowlist; `gcp_service_account` requires `type="service_account"` plus the four standard SA fields; `gcp_workload_identity_config` requires `type="external_account"` plus the standard external-account fields with `credential_source` as a nested object. Unknown keys, missing required keys, non-string values, and nesting beyond depth 2 all return 400. Wired into `saveAccountCredentials` after the `validCredentialTypes` lookup. Covered by `TestValidateCredentialPayload` (18 sub-tests).

### Original implementation plan

**Goal:** Every accepted credentials payload must match the shape required by its `credential_type`.

**Files to modify:**

- `internal/api/handler_accounts.go:438-460` — insert payload shape validation after the `validCredentialTypes` lookup
- `internal/api/validation.go` — add `validateCredentialPayload(credType string, payload map[string]any) error`
- `internal/api/validation_test.go` — add unit tests per credential_type

**Steps:**

1. Define per-`credential_type` allowlists (e.g. `aws_access_keys` → exactly `{access_key_id, secret_access_key}` both non-empty strings; `azure_client_secret` → `{client_secret}`; `gcp_service_account` → parseable service-account JSON; `azure_wif_private_key`, `gcp_workload_identity_config` → known keys only).
2. Reject payloads with extra/unknown keys, missing required keys, or wrong value types (`reflect.Kind` or a JSON Schema style walk).
3. Cap nesting depth at 2 to stop nested-map abuse.
4. Call from `saveAccountCredentials` before `json.Marshal(req.Payload)`.

**Edge cases the fix must handle:**

- Nil/empty payload for any type → 400.
- Extra unknown keys → 400 (not silent drop).
- Wrong types (e.g. boolean where string expected) → 400.
- GCP JSON whose `type != service_account` → 400.

**Test plan:**

- Per-type happy-path test + extra-key test + missing-key test + wrong-type test.
- Update `TestSaveAccountCredentials_Success` bodies if schema narrows.

**Verification:**

- `go test ./internal/api/...`

**Related issues:** `03_credentials_resolver#MEDIUM-AWSWebIdentityTokenFile` for downstream payload-trust assumptions.

**Effort:** `medium`

## ~~LOW: Missing test coverage for deleteAccount with non-existent ID~~ — RESOLVED

**File**: `internal/api/handler_accounts_test.go:219`
**Description**: `TestDeleteAccount_Success` previously existed but there was no test for deleting a UUID that does not correspond to any account.
**Impact**: Regression risk — a refactor that removed the existence check would not be caught by tests.
**Status:** ✔️ Resolved

**Resolved by:** `TestDeleteAccount_NotFound` added; uses `GetCloudAccountFn` to force not-found and asserts `IsNotFoundError(err) == true` and that `DeleteCloudAccount` was never invoked (via new `DeleteCloudAccountFn` mock hook).

### Original implementation plan

**Goal:** Lock down the 404 behaviour added by `031b958cf`.

**Files to modify:**

- `internal/api/handler_accounts_test.go` — add `TestDeleteAccount_NotFound`

**Steps:**

1. Set up a `MockConfigStore` whose `GetCloudAccountFn` returns `(nil, nil)` for the target UUID.
2. Call `handler.deleteAccount` and assert the error equals `errNotFound`.
3. Assert `DeleteCloudAccount` was never called.

**Test plan:**

- New test `TestDeleteAccount_NotFound`.

**Verification:**

- `go test ./internal/api/...`

**Effort:** `small`

## ~~LOW: Contact email stored without format validation~~ — RESOLVED

**File**: `internal/api/handler_accounts.go:268-301`
**Description**: `ContactEmail` was accepted and stored verbatim inside `cloudAccountFromRequest`. `validateCloudAccountRequest` had no regex or format check.
**Impact**: Invalid addresses silently persisted and broke downstream notifications (bounce, SMTP reject).
**Status:** ✔️ Resolved

**Resolved by:** `validateEmailFormat` added to `internal/api/validation.go` (uses `net/mail.ParseAddress`); `validateCloudAccountRequest` calls it after the external_id check. Empty emails remain allowed (field is optional). Covered by `TestCreateAccount_InvalidContactEmail` and `TestCreateAccount_EmptyContactEmail`.

### Original implementation plan

**Goal:** Reject malformed contact emails at the API boundary.

**Files to modify:**

- `internal/api/handler_accounts.go:268-281` — add a call to `validateEmailFormat` when `ContactEmail != ""`
- `internal/api/validation.go` — add `validateEmailFormat` using `net/mail.ParseAddress`

**Steps:**

1. In `validateCloudAccountRequest`, if `req.ContactEmail != ""` call `net/mail.ParseAddress(req.ContactEmail)`; on error return `NewClientError(400, "contact_email is not a valid email address")`.
2. Leave empty emails allowed (contact email is optional).

**Test plan:**

- `TestCreateAccount_InvalidContactEmail` — asserts 400 for `"not-an-email"`.
- `TestCreateAccount_EmptyContactEmail` — asserts success when field omitted.

**Verification:**

- `go test ./internal/api/...`

**Effort:** `small`
