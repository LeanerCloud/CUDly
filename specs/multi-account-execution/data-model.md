<!-- markdownlint-disable MD040 MD060 -->
# Data Model

## Overview

Four new tables are introduced. Four existing tables gain a nullable FK column (`cloud_account_id`) for forward-compatible account attribution. All changes are backward-compatible: existing rows keep working with `cloud_account_id = NULL` (interpreted as "legacy / pre-migration record").

Migration file: `internal/database/postgres/migrations/000011_cloud_accounts.up.sql`

---

## New Tables

### `cloud_accounts`

Central registry for every managed cloud account/subscription/project.

```sql
CREATE TABLE cloud_accounts (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- Display / metadata
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    contact_email   VARCHAR(255),
    enabled         BOOLEAN NOT NULL DEFAULT true,

    -- Provider identification
    provider        VARCHAR(32) NOT NULL
                    CHECK (provider IN ('aws', 'azure', 'gcp')),
    external_id     VARCHAR(255) NOT NULL,
    -- external_id meaning per provider:
    --   aws:   12-digit AWS account ID  (e.g. "123456789012")
    --   azure: Azure subscription UUID  (e.g. "xxxxxxxx-xxxx-...")
    --   gcp:   GCP project ID           (e.g. "my-project-123")

    -- ── AWS-specific ─────────────────────────────────────────
    aws_auth_mode   VARCHAR(32)
                    CHECK (aws_auth_mode IN ('access_keys', 'role_arn', 'bastion')),
    aws_role_arn    VARCHAR(512),        -- for role_arn and bastion modes
    aws_external_id VARCHAR(255),        -- ExternalId for STS AssumeRole (optional)
    aws_bastion_id  UUID                 -- for bastion mode: FK to the hub account
                    REFERENCES cloud_accounts(id) ON DELETE SET NULL,
    aws_is_org_root BOOLEAN NOT NULL DEFAULT false,
    -- When true: CUDly treats this as an AWS Organizations management account
    -- and can discover member accounts via the Organizations API.

    -- ── Azure-specific ───────────────────────────────────────
    azure_subscription_id   VARCHAR(36),
    azure_tenant_id         VARCHAR(36),
    azure_client_id         VARCHAR(36),    -- non-secret; client_secret stored in account_credentials

    -- ── GCP-specific ─────────────────────────────────────────
    gcp_project_id      VARCHAR(255),
    gcp_client_email    VARCHAR(255),       -- non-secret; private_key stored in account_credentials

    -- Audit
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,

    UNIQUE(provider, external_id)
);

CREATE TRIGGER update_cloud_accounts_updated_at
    BEFORE UPDATE ON cloud_accounts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_cloud_accounts_provider ON cloud_accounts(provider) WHERE enabled = true;
CREATE INDEX idx_cloud_accounts_org_root ON cloud_accounts(aws_is_org_root) WHERE aws_is_org_root = true;
```

**Constraints:**

- `aws_bastion_id` must not reference an account that itself has `aws_auth_mode = 'bastion'` — enforced at application layer (prevents chaining).
- When `aws_auth_mode = 'role_arn'` or `'bastion'`, `aws_role_arn` must be non-null — validated on write.
- When `provider = 'azure'`, `azure_subscription_id` and `azure_tenant_id` must be non-null — validated on write.

**`external_id` vs provider-specific ID fields:**
`external_id` is the provider-agnostic unique key used for deduplication (`UNIQUE(provider, external_id)`). For Azure and GCP, `external_id` duplicates the provider-specific field:

- Azure: `external_id` = `azure_subscription_id` (both are the subscription UUID). On create/update, if both are provided they must match; if only `external_id` is provided, the handler sets `azure_subscription_id = external_id` automatically.
- GCP: `external_id` = `gcp_project_id` (both are the project ID string). Same derivation rule applies.
- AWS: `external_id` is the 12-digit AWS account ID; there is no separate `aws_account_id` column (not needed — the account ID is not provider-specific secret data).

---

### `account_credentials`

Stores encrypted credential material. Never returned via GET responses.

