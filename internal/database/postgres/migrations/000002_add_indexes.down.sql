-- Drop all indexes created in 000002_add_indexes.up.sql

-- Full-text search indexes
DROP INDEX IF EXISTS idx_groups_name_trgm;
DROP INDEX IF EXISTS idx_purchase_plans_name_trgm;

-- Analytics indexes
DROP INDEX IF EXISTS idx_savings_snapshots_metadata;
DROP INDEX IF EXISTS idx_savings_snapshots_account_provider;
DROP INDEX IF EXISTS idx_savings_snapshots_time;
DROP INDEX IF EXISTS idx_savings_snapshots_provider;
DROP INDEX IF EXISTS idx_savings_snapshots_account_time;

-- API Keys indexes
DROP INDEX IF EXISTS idx_api_keys_expires_at;
DROP INDEX IF EXISTS idx_api_keys_active;
DROP INDEX IF EXISTS idx_api_keys_key_hash;
DROP INDEX IF EXISTS idx_api_keys_user_id;

-- Sessions indexes
DROP INDEX IF EXISTS idx_sessions_email;
DROP INDEX IF EXISTS idx_sessions_expires_at;
DROP INDEX IF EXISTS idx_sessions_user_id;

-- Groups indexes
DROP INDEX IF EXISTS idx_groups_name;

-- Users indexes
DROP INDEX IF EXISTS idx_users_role;
DROP INDEX IF EXISTS idx_users_active;
DROP INDEX IF EXISTS idx_users_reset_token;
DROP INDEX IF EXISTS idx_users_email;

-- Purchase history indexes
DROP INDEX IF EXISTS idx_purchase_history_purchase_id;
DROP INDEX IF EXISTS idx_purchase_history_provider_service;
DROP INDEX IF EXISTS idx_purchase_history_plan_id;
DROP INDEX IF EXISTS idx_purchase_history_timestamp;
DROP INDEX IF EXISTS idx_purchase_history_account_timestamp;

-- Purchase executions indexes
DROP INDEX IF EXISTS idx_purchase_executions_expires_at;
DROP INDEX IF EXISTS idx_purchase_executions_plan_date;
DROP INDEX IF EXISTS idx_purchase_executions_scheduled_date;
DROP INDEX IF EXISTS idx_purchase_executions_status;
DROP INDEX IF EXISTS idx_purchase_executions_plan_id;

-- Purchase plans indexes
DROP INDEX IF EXISTS idx_purchase_plans_updated_at;
DROP INDEX IF EXISTS idx_purchase_plans_next_execution;
DROP INDEX IF EXISTS idx_purchase_plans_enabled;

-- Service configs indexes
DROP INDEX IF EXISTS idx_service_configs_enabled;
DROP INDEX IF EXISTS idx_service_configs_provider_service;
