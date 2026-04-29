<!-- markdownlint-disable MD040 MD060 -->
# API Design

All new endpoints are under the existing `/api` prefix and follow the same auth middleware, error response format, and permission model as current endpoints.

---

## Cloud Accounts Endpoints

### `GET /api/accounts`

List all configured cloud accounts. Returns metadata only — no credential material.

**Query params:**

| Param | Type | Description |
|-------|------|-------------|
| `provider` | string | Filter by provider: `aws`, `azure`, `gcp` |
| `enabled` | bool | Filter by enabled status |
| `search` | string | Substring match on `name` or `external_id` |

**Response `200`:**

```json
{
  "accounts": [
    {
      "id": "uuid",
      "name": "Prod-US",
      "description": "Production US account",
      "contact_email": "infra@example.com",
      "enabled": true,
      "provider": "aws",
      "external_id": "123456789012",
      "aws_auth_mode": "role_arn",
      "aws_role_arn": "arn:aws:iam::123456789012:role/CUDly",
      "aws_is_org_root": false,
      "credentials_configured": true,
      "created_at": "2026-01-01T00:00:00Z",
      "updated_at": "2026-01-01T00:00:00Z"
    }
  ]
}
```

---

### `POST /api/accounts`

Create a new cloud account. Credentials can be included inline and are stored encrypted.

**Request body:**

```json
{
  "name": "Prod-US",
  "description": "Production US account",
  "contact_email": "infra@example.com",
  "enabled": true,
  "provider": "aws",
  "external_id": "123456789012",
  "aws_auth_mode": "role_arn",
  "aws_role_arn": "arn:aws:iam::123456789012:role/CUDly",
  "aws_external_id": "cudly-external-123",

  "credentials": {
    "access_key_id": "AKIA...",         // only for aws_auth_mode = access_keys
    "secret_access_key": "..."          // only for aws_auth_mode = access_keys
  }
}
```

For Azure:

```json
{
  "provider": "azure",
  "name": "Azure Prod",
  "external_id": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "azure_subscription_id": "xxxxxxxx-...",
  "azure_tenant_id": "yyyyyyyy-...",
  "azure_client_id": "zzzzzzzz-...",
  "credentials": {
    "client_secret": "..."
  }
}
```

For GCP:

```json
{
  "provider": "gcp",
  "name": "GCP Analytics",
  "external_id": "my-project-123",
  "gcp_project_id": "my-project-123",
  "credentials": {
    "service_account_json": "{ \"type\": \"service_account\", ... }"
  }
}
```

**Response `201`:** Created account object (same shape as GET, no credential material).

**Validation errors `400`:**

- `provider` not in `{aws, azure, gcp}`
- `aws_auth_mode = role_arn` but no `aws_role_arn` provided
- `aws_auth_mode = bastion` but no `aws_bastion_id` or bastion account not found
- Duplicate `(provider, external_id)` → `409 Conflict`

---

### `GET /api/accounts/:id`

Get a single account. Never returns credential material.

**Response `200`:** Same shape as list item.
**Response `404`:** Account not found.

---

### `PUT /api/accounts/:id`

Update account metadata. Does not touch credentials.

**Request body:** Same fields as POST, minus `credentials`, `provider`, and `external_id`. `provider` and `external_id` are immutable after creation; the server returns `400` if either is included in the PUT body.
**Response `200`:** Updated account.

---

### `DELETE /api/accounts/:id`

Delete account and cascade-delete its credentials and service overrides. Purchase history records retain the account name as a string but FK becomes `NULL`.

**Response `204`:** No content.
**Response `409`:** Account is referenced as bastion by other accounts (caller must re-assign those first).

