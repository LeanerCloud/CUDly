-- commitment_options_probe_runs is a singleton table: one row per
-- successful probe of the AWS reserved-offerings APIs. Its presence is
-- the "is the cache warm?" sentinel. An empty table means the backend
-- has never persisted a probe (or the admin cleared it to force a
-- refresh). A non-empty table plus an empty commitment_options_combos
-- means "we probed and AWS genuinely has no matching offerings" — the
-- frontend will fall back to its hardcoded defaults in that case.
CREATE TABLE IF NOT EXISTS commitment_options_probe_runs (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    probed_at TIMESTAMPTZ NOT NULL,
    source_account_id TEXT NOT NULL
);

-- commitment_options_combos records each supported (provider, service,
-- term, payment) tuple harvested from the reserved-offerings APIs. The
-- compound PK makes re-persists idempotent under ON CONFLICT DO NOTHING.
CREATE TABLE IF NOT EXISTS commitment_options_combos (
    provider TEXT NOT NULL,
    service TEXT NOT NULL,
    term_years INT NOT NULL,
    payment_option TEXT NOT NULL,
    PRIMARY KEY (provider, service, term_years, payment_option)
);
