-- Cache of cloud recommendations collected by the scheduler. Every invocation
-- of scheduler.CollectRecommendations writes here; read handlers query this
-- table instead of re-fetching live from AWS/Azure/GCP. Turns dashboard
-- provider-switch latency from seconds (live API calls) into milliseconds
-- (SQL read).
CREATE TABLE IF NOT EXISTS recommendations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    collected_at      TIMESTAMPTZ NOT NULL,
    cloud_account_id  UUID REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    provider          TEXT NOT NULL,
    service           TEXT NOT NULL,
    region            TEXT NOT NULL DEFAULT '',
    resource_type     TEXT NOT NULL DEFAULT '',
    -- Full RecommendationRecord JSON for display. Redundant with the
    -- denormalised columns below but kept authoritative so read handlers
    -- never have to reconstruct the record from columns.
    payload           JSONB NOT NULL,
    -- Denormalised for SQL-level filtering/aggregation. Go types are
    -- float64 and decoded via pgx's numeric scanner.
    upfront_cost      NUMERIC NOT NULL DEFAULT 0,
    monthly_savings   NUMERIC NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS recommendations_provider_idx   ON recommendations (provider);
CREATE INDEX IF NOT EXISTS recommendations_account_idx    ON recommendations (cloud_account_id);
CREATE INDEX IF NOT EXISTS recommendations_service_idx    ON recommendations (service);
CREATE INDEX IF NOT EXISTS recommendations_collected_idx  ON recommendations (collected_at DESC);

-- Natural key for upsert. Postgres UNIQUE treats NULL as distinct, which
-- would let duplicate "global" rows (no cloud_account_id) accumulate. We
-- collapse NULL to the zero UUID via a generated column so the UNIQUE
-- constraint fires deterministically. region and resource_type use ''
-- defaults (column definitions above) for the same reason.
ALTER TABLE recommendations
    ADD COLUMN IF NOT EXISTS account_key UUID
    GENERATED ALWAYS AS (COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid)) STORED;

CREATE UNIQUE INDEX IF NOT EXISTS recommendations_natural_key_idx
    ON recommendations (account_key, provider, service, region, resource_type);

-- Singleton table tracking the last successful collection timestamp + the
-- last collection error (if the most recent run was partial or failed).
-- The CHECK enforces a single row; the insert seeds it.
CREATE TABLE IF NOT EXISTS recommendations_state (
    id                     INT PRIMARY KEY DEFAULT 1,
    last_collected_at      TIMESTAMPTZ,
    last_collection_error  TEXT,
    CHECK (id = 1)
);

INSERT INTO recommendations_state (id) VALUES (1) ON CONFLICT DO NOTHING;
