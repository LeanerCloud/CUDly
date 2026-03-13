-- Drop rate_limits table
DROP FUNCTION IF EXISTS cleanup_expired_rate_limits();
DROP INDEX IF EXISTS idx_rate_limits_reset_time;
DROP TABLE IF EXISTS rate_limits CASCADE;
