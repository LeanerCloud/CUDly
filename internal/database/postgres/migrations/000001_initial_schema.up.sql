-- Enable necessary extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pg_trgm"; -- For text search

-- ==========================================
-- CONFIGURATION TABLES
-- ==========================================

-- Global configuration table (replaces single DynamoDB item)
CREATE TABLE global_config (
    id INTEGER PRIMARY KEY DEFAULT 1,
    enabled_providers TEXT[] NOT NULL DEFAULT '{}',
    notification_email VARCHAR(255),
    approval_required BOOLEAN NOT NULL DEFAULT true,
    default_term INTEGER NOT NULL DEFAULT 12,
    default_payment VARCHAR(32) NOT NULL DEFAULT 'all-upfront',
    default_coverage DECIMAL(5,2) NOT NULL DEFAULT 80.00,
    default_ramp_schedule VARCHAR(32) NOT NULL DEFAULT 'immediate',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT single_row CHECK (id = 1)
);

-- Service-specific configuration
CREATE TABLE service_configs (
    id SERIAL PRIMARY KEY,
    provider VARCHAR(32) NOT NULL,
    service VARCHAR(64) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    term INTEGER NOT NULL DEFAULT 12,
    payment VARCHAR(32) NOT NULL DEFAULT 'all-upfront',
    coverage DECIMAL(5,2) NOT NULL DEFAULT 80.00,
    ramp_schedule VARCHAR(32) NOT NULL DEFAULT 'immediate',
    include_engines TEXT[],
    exclude_engines TEXT[],
    include_regions TEXT[],
    exclude_regions TEXT[],
    include_types TEXT[],
    exclude_types TEXT[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(provider, service)
);

-- ==========================================
-- PURCHASE PLAN TABLES
-- ==========================================

-- Purchase plans (automated purchasing schedules)
CREATE TABLE purchase_plans (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT false,
    auto_purchase BOOLEAN NOT NULL DEFAULT false,
    notification_days_before INTEGER NOT NULL DEFAULT 7,

    -- Services configuration stored as JSONB
    -- Structure: {"aws:rds": {...}, "aws:elasticache": {...}}
    services JSONB NOT NULL DEFAULT '{}',

    -- Ramp schedule stored as JSONB
    -- Structure: {"type": "weekly", "percent_per_step": 25, ...}
    ramp_schedule JSONB NOT NULL DEFAULT '{"type": "immediate", "percent_per_step": 100, "total_steps": 1}',

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_execution_date TIMESTAMPTZ,
    last_execution_date TIMESTAMPTZ,
    last_notification_sent TIMESTAMPTZ
);

-- Purchase executions (individual execution records)
CREATE TABLE purchase_executions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    plan_id UUID NOT NULL REFERENCES purchase_plans(id) ON DELETE CASCADE,
    execution_id UUID NOT NULL DEFAULT uuid_generate_v4(),
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    step_number INTEGER NOT NULL DEFAULT 1,
    scheduled_date TIMESTAMPTZ NOT NULL,
    notification_sent TIMESTAMPTZ,
    approval_token VARCHAR(255),

    -- Recommendations stored as JSONB array
    recommendations JSONB NOT NULL DEFAULT '[]',

    total_upfront_cost DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    estimated_savings DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    completed_at TIMESTAMPTZ,
    error TEXT,

    -- TTL for cleanup (expires_at replaces DynamoDB TTL)
    expires_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT valid_status CHECK (status IN ('pending', 'notified', 'approved', 'cancelled', 'completed', 'failed'))
);

-- Purchase history (audit trail of completed purchases)
CREATE TABLE purchase_history (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id VARCHAR(20) NOT NULL,
    purchase_id VARCHAR(255) NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    provider VARCHAR(32) NOT NULL,
    service VARCHAR(64) NOT NULL,
    region VARCHAR(32) NOT NULL,
    resource_type VARCHAR(64) NOT NULL,
    count INTEGER NOT NULL DEFAULT 1,
    term INTEGER NOT NULL,
    payment VARCHAR(32) NOT NULL,
    upfront_cost DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    monthly_cost DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    estimated_savings DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    plan_id UUID REFERENCES purchase_plans(id) ON DELETE SET NULL,
    plan_name VARCHAR(255),
    ramp_step INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ==========================================
-- AUTHENTICATION & AUTHORIZATION TABLES
-- ==========================================

-- Users
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    salt VARCHAR(255) NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'user',
    group_ids UUID[] DEFAULT '{}',
    active BOOLEAN NOT NULL DEFAULT true,

    -- MFA fields
    mfa_enabled BOOLEAN NOT NULL DEFAULT false,
    mfa_secret VARCHAR(255),

    -- Password reset
    password_reset_token VARCHAR(255),
    password_reset_expiry TIMESTAMPTZ,

    -- Account lockout (brute-force protection)
    failed_login_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,

    -- Password history (prevent reuse)
    password_history TEXT[] DEFAULT '{}',

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ,

    CONSTRAINT valid_role CHECK (role IN ('admin', 'user', 'readonly'))
);

-- Permission groups
CREATE TABLE groups (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL UNIQUE,
    description TEXT,

    -- Permissions stored as JSONB array
    -- Structure: [{"action": "view", "resource": "recommendations", "constraints": {...}}, ...]
    permissions JSONB NOT NULL DEFAULT '[]',

    allowed_accounts TEXT[] DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL
);

-- Sessions
CREATE TABLE sessions (
    token VARCHAR(255) PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    role VARCHAR(32) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_agent TEXT,
    ip_address VARCHAR(45),
    csrf_token VARCHAR(255)
);

-- API Keys
CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    key_prefix VARCHAR(16) NOT NULL,
    key_hash VARCHAR(255) NOT NULL UNIQUE,

    -- Scoped permissions (JSONB array like groups)
    permissions JSONB DEFAULT '[]',

    is_active BOOLEAN NOT NULL DEFAULT true,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ
);

-- ==========================================
-- ANALYTICS TABLES (replaces S3/Athena)
-- ==========================================

-- Savings snapshots for analytics (partitioned by month)
CREATE TABLE savings_snapshots (
    id UUID DEFAULT uuid_generate_v4(),
    account_id VARCHAR(20) NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    provider VARCHAR(32) NOT NULL,
    service VARCHAR(64) NOT NULL,
    region VARCHAR(32) NOT NULL,
    commitment_type VARCHAR(32) NOT NULL,
    total_commitment DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    total_usage DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    total_savings DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    coverage_percentage DECIMAL(5,2) NOT NULL DEFAULT 0.00,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT valid_commitment_type CHECK (commitment_type IN ('RI', 'SavingsPlan'))
) PARTITION BY RANGE (timestamp);

-- Create initial partitions for the current month and next 2 months
-- Additional partitions will be created automatically via scheduled job
CREATE TABLE savings_snapshots_default PARTITION OF savings_snapshots DEFAULT;

-- ==========================================
-- HELPER FUNCTIONS
-- ==========================================

-- Function to automatically update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- ==========================================
-- TRIGGERS
-- ==========================================

-- Auto-update updated_at for tables with that column
CREATE TRIGGER update_global_config_updated_at BEFORE UPDATE ON global_config
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_service_configs_updated_at BEFORE UPDATE ON service_configs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_purchase_plans_updated_at BEFORE UPDATE ON purchase_plans
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_purchase_executions_updated_at BEFORE UPDATE ON purchase_executions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_groups_updated_at BEFORE UPDATE ON groups
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- ==========================================
-- INITIAL DATA
-- ==========================================

-- Insert default global configuration
INSERT INTO global_config (id) VALUES (1);
