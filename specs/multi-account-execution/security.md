<!-- markdownlint-disable MD040 MD060 -->
# Security Model

## Credential Storage

### Encryption

All sensitive credential values (AWS secret access keys, Azure client secrets, GCP service account private keys) are stored encrypted in the `account_credentials` table.

**Algorithm**: AES-256-GCM (authenticated encryption — provides both confidentiality and integrity).

**Format**: `<base64url(nonce)>.<base64url(ciphertext)>` where:

- `nonce` is a randomly generated 12-byte value (unique per encryption operation)
- `ciphertext` includes the GCM authentication tag appended

**Key management**:

| Environment | Key Source | Behaviour |
|-------------|-----------|-----------|
| Production (AWS) | `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN` env var → AWS Secrets Manager `GetSecretValue` | ARN set via Terraform `compute.tf`; secret created in `secrets.tf`. See `iac.md`. |
| Staging/CI (AWS) | Same SM-based pattern with a staging-specific secret | Separate Secrets Manager secret per environment |
| Non-AWS / local dev | `CREDENTIAL_ENCRYPTION_KEY` env var (64-char hex = 32 bytes) | Used directly when SM ARN is absent; bypasses Secrets Manager for local ergonomics |
| Fallback (dev only) | Neither env var set | Hardcoded dev-only key; logs `WARN: using insecure dev credential key — do not use in production` |

Load order in `internal/credentials/cipher.go:KeyFromEnv()`:

1. If `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN` is set → call `secretsmanager.GetSecretValue(arn)`, use returned string as hex key (cached for Lambda lifetime)
2. Else if `CREDENTIAL_ENCRYPTION_KEY` is set → use directly
3. Else → hardcoded dev key + `WARN` log

**Key rotation**: To rotate the encryption key, a `RotateCredentials(oldKey, newKey)` utility function will decrypt all blobs with `oldKey` and re-encrypt with `newKey`. This is a maintenance operation requiring downtime or a migration job.

---

## Credential Access Patterns

### Write-only API

Credential material is never returned in any API response:

- `GET /api/accounts/:id` returns `credentials_configured: bool` only
- `GET /api/accounts` list — same
- `POST /api/accounts/:id/credentials` — returns `204 No Content`

The only code path that decrypts credentials is the server-side resolution in `internal/credentials/resolver.go`, called immediately before making a cloud API call and not persisted in memory beyond that call.

### Test endpoint constraints

`POST /api/accounts/:id/test`:

- Resolves credentials in memory for the duration of the test call
- Makes one lightweight, read-only cloud API call
- Logs the result (success/failure) but **never logs credential values or session tokens**
- Returns `{ ok: bool, error?: string }` — never the credential itself
- **Rate limiting**: Limited to 10 calls per account per minute (enforced server-side via a sliding window counter in the handler). Returns `429 Too Many Requests` when exceeded. This prevents the endpoint from being used to probe or brute-force credential validity at scale.

### Logging policy

- Credential fields are never included in structured log output
- `fmt.Errorf` wrapping credential operations must not include credential values in error messages
- The `AWSCredentials`, `AzureCredentials`, and GCP JSON structs must implement `String() string` returning `"[REDACTED]"` to prevent accidental logging via `%v`

---

## Bastion Chain Depth Limit

To prevent misconfiguration creating circular or deep credential chains:

1. An account with `aws_auth_mode = 'bastion'` cannot itself be set as the `aws_bastion_id` of another bastion account — validated on `POST /api/accounts` and `PUT /api/accounts/:id`.
2. Maximum chain depth: 1 (bastion → target). A bastion account can only use `access_keys` or `role_arn` auth modes, not `bastion`.
3. This is enforced both in the API handler (returns `400` with error `"bastion chaining is not supported"`) and in `resolver.go` (returns an error if attempted at runtime).

---

## Input Validation

### ARN Format Validation

When a user provides `aws_role_arn` (for `role_arn` or `bastion` auth modes), the handler must validate the format before storing:

- Regex: `^arn:aws:iam::\d{12}:role/[\w+=,.@/-]+$`
- Max length: 512 characters
- Returns `400 Bad Request` with a descriptive message if invalid

`aws_external_id`, if provided, must be 2–1224 characters (AWS constraint) and contain only printable ASCII (no control characters).

