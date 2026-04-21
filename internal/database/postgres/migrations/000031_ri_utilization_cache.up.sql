-- Persistent TTL cache for AWS Cost Explorer GetReservationUtilization
-- results. Lambda containers are short-lived and dashboards / the RI
-- Exchange page can be served from any warm or cold container, so a
-- per-process in-memory cache effectively never hits: every dashboard
-- load billed a Cost Explorer API call. Postgres-backed storage shares
-- the cache across all containers and persists across restarts.
--
-- Key: (region, lookback_days). Payload is the JSON encoding of
-- []recommendations.RIUtilization — opaque to SQL, decoded by the
-- api-layer cache wrapper. TTL freshness is enforced in the caller,
-- not the DB, so tests can inject shorter TTLs without touching the
-- schema.
CREATE TABLE IF NOT EXISTS ri_utilization_cache (
    region        TEXT NOT NULL,
    lookback_days INT  NOT NULL,
    payload       JSONB NOT NULL,
    fetched_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (region, lookback_days)
);
