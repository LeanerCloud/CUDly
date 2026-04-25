<!-- markdownlint-disable MD040 MD060 -->
# Acceptance Criteria

BDD-style scenarios using Given / When / Then.
Each scenario maps to one or more automated tests or manual verification steps.

---

## A. Account CRUD

### A-1: Create an AWS account with Role ARN

**Given** no cloud accounts are configured
**When** I POST `/api/accounts` with:

```json
{
  "name": "Prod-US",
  "provider": "aws",
  "external_id": "123456789012",
  "aws_auth_mode": "role_arn",
  "aws_role_arn": "arn:aws:iam::123456789012:role/CUDly"
}
```

**Then** the response is `201` with the created account object
**And** `GET /api/accounts` returns the account in the list
**And** `credentials_configured` is `false` (no credentials provided yet)

---

### A-2: Create account with inline credentials

**Given** a valid account payload
**When** I include `credentials: { "access_key_id": "...", "secret_access_key": "..." }` in the POST (inline credentials on create; `type` is inferred from `aws_auth_mode = "access_keys"`)
**Then** the account is created AND credentials are stored
**And** `credentials_configured` is `true`
**And** the credentials are NOT returned in any subsequent GET response

---

### A-2a: provider and external_id are immutable

**Given** account X exists with `provider=aws, external_id=123456789012`
**When** I PUT `/api/accounts/X.id` with `{ "provider": "azure" }` in the body
**Then** response is `400` with an error describing that `provider` is immutable
**And** account X is unchanged

---

### A-3: Duplicate external_id is rejected

**Given** account with `provider=aws, external_id=123456789012` exists
**When** I POST another account with the same `(provider, external_id)`
**Then** the response is `409 Conflict`

---

### A-4: Bastion chain not allowed

**Given** account A has `aws_auth_mode = 'bastion'`
**When** I create account B with `aws_auth_mode = 'bastion'` and `aws_bastion_id = A.id`
**Then** the response is `400` with error `"bastion chaining is not supported"`

---

### A-5: Delete account cascades correctly

**Given** account X exists with service overrides and purchase history
**When** I DELETE `/api/accounts/X.id`
**Then** response is `204`
**And** `account_credentials` rows for X are deleted
**And** `account_service_overrides` rows for X are deleted
**And** `purchase_history` rows for X retain their data but have `cloud_account_id = NULL`

---

### A-6: Delete bastion account blocked by dependents

**Given** account B is used as bastion by accounts C and D
**When** I DELETE `/api/accounts/B.id`
**Then** the response is `409` with a message listing accounts C and D
**And** account B still exists

---

## B. Credential Management

### B-1: Credentials are never returned

**Given** credentials are stored for account X
**When** I GET `/api/accounts/X.id`
**Then** the response body does NOT contain `access_key_id`, `secret_access_key`, `client_secret`, `private_key`, or any credential value
**And** `credentials_configured: true` is present

---

### B-2: Test credentials — success

**Given** account X has valid credentials stored
**When** I POST `/api/accounts/X.id/test`
**Then** response is `200` with `{ "ok": true, "caller_identity": "..." }`
**And** no credential values appear in the response or server logs

---

### B-3: Test credentials — failure

**Given** account X has invalid/expired credentials
**When** I POST `/api/accounts/X.id/test`
**Then** response is `200` (not `5xx`) with `{ "ok": false, "error": "..." }`
**And** the error message does NOT contain the credential value

---

### B-3a: Test credentials — no credentials configured

**Given** account X exists but has no credentials stored
**When** I POST `/api/accounts/X.id/test`
**Then** response is `200` (not `404`) with `{ "ok": false, "error": "no credentials configured" }`

---

### B-4: Overwrite credentials

**Given** credentials are already stored for account X
**When** I POST `/api/accounts/X.id/credentials` with new credential values
**Then** response is `204`
**And** old encrypted blob is replaced in `account_credentials`
**And** test connectivity returns success with the new credentials

---

## C. Service Overrides

### C-1: Global default is used when no override exists

**Given** global service config: `{ provider: "aws", service: "ec2", term: 3, coverage: 80 }`
**And** account X has no override for `aws/ec2`
**When** recommendations are collected for account X
**Then** the effective config used for EC2 is `term=3, coverage=80`

---

### C-2: Account override takes precedence for set fields

**Given** global: `{ term: 3, coverage: 80 }`
**And** account X has override: `{ term: 1 }` (coverage not set)
**When** recommendations are collected for account X
**Then** the effective config is `term=1, coverage=80` (term overridden, coverage inherited)

---

### C-3: Deleting an override reverts to global

**Given** account X has override `{ term: 1 }` for `aws/ec2`
**When** I DELETE `/api/accounts/X.id/service-overrides/aws/ec2`
**Then** response is `204`
**And** effective config for account X / EC2 reverts to global default

---

## D. Multi-Account Filtering

### D-1: Filter recommendations by account

**Given** accounts A (`123...`) and B (`234...`) have separate recommendations
**When** I GET `/api/recommendations?account_ids=A.id`
**Then** only recommendations from account A are returned
**And** recommendations from account B are absent

---

### D-2: Filter dashboard by multiple accounts

