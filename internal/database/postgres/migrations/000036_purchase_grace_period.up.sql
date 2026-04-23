-- grace_period_days holds a JSON map keyed by provider slug (e.g.
-- {"aws":7,"azure":7,"gcp":7}). The value is the window in days during
-- which recently-purchased recommendations are suppressed from the
-- rec-list so users don't re-buy the same capacity while cloud-provider
-- utilisation metrics catch up. A value of 0 for a provider disables
-- the feature for that provider; a missing key defaults to 7 in code.
ALTER TABLE global_config
  ADD COLUMN IF NOT EXISTS grace_period_days TEXT NOT NULL DEFAULT '{}';
