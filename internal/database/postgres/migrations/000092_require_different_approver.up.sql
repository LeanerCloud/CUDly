-- 000092: add require_different_approver to global_config (issue #1005, 4-eyes approval mode)
--
-- When enabled, the user who created a purchase execution cannot approve it
-- themselves. A different person with approval rights must do so. This is a
-- standard SOX / SOC2 segregation-of-duties control.
--
-- Default is false so existing deployments are unaffected on upgrade.
ALTER TABLE global_config
  ADD COLUMN IF NOT EXISTS require_different_approver BOOLEAN NOT NULL DEFAULT false;
