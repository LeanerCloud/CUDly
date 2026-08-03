-- Migration 000095: widen purchase_history.account_id to VARCHAR(255).
--
-- account_id has been VARCHAR(20) since 000001. That fits an AWS account ID
-- (12 digits) and nothing else:
--
--   * an Azure subscription ID is a 36-character GUID
--   * a GCP project ID is 6 to 30 characters
--
-- The value written into this column is cloud_accounts.external_id, which is
-- VARCHAR(255) at its source (000011), so nothing upstream constrains it.
-- SavePurchaseHistory therefore issues an INSERT that Postgres rejects with
-- SQLSTATE 22001 (`value too long for type character varying(20)`) for every
-- Azure purchase and every GCP purchase whose project ID exceeds 20 chars.
--
-- The purchase itself has already succeeded and been billed by the time this
-- INSERT runs, so the failure loses the audit row for real money spent. The
-- error is not swallowed -- savePurchaseHistory returns it and the caller
-- stamps a `history_write_failed` audit-gap marker on the execution
-- (issue #621) -- but the purchase_history row is gone: invisible in the
-- History view, absent from GetActivePurchaseHistory (so analytics undercount
-- committed spend), and unseen by the grace-period and suppression logic.
--
-- The identical widening was already applied to the sibling column:
-- migration 000067 widened savings_snapshots.account_id to VARCHAR(255) for
-- exactly this reason, and 000074 repaired it on partially-migrated
-- databases. purchase_history was missed. This closes that gap and lands on
-- VARCHAR(255), matching both cloud_accounts.external_id and
-- savings_snapshots.account_id.
--
-- Operator note: rows already lost to 22001 cannot be recovered by this
-- migration -- they were never inserted. Affected executions are identifiable
-- via `SELECT execution_id, error FROM purchase_executions WHERE error LIKE
-- '%history_write_failed%'`, whose marker carries the provider-side commitment
-- ID; reconciliation has to come from there or from the provider console. The
-- wildcard on both sides is deliberate: recordHistoryAuditGap appends the
-- marker via appendErrNote, so it is not necessarily at the start of the
-- field when the execution already carried an error.
--
-- ------------------------------------------------------------------
-- Why the probe, and why pg_attribute rather than information_schema
-- ------------------------------------------------------------------
-- Guarded so it is correct on a fresh database, on an already-deployed one,
-- and on re-run under the auto-heal path (project rule
-- feedback_migration_full_restore: IF NOT EXISTS does not repair a wrong
-- column type, so the ALTER has to be conditioned on the observed type
-- rather than skipped).
--
-- 000067 probed information_schema.columns without a schema predicate. This
-- one resolves the table through 'purchase_history'::regclass instead, which
-- follows the connection's search_path in EXACTLY the way the bare
-- `ALTER TABLE purchase_history` below does. buildMigrateDSN deliberately
-- appends no connection options (RDS Proxy does not support them), so
-- search_path is whatever the role default is; a probe that resolved the
-- table differently from the ALTER could silently match nothing, no-op, and
-- let golang-migrate record 000095 as applied while the p0 data loss
-- persisted. On a money path a migration must not be able to report success
-- without having done the work -- hence also the unconditional post-check
-- and the hard failure when the column is absent entirely.
--
-- atttypmod encodes VARCHAR(n) as n + 4. A value of -1 means unbounded
-- `varchar`, which is WIDER than VARCHAR(255) and must be left alone --
-- 000067's `character_maximum_length IS NULL` branch would have narrowed it.
--
-- Nothing else depends on this column's type: no view, materialized view,
-- foreign key or partition references purchase_history, so no drop/recreate
-- dance is needed (contrast 000067, which had to drop three views first).
-- Widening a varchar is binary-coercible, so Postgres skips both the table
-- rewrite and any index rebuild -- idx_purchase_history_account_timestamp
-- (000002) is left in place and the ALTER is near-instant even on a large
-- table. That reasoning does NOT hold in reverse; see the down migration.

DO $$
DECLARE
    typmod INTEGER;
BEGIN
    SELECT atttypmod INTO typmod
      FROM pg_attribute
     WHERE attrelid = 'purchase_history'::regclass
       AND attname  = 'account_id'
       AND NOT attisdropped;

    IF typmod IS NULL THEN
        RAISE EXCEPTION
            'migration 000095: column purchase_history.account_id does not exist';
    END IF;

    IF typmod <> -1 AND typmod - 4 < 255 THEN
        ALTER TABLE purchase_history
            ALTER COLUMN account_id TYPE VARCHAR(255);

        -- Post-check: re-read the catalog and fail loudly if the widening
        -- did not take effect, so this migration can never be recorded as
        -- applied while account_id is still too narrow for Azure/GCP.
        SELECT atttypmod INTO typmod
          FROM pg_attribute
         WHERE attrelid = 'purchase_history'::regclass
           AND attname  = 'account_id'
           AND NOT attisdropped;

        IF typmod <> -1 AND typmod - 4 < 255 THEN
            RAISE EXCEPTION
                'migration 000095 failed to widen purchase_history.account_id: still VARCHAR(%)',
                typmod - 4;
        END IF;
    END IF;
END $$;