On delete, CUDly applies `ON DELETE CASCADE` to `account_credentials` and `account_service_overrides`, and `ON DELETE SET NULL` to `cloud_account_id` in `purchase_history`, `purchase_executions`, `savings_snapshots`, and `ri_exchange_history`. The `account_id VARCHAR(20)` column (cloud provider's raw account ID) is retained unchanged in those tables.

---

### `POST /api/accounts/:id/credentials`

Replace credentials for an account. Write-only — returns no credential data.

**Request body:**

AWS access keys:

```json
{
  "type": "aws_access_keys",
  "access_key_id": "AKIA...",
  "secret_access_key": "..."
}
```

Azure client secret:

```json
{
  "type": "azure_client_secret",
  "client_secret": "..."
}
```

GCP service account:

```json
{
  "type": "gcp_service_account",
  "service_account_json": "{ \"type\": \"service_account\", ... }"
}
```

**Response `204`:** No content.

---

### `POST /api/accounts/:id/test`

Test connectivity using the account's stored credentials. Makes a minimal read-only API call:

- AWS: `sts:GetCallerIdentity`
- Azure: `GET /subscriptions/{id}` (read subscription metadata)
- GCP: `resourcemanager.projects.get`

**Response `200`:**

```json
{
  "ok": true,
  "caller_identity": "arn:aws:iam::123456789012:role/CUDly"
}
```

**Response `200`** (failure — connection error is not an HTTP error):

```json
{
  "ok": false,
  "error": "NoCredentialProviders: no valid providers in chain"
}
```

**Response `200`** (no credentials stored — not a 404; credentials absence is a soft failure):

```json
{ "ok": false, "error": "no credentials configured" }
```

**Response `404`:** Account not found.

---

### `GET /api/accounts/:id/service-overrides`

List all service overrides for an account.

**Response `200`:**

```json
{
  "overrides": [
    {
      "id": "uuid",
      "account_id": "uuid",
      "provider": "aws",
      "service": "ec2",
      "term": 1,
      "coverage": 60.0
    }
  ]
}
```

Fields not overridden are omitted from the response (callers should merge with global defaults client-side or use the resolved-config endpoint).

---

### `PUT /api/accounts/:id/service-overrides/:provider/:service`

Create or replace a service override for an account. This is a **full-replace** operation: all override columns are set to the values provided in the request body; any field absent from the body is written as `NULL` (meaning "inherit from global default"). To update one field without resetting others, the caller must include the full desired override in the body.

**Request body (all fields optional):**

```json
{
  "enabled": true,
  "term": 1,
  "payment": "all-upfront",
  "coverage": 60.0,
  "ramp_schedule": "immediate",
  "include_regions": ["us-east-1", "eu-west-1"],
  "exclude_engines": ["aurora-mysql"]
}
```

**Response `200`:** Saved override (sparse — only explicitly-set fields returned).

---

### `DELETE /api/accounts/:id/service-overrides/:provider/:service`

Delete override, reverting to global default.

**Response `204`:** No content.

---

### `POST /api/accounts/discover-org`

Trigger AWS Organizations member account discovery using a configured org-root account. Idempotent: existing accounts matching discovered external IDs are skipped; new ones are created with `enabled = false` pending user review.

**Request body:**

```json
{
  "org_root_account_id": "uuid"
}
```

**Response `200`:**

```json
{
  "discovered": 14,
  "created": 3,
  "skipped": 11,
  "accounts": [
    { "name": "Member-Acct-A", "external_id": "111122223333", "created": true },
    ...
  ]
}
```

**Response `400`:** `aws_is_org_root = false` for the referenced account (account exists but is not an org root).
**Response `404`:** `org_root_account_id` references an account that does not exist.
**Response `403`:** Caller is not an admin.

---

## Plan ↔ Account Association

### `GET /api/plans/:id/accounts`

List accounts associated with a plan.

**Required permission:** `view` on `accounts`.

**Response `200`:**

```json
{
  "account_ids": ["uuid1", "uuid2"],
  "accounts": [
    { "id": "uuid1", "name": "Prod-US", "provider": "aws", "external_id": "123..." }
  ]
}
```

---

### `PUT /api/plans/:id/accounts`

Replace the full account list for a plan. Send an empty array to target all accounts for the plan's provider.

**Required permission:** `update` on `accounts`.

**Request body:**

```json
{
  "account_ids": ["uuid1", "uuid2"]
}
```

**Validation:** All referenced `account_ids` must exist and their `provider` must match the plan's provider (derived from the plan's `services` map keys — e.g. a key of `"aws:ec2"` means provider `aws`).
**Response `200`:** Same as GET response above.

---

## Modifications to Existing Endpoints

The following endpoints gain an optional `account_ids` query parameter.

**`account_ids`**: comma-separated list of `cloud_accounts.id` UUIDs. Omitting the param (or passing `account_ids=`) returns data for **all** accounts.

### Dashboard

```
GET /api/dashboard/summary?provider=aws&account_ids=uuid1,uuid2
GET /api/dashboard/upcoming?account_ids=uuid1
```

### Recommendations

```
GET /api/recommendations?provider=aws&account_ids=uuid1&service=ec2&region=us-east-1
```

### History

```
GET /api/history?provider=aws&account_ids=uuid1,uuid2&start=2026-01-01&end=2026-03-31
GET /api/history/analytics?account_ids=uuid1&interval=monthly
GET /api/history/breakdown?dimension=service&account_ids=uuid1
```

---

## Error Response Format

Unchanged from current pattern — a single `error` string key, no code field:

```json
{
  "error": "account not found"
}
```

Standard HTTP status codes apply: `400` validation, `401` unauthenticated, `403` unauthorized, `404` not found, `409` conflict, `500` internal.

Error types are signalled via `NewClientError(code, message)` (defined in `internal/api/handler_router.go`). For example, `NewClientError(404, "account not found")` produces `{"error": "account not found"}` with HTTP 404. Any unhandled Go error maps to `500 {"error": "Internal server error"}`.

---

## Permissions

New resource type `accounts` added to the permission model:

| Action | Resource | Description |
|--------|----------|-------------|
| `view` | `accounts` | List and GET accounts |
| `create` | `accounts` | Create new accounts |
| `update` | `accounts` | Edit metadata and service overrides |
| `delete` | `accounts` | Delete accounts |
| `manage_credentials` | `accounts` | Write credentials to an account |
| `test_credentials` | `accounts` | Test account connectivity |
| `discover_org` | `accounts` | Trigger org discovery (admin only) |

The `allowed_accounts` constraint on existing permissions continues to work: a user with `view` on `recommendations` and `allowed_accounts: ["uuid1"]` only sees recommendations from account `uuid1`.
