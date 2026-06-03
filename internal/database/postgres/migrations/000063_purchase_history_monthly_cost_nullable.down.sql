-- Reverse 000063: restore NOT NULL on purchase_history.monthly_cost
--
-- Coerces any existing NULLs to 0.0 before re-adding the constraint so the
-- rollback never fails on rows written after the up migration applied.

UPDATE purchase_history SET monthly_cost = 0.0 WHERE monthly_cost IS NULL;

ALTER TABLE purchase_history ALTER COLUMN monthly_cost SET NOT NULL;
