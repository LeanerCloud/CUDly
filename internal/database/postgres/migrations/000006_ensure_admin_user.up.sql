-- Ensure admin user exists (UPSERT approach)
-- This migration ensures the admin user is created even if migration 000005 didn't run
-- Email will be set via runtime parameter: app.admin_email

DO $$
DECLARE
    admin_email TEXT;
    user_count INT;
BEGIN
    -- Get admin email from runtime parameter
    admin_email := current_setting('app.admin_email', true);

    IF admin_email IS NULL OR admin_email = '' THEN
        RAISE NOTICE 'Admin email not provided, skipping admin user check';
        RETURN;
    END IF;

    -- Check if user exists
    SELECT COUNT(*) INTO user_count FROM users WHERE email = admin_email;

    IF user_count = 0 THEN
        -- User doesn't exist, create it
        INSERT INTO users (
            id,
            email,
            password_hash,
            salt,
            role,
            active,
            created_at,
            updated_at
        ) VALUES (
            uuid_generate_v4(),
            admin_email,
            '',  -- Empty password - user must reset to login
            '',  -- Empty salt
            'admin',
            false,  -- Inactive until password is set via reset flow
            NOW(),
            NOW()
        );
        RAISE NOTICE 'Admin user created: %', admin_email;
    ELSE
        RAISE NOTICE 'Admin user already exists: %', admin_email;
    END IF;
END $$;
