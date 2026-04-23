-- capacity_percent records what fraction of the originally-recommended
-- counts the user chose when the bulk Purchase flow submitted this
-- execution. Audit-only: the request's rec counts already carry the
-- scaled values, and backend math uses those counts directly. Storing
-- the percentage here lets the Purchase History view answer "how much
-- did the user want of what was recommended?" without reverse-engineering
-- the scaling from individual counts.
--
-- DEFAULT 100 means legacy executions (and scheduler-driven executions
-- that don't go through the bulk UI) record "full capacity", which is
-- factually correct — those paths buy the recommended count verbatim.
ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS capacity_percent INT NOT NULL DEFAULT 100
        CHECK (capacity_percent BETWEEN 1 AND 100);
