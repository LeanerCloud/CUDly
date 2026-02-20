-- Remove default admin user
-- Uses app.admin_email environment variable
DO $$
DECLARE
    admin_email TEXT;
BEGIN
    admin_email := current_setting('app.admin_email', true);

    IF admin_email IS NOT NULL AND admin_email != '' THEN
        DELETE FROM users WHERE email = admin_email;
        RAISE NOTICE 'Removed admin user: %', admin_email;
    ELSE
        RAISE NOTICE 'Admin email not provided, skipping admin user removal';
    END IF;
END $$;
