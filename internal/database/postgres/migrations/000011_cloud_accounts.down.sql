-- Remove cloud_account_id from existing tables
ALTER TABLE ri_exchange_history DROP COLUMN IF EXISTS cloud_account_id;
ALTER TABLE savings_snapshots DROP COLUMN IF EXISTS cloud_account_id;

DROP INDEX IF EXISTS idx_purchase_executions_cloud_account;
ALTER TABLE purchase_executions DROP COLUMN IF EXISTS cloud_account_id;

DROP INDEX IF EXISTS idx_purchase_history_cloud_account;
ALTER TABLE purchase_history DROP COLUMN IF EXISTS cloud_account_id;

-- Drop new tables in reverse dependency order
DROP TABLE IF EXISTS plan_accounts;
DROP TABLE IF EXISTS account_service_overrides;
DROP TABLE IF EXISTS account_credentials;
DROP TABLE IF EXISTS cloud_accounts;
