-- 000053: tighten purchase_executions.cloud_account_id FK to ON DELETE RESTRICT (issue #606)
--
-- The original FK (migration 000011, line 132) was declared ON DELETE SET NULL.
-- That choice silently invalidates every pending / notified execution that
-- references the deleted cloud_account row:
--
--   1. The approve modal renders "(ambient)" because the frontend can't
--      resolve the now-null account.
--   2. The execution can never actually run for non-AWS providers: the
--      executor's chained DefaultAzureCredential / GCP ADC chain has no
--      registered account to dial, so the purchase fails with a credential
--      error the user can't fix without re-registering the deleted account.
--
-- Recommendations rows on the same migration keep ON DELETE SET NULL because
-- the next recommendation-collection cycle re-upserts them with the current
-- account IDs (or evicts orphans). Executions are immutable snapshots and
-- cannot self-heal, so the safer default is RESTRICT: block the DELETE at
-- the DB level and force the operator to cancel pending purchases first.
-- The handler in internal/api/handler_accounts.go also preflights this so
-- the frontend can offer a Cancel-All-And-Delete affordance instead of
-- surfacing a raw FK violation.

ALTER TABLE purchase_executions
    DROP CONSTRAINT IF EXISTS purchase_executions_cloud_account_id_fkey;

ALTER TABLE purchase_executions
    ADD CONSTRAINT purchase_executions_cloud_account_id_fkey
    FOREIGN KEY (cloud_account_id)
    REFERENCES cloud_accounts(id)
    ON DELETE RESTRICT;
