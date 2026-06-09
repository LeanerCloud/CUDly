-- purchase_suppressions tracks just-purchased recommendation capacity that
-- should be hidden from the Recommendations view for a provider-specific
-- grace window (configured in Settings → Purchasing → Grace Period). This
-- prevents re-proposing the same capacity while cloud-provider utilisation
-- metrics catch up after a fresh commitment.
--
-- Lifecycle (managed in code, not via FK cascade):
--   * Insert: same tx as the execution insert in executePurchase. If the
--     provider's grace period is 0, no row is written (feature disabled
--     for that provider).
--   * Delete: cancel/expire of the execution deletes by execution_id.
--   * Natural expiry: rows with expires_at < now() stop contributing to
--     the scheduler's subtraction pass. No sweeper needed — they simply
--     age out.
CREATE TABLE purchase_suppressions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id UUID NOT NULL,
    account_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    service TEXT NOT NULL,
    region TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    engine TEXT NOT NULL DEFAULT '',
    suppressed_count INT NOT NULL CHECK (suppressed_count > 0),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- One tuple per execution — if the same request has duplicate recs
    -- post-bucketing we collapse them client-side, but this constraint
    -- makes the "no duplicates per execution" invariant explicit at the
    -- DB level.
    UNIQUE (execution_id, account_id, provider, service, region,
            resource_type, engine)
);

-- Composite index for the scheduler's rec-list hot path. We include
-- expires_at in the column list rather than as a partial-index WHERE
-- clause because Postgres rejects NOW() in index predicates (not
-- IMMUTABLE). The scheduler's query filters by NOW() inline; the index
-- just makes the 6-tuple lookup + freshness filter fast.
CREATE INDEX idx_purchase_suppressions_lookup
    ON purchase_suppressions (account_id, provider, service, region,
                              resource_type, engine, expires_at);

-- Lookup index for cancel-execution's DELETE ... WHERE execution_id = $1
CREATE INDEX idx_purchase_suppressions_execution
    ON purchase_suppressions (execution_id);
