-- Create rate_limits table for distributed rate limiting
CREATE TABLE IF NOT EXISTS rate_limits (
    id TEXT PRIMARY KEY,              -- Format: "IP#{ip}#ENDPOINT#{endpoint}" or "EMAIL#{email}#ENDPOINT#{endpoint}"
    count INTEGER NOT NULL DEFAULT 0, -- Number of attempts in current window
    reset_time TIMESTAMPTZ NOT NULL,  -- When the window resets
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for cleanup queries
CREATE INDEX IF NOT EXISTS idx_rate_limits_reset_time ON rate_limits(reset_time);

-- Function to clean up expired rate limit entries (auto-cleanup)
CREATE OR REPLACE FUNCTION cleanup_expired_rate_limits()
RETURNS void AS $$
BEGIN
    DELETE FROM rate_limits WHERE reset_time < NOW() - INTERVAL '24 hours';
END;
$$ LANGUAGE plpgsql;

-- Comment
COMMENT ON TABLE rate_limits IS 'Distributed rate limiting for API endpoints across Lambda instances';
