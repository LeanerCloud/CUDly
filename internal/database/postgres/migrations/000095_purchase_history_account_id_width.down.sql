-- Reverse 000095: narrow purchase_history.account_id back to VARCHAR(20).
--
-- THIS ROLLBACK IS LOSSY BY NATURE and deliberately FAILS LOUDLY rather than
-- truncating. Any row written after the up migration applied may carry an
-- Azure subscription GUID (36 chars) or a long GCP project ID; narrowing the
-- column would either error mid-rewrite or, worse, be "fixed" by a
-- well-meaning operator with a truncating USING clause that silently corrupts
-- the audit trail this table exists to provide.
--
-- So: refuse the rollback while any over-long value is present, naming the
-- count, and let the operator decide. There is no safe automatic remedy --
-- unlike 000063's down migration, which could coerce NULL monthly_cost to 0.0
-- losslessly, there is no lossless 20-character encoding of a 36-character
-- subscription ID.
--
-- 000067's down migration made the same call for savings_snapshots.account_id
-- and simply left the column at VARCHAR(255). This one goes further and does
-- narrow, but only when it is provably safe.
--
-- ------------------------------------------------------------------
-- Reverse exactly what the up migration did, and nothing more
-- ------------------------------------------------------------------
-- The up lands the column on exactly VARCHAR(255) (atttypmod 259) and leaves
-- anything already >= 255, or unbounded `varchar` (atttypmod -1), untouched.
-- So this narrows ONLY from 259. Guarding on "anything wider than 20" would
-- narrow a VARCHAR(300) or an unbounded varchar down to VARCHAR(20) -- a
-- state 000095 never created, and a rollback must restore the pre-up state
-- rather than impose a new one.
--
-- Unlike the up, this direction is NOT binary-coercible: narrowing a varchar
-- forces a full table rewrite and rebuilds every dependent index. On a large
-- purchase_history that is a long ACCESS EXCLUSIVE lock, not the near-instant
-- catalog-only change the up migration is.
--
-- ------------------------------------------------------------------
-- Recovering from a refused rollback
-- ------------------------------------------------------------------
-- golang-migrate stamps (version=95, dirty=true) BEFORE running this file, so
-- a refusal leaves schema_migrations dirty at 95 and maybeAutoHealDirty
-- (migrate.go) deliberately will NOT clear it -- the next deploy hard-fails
-- until an operator intervenes. Because this file raises before touching the
-- schema, the database still matches version 95 exactly. Recover by setting
-- CUDLY_FORCE_MIGRATION_VERSION=95 (the CURRENT version, never lower: the
-- 000095 up migration's effects ARE present) and redeploying, then removing
-- the env var. Only then archive or reconcile the over-long rows if the
-- rollback is still wanted.

DO $$
DECLARE
    typmod    INTEGER;
    over_long BIGINT;
BEGIN
    SELECT atttypmod INTO typmod
      FROM pg_attribute
     WHERE attrelid = 'purchase_history'::regclass
       AND attname  = 'account_id'
       AND NOT attisdropped;

    -- 259 == VARCHAR(255) (atttypmod is n + 4). Anything else was not
    -- produced by 000095's up migration, so leave it alone.
    IF typmod IS DISTINCT FROM 259 THEN
        RETURN;
    END IF;

    SELECT COUNT(*) INTO over_long
      FROM purchase_history
     WHERE CHAR_LENGTH(account_id) > 20;

    IF over_long > 0 THEN
        RAISE EXCEPTION
            'refusing to narrow purchase_history.account_id to VARCHAR(20): '
            '% row(s) hold an account ID longer than 20 characters '
            '(Azure subscription GUIDs are 36 chars, GCP project IDs up to 30). '
            'Narrowing would truncate the audit trail. Archive or reconcile '
            'those rows before rolling back migration 000095.',
            over_long;
    END IF;

    ALTER TABLE purchase_history
        ALTER COLUMN account_id TYPE VARCHAR(20);
END $$;