```sql
CREATE TABLE account_credentials (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id      UUID NOT NULL
                    REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    credential_type VARCHAR(32) NOT NULL,
    -- Allowed values and the JSON structure they encrypt:
    --
    --  'aws_access_keys'       → {"access_key_id": "...", "secret_access_key": "..."}
    --  'azure_client_secret'   → {"client_secret": "..."}
    --  'gcp_service_account'   → full service account JSON blob
    --
    encrypted_blob  TEXT NOT NULL,
    -- AES-256-GCM ciphertext, base64url-encoded.
    -- Format: <base64(nonce)>.<base64(ciphertext)>
    -- Key source: loaded by KeyFromEnv() — see iac.md and security.md for full load order
    --   (prod: CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN → Secrets Manager;
    --    dev:  CREDENTIAL_ENCRYPTION_KEY env var directly)

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(account_id, credential_type)
);

CREATE TRIGGER update_account_credentials_updated_at
    BEFORE UPDATE ON account_credentials
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
```

**Note**: `account_credentials` has no SELECT-returning API endpoint. The application only reads it server-side to resolve credentials before making cloud API calls.

---

### `account_service_overrides`

Per-account sparse overrides for service configuration. `NULL` in any column means "inherit from global `service_configs`".

```sql
CREATE TABLE account_service_overrides (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id      UUID NOT NULL
                    REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    provider        VARCHAR(32) NOT NULL,
    service         VARCHAR(64) NOT NULL,

    -- All override fields are nullable; NULL = inherit global default
    enabled         BOOLEAN,
    term            INTEGER,                -- years: 1 or 3
    payment         VARCHAR(32),            -- no-upfront / partial-upfront / all-upfront
    coverage        DECIMAL(5,2),           -- 0–100
    ramp_schedule   VARCHAR(32),
    include_engines TEXT[],
    exclude_engines TEXT[],
    include_regions TEXT[],
    exclude_regions TEXT[],
    include_types   TEXT[],
    exclude_types   TEXT[],

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(account_id, provider, service)
);

CREATE TRIGGER update_account_service_overrides_updated_at
    BEFORE UPDATE ON account_service_overrides
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_aso_account ON account_service_overrides(account_id);
```

**Cascade resolution logic** (implemented in `internal/config/resolver.go`):

```
effectiveConfig(account, provider, service):
  global = service_configs WHERE provider=? AND service=?
  override = account_service_overrides WHERE account_id=? AND provider=? AND service=?
  IF override IS NULL: return global
  RETURN {
    enabled:         COALESCE(override.enabled,         global.enabled),
    term:            COALESCE(override.term,            global.term),
    payment:         COALESCE(override.payment,         global.payment),
    coverage:        COALESCE(override.coverage,        global.coverage),
    ramp_schedule:   COALESCE(override.ramp_schedule,   global.ramp_schedule),
    include_engines: COALESCE(override.include_engines, global.include_engines),
    ... etc
  }
```

---

### `plan_accounts`

Many-to-many join: which cloud accounts a purchase plan targets.

```sql
CREATE TABLE plan_accounts (
    plan_id     UUID NOT NULL REFERENCES purchase_plans(id) ON DELETE CASCADE,
    account_id  UUID NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    PRIMARY KEY (plan_id, account_id)
);

CREATE INDEX idx_plan_accounts_account ON plan_accounts(account_id);
```

**Behaviour when the list is empty**: A plan with no rows in `plan_accounts` targets **all enabled accounts** for its configured provider. This is the default for backward compatibility with plans created before multi-account support.

---

## Existing Table Modifications

The following tables gain a nullable `cloud_account_id` column so that new records can be attributed to a specific managed account. Existing rows default to `NULL` (no attribution required).

### `purchase_history`

```sql
ALTER TABLE purchase_history
    ADD COLUMN cloud_account_id UUID REFERENCES cloud_accounts(id) ON DELETE SET NULL;

CREATE INDEX idx_purchase_history_cloud_account
    ON purchase_history(cloud_account_id) WHERE cloud_account_id IS NOT NULL;
```

### `purchase_executions`

```sql
ALTER TABLE purchase_executions
    ADD COLUMN cloud_account_id UUID REFERENCES cloud_accounts(id) ON DELETE SET NULL;

CREATE INDEX idx_purchase_executions_cloud_account
    ON purchase_executions(cloud_account_id) WHERE cloud_account_id IS NOT NULL;
```

### `savings_snapshots`

