-- ==========================================
-- PERFORMANCE INDEXES
-- ==========================================

-- Service configs indexes (replaces DynamoDB GSI)
CREATE INDEX idx_service_configs_provider_service ON service_configs(provider, service);
CREATE INDEX idx_service_configs_enabled ON service_configs(enabled) WHERE enabled = true;

-- Purchase plans indexes
CREATE INDEX idx_purchase_plans_enabled ON purchase_plans(enabled) WHERE enabled = true;
CREATE INDEX idx_purchase_plans_next_execution ON purchase_plans(next_execution_date)
    WHERE next_execution_date IS NOT NULL AND enabled = true;
CREATE INDEX idx_purchase_plans_updated_at ON purchase_plans(updated_at DESC);

-- Purchase executions indexes
CREATE INDEX idx_purchase_executions_plan_id ON purchase_executions(plan_id);
CREATE INDEX idx_purchase_executions_status ON purchase_executions(status);
CREATE INDEX idx_purchase_executions_scheduled_date ON purchase_executions(scheduled_date);
CREATE INDEX idx_purchase_executions_plan_date ON purchase_executions(plan_id, scheduled_date);
CREATE INDEX idx_purchase_executions_expires_at ON purchase_executions(expires_at)
    WHERE expires_at IS NOT NULL;

-- Purchase history indexes (replaces DynamoDB GSI)
CREATE INDEX idx_purchase_history_account_timestamp ON purchase_history(account_id, timestamp DESC);
CREATE INDEX idx_purchase_history_timestamp ON purchase_history(timestamp DESC);
CREATE INDEX idx_purchase_history_plan_id ON purchase_history(plan_id) WHERE plan_id IS NOT NULL;
CREATE INDEX idx_purchase_history_provider_service ON purchase_history(provider, service, timestamp DESC);
CREATE INDEX idx_purchase_history_purchase_id ON purchase_history(purchase_id);

-- Users indexes
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_reset_token ON users(password_reset_token) WHERE password_reset_token IS NOT NULL;
CREATE INDEX idx_users_active ON users(active) WHERE active = true;
CREATE INDEX idx_users_role ON users(role);

-- Groups indexes
CREATE INDEX idx_groups_name ON groups(name);

-- Sessions indexes
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
CREATE INDEX idx_sessions_email ON sessions(email);

-- API Keys indexes
CREATE INDEX idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX idx_api_keys_key_hash ON api_keys(key_hash);
CREATE INDEX idx_api_keys_active ON api_keys(is_active) WHERE is_active = true;
CREATE INDEX idx_api_keys_expires_at ON api_keys(expires_at) WHERE expires_at IS NOT NULL;

-- ==========================================
-- ANALYTICS INDEXES
-- ==========================================

-- Savings snapshots indexes (critical for time-series queries)
CREATE INDEX idx_savings_snapshots_account_time ON savings_snapshots(account_id, timestamp DESC);
CREATE INDEX idx_savings_snapshots_provider ON savings_snapshots(provider, service, timestamp DESC);
CREATE INDEX idx_savings_snapshots_time ON savings_snapshots(timestamp DESC);
CREATE INDEX idx_savings_snapshots_account_provider ON savings_snapshots(account_id, provider, timestamp DESC);

-- GIN index for JSONB metadata searches (if needed)
CREATE INDEX idx_savings_snapshots_metadata ON savings_snapshots USING GIN (metadata);

-- ==========================================
-- FULL-TEXT SEARCH INDEXES (optional, for future features)
-- ==========================================

-- Purchase plan name search
CREATE INDEX idx_purchase_plans_name_trgm ON purchase_plans USING GIN (name gin_trgm_ops);

-- Group name and description search
CREATE INDEX idx_groups_name_trgm ON groups USING GIN (name gin_trgm_ops);