**Given** accounts A, B, C each have savings data
**When** I GET `/api/dashboard/summary?account_ids=A.id,B.id`
**Then** the summary aggregates A and B only
**And** C's data is excluded

---

### D-3: No account filter returns all accounts

**Given** accounts A, B, C exist
**When** I GET `/api/recommendations` (no `account_ids` param)
**Then** recommendations from all three accounts are returned

---

### D-4: Filtering by disabled account returns empty

**Given** account D exists but `enabled = false`
**When** I GET `/api/recommendations` (no filter)
**Then** recommendations from account D are NOT included
(Disabled accounts are excluded from all data collection)

---

## E. Plan Fan-out

### E-1: Plan targeting two accounts creates two execution records

**Given** plan P targets accounts A and B
**When** the plan executes
**Then** two rows are inserted in `purchase_executions`: one with `cloud_account_id = A.id`, one with `cloud_account_id = B.id`
**And** both executions proceed in parallel (goroutines; verified via execution timestamps being close)

---

### E-2: One account failure does not fail others

**Given** plan P targets accounts A (valid credentials) and B (invalid credentials)
**When** the plan executes
**Then** account A's execution completes successfully
**And** account B's execution is marked `failed` with an error message
**And** account A's execution record is unaffected by B's failure

---

### E-3: Plan with empty account list targets all provider accounts

**Given** plan P has services with keys like `"aws:ec2"` (provider derived at runtime as `aws`) and no entries in `plan_accounts`
**And** three AWS accounts are enabled (A, B, C)
**When** the plan executes
**Then** three execution records are created (one per account)

---

### E-4: Plan filtered to provider — only that provider's accounts run

**Given** plan P has services with keys like `"aws:ec2"` (provider derived as `aws`)
**And** accounts A (AWS), B (AWS), C (Azure) are enabled
**When** `PUT /api/plans/P.id/accounts` is called with `[A.id, B.id, C.id]`
**Then** the API returns `400` because C is not an AWS account
**And** no accounts are associated

---

## F. Org Discovery

### F-1: Discovery creates accounts for new members

**Given** org root account R is configured with valid credentials
**And** Organizations has member accounts M1, M2, M3
**And** M1 already exists in `cloud_accounts`
**When** I POST `/api/accounts/discover-org` with `org_root_account_id = R.id`
**Then** response is `200` with `{ "discovered": 3, "created": 2, "skipped": 1 }`
**And** M2 and M3 are created with `enabled = false`
**And** M1 is untouched (skipped)

---

### F-2: Non-admin cannot trigger discovery

**Given** user has role `user` (not `admin`)
**When** I POST `/api/accounts/discover-org`
**Then** response is `403 Forbidden`

---

### F-3: Discovery from non-org-root is rejected

**Given** account X has `aws_is_org_root = false`
**When** I POST `/api/accounts/discover-org` with `org_root_account_id = X.id`
**Then** response is `400` with a descriptive error

---

## G. Frontend — Settings UI

### G-1: Account appears in list after creation

**Given** the Settings tab is open, AWS provider enabled
**When** I click "+ Add Account" and fill in valid details
**And** I click "Save Account"
**Then** the new account appears in the `#aws-account-list` without page reload
**And** the row shows the account name, Account ID, and auth mode badge

---

### G-2: Credentials status updates after saving credentials

**Given** account X shows "⚠ No credentials"
**When** I click "Credentials" and save valid credentials
**Then** the row updates to "✓ Credentials set"

---

### G-3: Test button shows inline success/error

**Given** account X has credentials configured
**When** I click "Test"
**Then** a non-blocking notification appears: "✓ Connected as arn:aws:iam::..." or "✗ Test failed: [reason]"
**And** the page does not navigate away

---

### G-4: Overrides panel expands inline

**Given** account X is shown in the account list
**When** I click "Overrides"
**Then** a panel expands below the account row showing service rows
**And** rows with active overrides show "(override)" badge
**And** rows without overrides show "(global)" badge

---

## H. Frontend — Filtering

### H-1: Account filter is populated on tab load

**Given** three AWS accounts are configured
**When** I navigate to the Recommendations tab
**Then** the `#recommendations-account-filter` select contains "All Accounts" plus the three account names

---

### H-2: Provider filter change updates account list

**Given** the provider filter is set to "All"
**And** the account filter shows AWS + Azure accounts
**When** I change the provider filter to "AWS"
**Then** the account filter is repopulated with AWS accounts only

---

### H-3: Multi-account selection is passed to API

**Given** accounts A and B are selected in the account filter
**When** recommendations reload
**Then** the API request includes `account_ids=A.id,B.id`

---

## I. Backward Compatibility

### I-1: Existing purchase history is unaffected

**Given** purchase history records exist with `cloud_account_id = NULL` (pre-migration)
**When** I GET `/api/history` with no filters
**Then** old records are included in the response
**And** their `cloud_account_id` field is absent or null

---

### I-2: Existing config endpoints still work

**Given** multi-account feature is deployed
**When** I GET `/api/config`
**Then** the response is identical to pre-deployment (no breaking changes to existing config shape)

---

### I-3: Single-account workflow unchanged

**Given** only one AWS account is configured
**When** using any existing feature (recommendations, history, plans)
**Then** the behavior is identical to pre-deployment with no account filter applied
