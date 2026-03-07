CREATE TABLE ri_exchange_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id VARCHAR(20) NOT NULL CHECK (account_id ~ '^\d{12}$'),
    exchange_id VARCHAR(100) DEFAULT '',
    region VARCHAR(50) NOT NULL,
    source_ri_ids TEXT[] NOT NULL,
    source_instance_type VARCHAR(50) NOT NULL,
    source_count INTEGER NOT NULL,
    target_offering_id VARCHAR(255) NOT NULL,
    target_instance_type VARCHAR(50) NOT NULL,
    target_count INTEGER NOT NULL,
    payment_due DECIMAL(20,6) NOT NULL DEFAULT 0 CHECK (payment_due >= 0),
    status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'cancelled')),
    approval_token VARCHAR(255) UNIQUE,
    error TEXT,
    mode VARCHAR(20) NOT NULL DEFAULT 'manual' CHECK (mode IN ('manual', 'auto')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ
);

CREATE OR REPLACE FUNCTION update_ri_exchange_updated_at()
RETURNS TRIGGER AS $$
BEGIN NEW.updated_at = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ri_exchange_updated_at
    BEFORE UPDATE ON ri_exchange_history
    FOR EACH ROW EXECUTE FUNCTION update_ri_exchange_updated_at();

CREATE INDEX idx_ri_exchange_history_status ON ri_exchange_history(status);
CREATE INDEX idx_ri_exchange_history_created ON ri_exchange_history(created_at DESC);
CREATE INDEX idx_ri_exchange_history_account ON ri_exchange_history(account_id, created_at DESC);
