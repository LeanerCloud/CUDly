-- Migration 000063: allow NULL in purchase_history.monthly_cost
--
-- Prior to this migration, monthly_cost had a NOT NULL constraint that forced
-- execution.go to collapse nil (provider API did not return a monthly breakdown)
-- into 0.0, making "no data" indistinguishable from "explicitly $0 recurring
-- charge" (e.g. AWS all-upfront commitments). Dropping the constraint lets new
-- writes carry NULL for Azure/GCP recommendations where the monthly breakdown
-- is absent, preserving the semantic distinction for the History UI.
--
-- Existing 0.0 rows are NOT updated: those represent real $0 recurring charges
-- (typically AWS all-upfront RIs/SPs) and the conversion was lossless for them.
-- Only new writes from providers where monthly_cost was nil can now store NULL.

ALTER TABLE purchase_history ALTER COLUMN monthly_cost DROP NOT NULL;
