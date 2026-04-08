ALTER TABLE global_config
  DROP COLUMN IF EXISTS auto_collect,
  DROP COLUMN IF EXISTS collection_schedule,
  DROP COLUMN IF EXISTS notification_days_before;
