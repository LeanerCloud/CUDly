-- Roll back API key usage counters (000093).
ALTER TABLE api_keys
    DROP COLUMN IF EXISTS request_count_window_start,
    DROP COLUMN IF EXISTS request_count_window,
    DROP COLUMN IF EXISTS request_count_total;
