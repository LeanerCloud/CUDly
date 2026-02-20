-- Drop tables in reverse order (respecting foreign key dependencies)

-- Analytics tables
DROP TABLE IF EXISTS savings_snapshots CASCADE;

-- Auth tables
DROP TABLE IF EXISTS api_keys CASCADE;
DROP TABLE IF EXISTS sessions CASCADE;
DROP TABLE IF EXISTS groups CASCADE;
DROP TABLE IF EXISTS users CASCADE;

-- Purchase tables
DROP TABLE IF EXISTS purchase_history CASCADE;
DROP TABLE IF EXISTS purchase_executions CASCADE;
DROP TABLE IF EXISTS purchase_plans CASCADE;

-- Configuration tables
DROP TABLE IF EXISTS service_configs CASCADE;
DROP TABLE IF EXISTS global_config CASCADE;

-- Drop functions
DROP FUNCTION IF EXISTS update_updated_at_column CASCADE;

-- Drop extensions (only if no other databases use them)
-- DROP EXTENSION IF EXISTS "pg_trgm" CASCADE;
-- DROP EXTENSION IF EXISTS "uuid-ossp" CASCADE;
