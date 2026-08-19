-- Migration 000098: backfill purchase_executions.step_number onto the 1-based
-- "step this row completes" convention (issue #1669).
--
-- WHY
-- purchase.getOrCreateExecution stamped step_number with the COUNT of ramp
-- steps already completed (plan.ramp_schedule.current_step), while
-- api.createPurchaseExecutionsTx stamped the 1-based step the row would
-- execute (current_step + i + 1). The disagreement was invisible while the
-- ramp advanced by a blind CurrentStep++ that ignored step_number entirely.
--
-- Issue #1669 makes the advance idempotent per step: it advances only when the
-- completing step is exactly current_step + 1. An old-convention row therefore
-- completes a step the plan has already counted, which the new code correctly
-- treats as a no-op, so the ramp would never advance again for that plan and
-- the remaining steps would silently never purchase. That is the same
-- under-buy shape #1669 exists to fix, so the in-flight rows must be corrected
-- as part of it.
--
-- WHICH ROWS
-- Only rows that can still execute. A correctly-stamped executable row names a
-- step the plan has not yet counted, i.e. step_number >= current_step + 1, so
-- step_number <= current_step is the old-convention marker.
--
-- One correctly-stamped shape defeats that marker on its own, hence the NOT
-- EXISTS guard. A multi-account step that partially failed leaves per-account
-- retry successors carrying the step they belong to; once the first retry
-- advances the ramp, a sibling successor still pending for the SAME step now
-- reads step_number = current_step. Retargeting it would make it buy its own
-- step's tranche while advancing the ramp past the next one, which is exactly
-- the under-buy #1669 fixes. Such a row is recognizable: a sibling execution
-- for that plan and step has already reached a successful terminal status,
-- which is never true of an old-convention row (the old writer stamped the row
-- that advanced step k as k-1, so no completed row carries k).
--
-- 'paused' counts as executable: resumePlannedPurchase transitions it back to
-- 'pending' and runPlannedPurchase transitions it straight to 'running'
-- (internal/api/handler_purchases.go).
--
-- Terminal rows (completed / partially_completed / failed / canceled /
-- expired / revocation_requested) are deliberately NOT rewritten: their
-- step_number is audit trail and display, and no discriminator distinguishes
-- an old-convention terminal row from a correctly-stamped one whose ramp has
-- since moved past it.
--
-- DEPLOY WINDOW
-- Old code instances still running when this migration lands keep writing the
-- old convention, exactly as migration 000089 documents for the cancelled ->
-- canceled rename. Such a row freezes its own plan's ramp at one step until an
-- operator intervenes. The window is the deploy overlap, and the alternative
-- (deriving current_step from the executions table) is a larger change tracked
-- separately.

UPDATE purchase_executions e
   SET step_number = COALESCE((p.ramp_schedule ->> 'current_step')::int, 0) + 1
  FROM purchase_plans p
 WHERE e.plan_id = p.id
   AND e.status IN ('pending', 'notified', 'approved', 'running', 'scheduled', 'paused')
   AND e.step_number <= COALESCE((p.ramp_schedule ->> 'current_step')::int, 0)
   AND NOT EXISTS (
       SELECT 1
         FROM purchase_executions sibling
        WHERE sibling.plan_id = e.plan_id
          AND sibling.step_number = e.step_number
          AND sibling.status IN ('completed', 'partially_completed')
   );
