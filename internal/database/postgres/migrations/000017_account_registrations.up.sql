CREATE TABLE account_registrations (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    reference_token VARCHAR(64) NOT NULL UNIQUE,
    status          VARCHAR(16) NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'approved', 'rejected')),

    -- Account identity
    provider        VARCHAR(32) NOT NULL CHECK (provider IN ('aws', 'azure', 'gcp')),
    external_id     VARCHAR(255) NOT NULL,
    account_name    VARCHAR(255) NOT NULL,
    contact_email   VARCHAR(255) NOT NULL,
    description     TEXT,

    -- Provider-specific fields (populated by IaC outputs)
    source_provider     VARCHAR(32) CHECK (source_provider IS NULL OR source_provider IN ('aws', 'azure', 'gcp')),
    aws_role_arn        VARCHAR(2048),
    aws_auth_mode       VARCHAR(64),
    azure_subscription_id VARCHAR(255),
    azure_tenant_id     VARCHAR(255),
    azure_client_id     VARCHAR(255),
    azure_auth_mode     VARCHAR(64),
    gcp_project_id      VARCHAR(255),
    gcp_client_email    VARCHAR(255),
    gcp_auth_mode       VARCHAR(64),

    -- Workflow
    rejection_reason TEXT,
    cloud_account_id UUID REFERENCES cloud_accounts(id) ON DELETE SET NULL,
    reviewed_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Only one pending registration per provider+external_id at a time
CREATE UNIQUE INDEX idx_account_registrations_pending_unique
    ON account_registrations(provider, external_id) WHERE status = 'pending';

CREATE TRIGGER update_account_registrations_updated_at
    BEFORE UPDATE ON account_registrations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_account_registrations_status ON account_registrations(status);
CREATE INDEX idx_account_registrations_token ON account_registrations(reference_token);
