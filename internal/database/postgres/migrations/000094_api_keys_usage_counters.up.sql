-- API key usage counters (deferred sub-task from #340 / #344).
--
-- Surface per-key request volume to the Admin -> API Keys UI without
-- introducing a separate audit-log table. Two counters live directly on
-- the api_keys row, updated atomically by the same code path that
-- already maintains last_used_at:
--
--   * request_count_total        -- lifetime count since the key was created.
--   * request_count_window       -- count since request_count_window_start;
--                                  restarted (with a fresh window_start)
--                                  the first time RecordAPIKeyUsage sees
--                                  the window older than 24h. This is a
--                                  FIXED/TUMBLING window, not a true
--                                  trailing-24h rolling count: a request
--                                  just before the window resets is
--                                  discarded from the count as soon as the
--                                  next request starts a new window. See
--                                  store_postgres.go RecordAPIKeyUsage.
--                                  NOTE the restart only happens on the
--                                  key's NEXT request, so an idle key keeps
--                                  its closed window's count in this column
--                                  indefinitely. Readers must treat a
--                                  window older than 24h as zero -- see
--                                  service_apikeys_api.go
--                                  effectiveWindowUsage.
--   * request_count_window_start -- the timestamp the current window
--                                  started at; NULL until the first
--                                  request after this migration runs.
--                                  Exposed via the API so consumers can
--                                  see exactly which period the count
--                                  covers instead of assuming "last 24h".
--
-- Counters default to 0 because the columns are NOT NULL. That default is
-- not the same as "this key served no traffic": a key that was already in
-- use before this migration also reads as 0. The read path distinguishes
-- the two by last_used_at -- a zero request_count_total on a key that has
-- been used is reported as unknown (nil, surfacing as lifetime_partial),
-- while a never-used row keeps its genuine 0. See effectiveLifetimeUsage
-- in service_apikeys_api.go. The window-start column is nullable because
-- a row with zero-ever-usage shouldn't lie about when its window began.

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS request_count_total BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS request_count_window BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS request_count_window_start TIMESTAMPTZ;
