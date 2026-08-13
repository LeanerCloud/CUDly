-- Submit-time idempotency for the RI exchange execute endpoints (issue #1642).
--
-- POST /api/ri-exchange/azure-instances/exchange and its AWS sibling commit an
-- irreversible exchange with no request-level dedupe. The Azure handler mints a
-- FRESH CalculateExchange session on every request (deliberately, so a stale or
-- client-supplied session can never bypass the guardrails), which means Azure's
-- own session-level replay protection never fires across two POSTs of the same
-- logical exchange. AWS's AcceptReservedInstancesExchangeQuote has no
-- ClientToken at all. A client that times out while the long-running operation
-- is still polling and retries therefore commits the exchange twice, spending
-- roughly twice the intended amount while each half stays under the cap.
--
-- This table is the claim ledger. The handler derives a fingerprint of WHAT the
-- request buys (account scope + sources + targets; see the exchangeIdempotency*
-- helpers in internal/api/handler_ri_exchange.go) and claims it in a single
-- atomic statement immediately before the commit call. Exactly one claimant wins
-- inside the window; the losers get a 409.
--
-- Rows are not garbage-collected on a schedule: a re-claim of the same
-- fingerprint after the window expires overwrites claimed_at in place, so the
-- table's size is bounded by the number of DISTINCT exchange shapes ever
-- submitted, which is tiny.
CREATE TABLE IF NOT EXISTS ri_exchange_idempotency (
    -- sha256 hex of the request fingerprint. Opaque to the database.
    idempotency_key TEXT PRIMARY KEY,
    -- When the current holder claimed it. Always written from the DATABASE
    -- clock (now()), never the application's, so skew between concurrent
    -- application instances cannot widen or shrink the window.
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