### GCP Service Account JSON

GCP credentials are a JSON key file. The handler must:

- Enforce a maximum payload size of 64 KB to prevent memory exhaustion
- Validate that the blob is valid JSON before encrypting
- Returns `400 Bad Request` on size or JSON parse failure

### Account ID Format

`external_id` (AWS account ID / Azure subscription ID / GCP project ID):

- AWS: exactly 12 digits (`^\d{12}$`)
- Azure: UUID format (`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
- GCP: 6–30 lowercase alphanumeric + hyphens, must start with a letter (`^[a-z][a-z0-9-]{5,29}$`)

---

## AWS Role Trust Policy Recommendations

When users configure a `role_arn` or bastion target, CUDly validates the ARN format but cannot validate that the trust policy allows assumption. The UI should display a recommended trust policy template for users to apply in their target accounts:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "AWS": "arn:aws:iam::{CUDLY_ACCOUNT_ID}:role/{CUDLY_ROLE}" },
    "Action": "sts:AssumeRole",
    "Condition": {
      "StringEquals": { "sts:ExternalId": "{EXTERNAL_ID}" }
    }
  }]
}
```

The ExternalId condition is strongly recommended (shown in UI) but not enforced (some orgs manage it differently).

---

## Access Control

### Existing permission model extension

The existing `groups.permissions` JSONB array is extended with a new resource type `accounts`:

```json
[
  { "action": "view",               "resource": "accounts" },
  { "action": "create",             "resource": "accounts" },
  { "action": "update",             "resource": "accounts" },
  { "action": "delete",             "resource": "accounts" },
  { "action": "manage_credentials", "resource": "accounts" },
  { "action": "test_credentials",   "resource": "accounts" },
  { "action": "discover_org",       "resource": "accounts" }
]
```

`discover_org` requires `role = 'admin'` in addition to the permission (defence in depth).

### Existing `allowed_accounts` constraint

The existing `Permission.constraints.accounts` field (already in the DB schema) now resolves against `cloud_accounts.id` UUIDs. A user with:

```json
{ "action": "view", "resource": "recommendations", "constraints": { "accounts": ["uuid1"] } }
```

can only see recommendations filtered to `account_ids=uuid1`.

---

## Audit Logging

The following events are logged at `INFO` level with structured fields (no credential values):

| Event | Fields logged |
|-------|--------------|
| Account created | `account_id`, `provider`, `external_id`, `created_by` |
| Account updated | `account_id`, `changed_fields` (list of field names, no values for sensitive fields) |
| Account deleted | `account_id`, `deleted_by` |
| Credentials saved | `account_id`, `credential_type`, `updated_by` |
| Credentials test | `account_id`, `result` (ok/fail), `error` (no credential content) |
| Org discovery | `org_root_account_id`, `discovered`, `created`, `triggered_by` |

---

## Threat Model

| Threat | Mitigation |
|--------|-----------|
| DB compromise exposes all cloud credentials | AES-256-GCM encryption; attacker needs both DB access AND `CREDENTIAL_ENCRYPTION_KEY` |
| Credential exfiltration via API | Write-only credential endpoint; GET responses never include credential material |
| Bastion account misuse grants access to all member accounts | Bastion chain depth limit of 1; ExternalId recommended; IAM role requires explicit trust policy |
| XSS → credential theft | Credentials never rendered in DOM; account names/IDs rendered with `textContent` (not raw HTML) |
| SSRF via "test credentials" | Test endpoint is tightly scoped to known cloud SDK calls; no user-controlled URLs in the call path |
| Insider threat / privilege escalation | `manage_credentials` and `discover_org` are separate permissions; full audit log; admin role required for org discovery |
| Credential logging | All credential structs implement `String() = "[REDACTED]"`; structured log review required during code review |
| Abuse of test endpoint to probe credential validity | Rate limit: 10 calls/account/minute; returns `429` when exceeded |
| Malformed ARN or GCP JSON causes panic/memory issue | ARN regex + length validation; GCP JSON validated + capped at 64 KB before encrypting |
| Encryption key exposure in Lambda logs/env | Key loaded from Secrets Manager (not plaintext env); never logged; `KeyFromEnv()` returns `[]byte`, not string |
