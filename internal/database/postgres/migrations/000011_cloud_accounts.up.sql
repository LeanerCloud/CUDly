-- ==========================================
-- CLOUD ACCOUNTS
-- ==========================================

CREATE TABLE cloud_accounts (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    contact_email   VARCHAR(255),
    enabled         BOOLEAN NOT NULL DEFAULT true,

    provider        VARCHAR(32) NOT NULL
                    CHECK (provider IN ('aws', 'azure', 'gcp')),
    external_id     VARCHAR(255) NOT NULL,

    -- AWS-specific
    aws_auth_mode   VARCHAR(32)
                    CHECK (aws_auth_mode IN ('access_keys', 'role_arn', 'bastion')),
    aws_role_arn    VARCHAR(512),
    aws_external_id VARCHAR(255),
    aws_bastion_id  UUID REFERENCES cloud_accounts(id) ON DELETE SET NULL,
    aws_is_org_root BOOLEAN NOT NULL DEFAULT false,

    -- Azure-specific
    azure_subscription_id   VARCHAR(36),
    azure_tenant_id         VARCHAR(36),
    azure_client_id         VARCHAR(36),

    -- GCP-specific
    gcp_project_id      VARCHAR(255),
    gcp_client_email    VARCHAR(255),

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

-- ==========================================
-- ACCOUNT CREDENTIALS
-- ==========================================

CREATE TABLE account_credentials (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id      UUID NOT NULL
                    REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    credential_type VARCHAR(32) NOT NULL,
    encrypted_blob  TEXT NOT NULL,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(account_id, credential_type)
);

CREATE TRIGGER update_account_credentials_updated_at
    BEFORE UPDATE ON account_credentials
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- ==========================================
-- ACCOUNT SERVICE OVERRIDES
-- ==========================================

CREATE TABLE account_service_overrides (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id      UUID NOT NULL
                    REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    provider        VARCHAR(32) NOT NULL,
    service         VARCHAR(64) NOT NULL,

    enabled         BOOLEAN,
    term            INTEGER,
    payment         VARCHAR(32),
    coverage        DECIMAL(5,2),
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

-- ==========================================
-- PLAN ACCOUNTS (many-to-many)
-- ==========================================

CREATE TABLE plan_accounts (
    plan_id     UUID NOT NULL REFERENCES purchase_plans(id) ON DELETE CASCADE,
    account_id  UUID NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    PRIMARY KEY (plan_id, account_id)
);

CREATE INDEX idx_plan_accounts_account ON plan_accounts(account_id);

-- ==========================================
-- ADD cloud_account_id TO EXISTING TABLES
-- ==========================================

ALTER TABLE purchase_history
    ADD COLUMN cloud_account_id UUID REFERENCES cloud_accounts(id) ON DELETE SET NULL;

CREATE INDEX idx_purchase_history_cloud_account
    ON purchase_history(cloud_account_id) WHERE cloud_account_id IS NOT NULL;

ALTER TABLE purchase_executions
    ADD COLUMN cloud_account_id UUID REFERENCES cloud_accounts(id) ON DELETE SET NULL;

CREATE INDEX idx_purchase_executions_cloud_account
    ON purchase_executions(cloud_account_id) WHERE cloud_account_id IS NOT NULL;

ALTER TABLE savings_snapshots
    ADD COLUMN cloud_account_id UUID REFERENCES cloud_accounts(id) ON DELETE SET NULL;

ALTER TABLE ri_exchange_history
    ADD COLUMN cloud_account_id UUID REFERENCES cloud_accounts(id) ON DELETE SET NULL;
