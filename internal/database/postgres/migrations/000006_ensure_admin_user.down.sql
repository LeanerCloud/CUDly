-- Down migration for 000006_ensure_admin_user
-- Note: This does not delete the admin user, as that would be destructive
-- If you need to remove the admin user, do it manually

-- No-op migration (safe rollback)
SELECT 1;