```sql
ALTER TABLE savings_snapshots
    ADD COLUMN cloud_account_id UUID REFERENCES cloud_accounts(id) ON DELETE SET NULL;
```

### `ri_exchange_history`

```sql
ALTER TABLE ri_exchange_history
    ADD COLUMN cloud_account_id UUID REFERENCES cloud_accounts(id) ON DELETE SET NULL;
```

**Note on `account_id VARCHAR(20)`**: These columns are kept as-is. They store the raw cloud-provider account identifier (AWS 12-digit ID, Azure subscription GUID, etc.) and are still populated for all new records. `cloud_account_id` is the normalized FK into CUDly's own registry.

---

## Migration File Structure

```
internal/database/postgres/migrations/
  000011_cloud_accounts.up.sql    ← creates the 4 new tables, adds FK columns, creates indexes
  000011_cloud_accounts.down.sql  ← drops indexes, removes FK columns, drops tables in reverse order
```

Down migration order (reverse dependency):

1. `DROP TABLE plan_accounts`
2. `DROP TABLE account_service_overrides`
3. `DROP TABLE account_credentials`
4. Remove FK columns from `purchase_history`, `purchase_executions`, `savings_snapshots`, `ri_exchange_history`
5. `DROP TABLE cloud_accounts`

---

## Go Type Additions (`internal/config/types.go`)

```go
// CloudAccount represents a single managed cloud account/subscription/project.
type CloudAccount struct {
    ID           string    `json:"id"`
    Name         string    `json:"name"`
    Description  string    `json:"description,omitempty"`
    ContactEmail string    `json:"contact_email,omitempty"`
    Enabled      bool      `json:"enabled"`
    Provider     string    `json:"provider"`
    ExternalID   string    `json:"external_id"`

    // AWS
    AWSAuthMode    string  `json:"aws_auth_mode,omitempty"`
    AWSRoleARN     string  `json:"aws_role_arn,omitempty"`
    AWSExternalID  string  `json:"aws_external_id,omitempty"`
    AWSBastionID   string  `json:"aws_bastion_id,omitempty"`
    AWSIsOrgRoot   bool    `json:"aws_is_org_root,omitempty"`

    // Azure
    AzureSubscriptionID string `json:"azure_subscription_id,omitempty"`
    AzureTenantID       string `json:"azure_tenant_id,omitempty"`
    AzureClientID       string `json:"azure_client_id,omitempty"`

    // GCP
    GCPProjectID    string `json:"gcp_project_id,omitempty"`
    GCPClientEmail  string `json:"gcp_client_email,omitempty"`

    // Derived (not stored)
    CredentialsConfigured bool   `json:"credentials_configured"`
    BastionAccountName    string `json:"bastion_account_name,omitempty"`

    CreatedAt time.Time  `json:"created_at"`
    UpdatedAt time.Time  `json:"updated_at"`
    CreatedBy string     `json:"created_by,omitempty"`
}

// CloudAccountFilter for ListCloudAccounts queries.
type CloudAccountFilter struct {
    Provider  *string
    Enabled   *bool
    Search    string  // substring match on name or external_id
    BastionID *string // return accounts whose aws_bastion_id = *BastionID; used by delete handler
}

// AccountServiceOverride is a sparse per-account override on top of the global ServiceConfig.
// Nil pointer fields inherit the global value.
type AccountServiceOverride struct {
    ID           string    `json:"id"`
    AccountID    string    `json:"account_id"`
    Provider     string    `json:"provider"`
    Service      string    `json:"service"`
    Enabled      *bool     `json:"enabled,omitempty"`
    Term         *int      `json:"term,omitempty"`
    Payment      *string   `json:"payment,omitempty"`
    Coverage     *float64  `json:"coverage,omitempty"`
    RampSchedule *string   `json:"ramp_schedule,omitempty"`
    IncludeEngines []string `json:"include_engines,omitempty"`
    ExcludeEngines []string `json:"exclude_engines,omitempty"`
    IncludeRegions []string `json:"include_regions,omitempty"`
    ExcludeRegions []string `json:"exclude_regions,omitempty"`
    IncludeTypes   []string `json:"include_types,omitempty"`
    ExcludeTypes   []string `json:"exclude_types,omitempty"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```
