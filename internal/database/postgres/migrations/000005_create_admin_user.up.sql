-- Create default admin user without password
-- Email will be set via environment variable: app.admin_email
-- The admin user must use password reset to set their initial password
-- This migration is idempotent - it will not fail if user already exists

DO $$
DECLARE
    admin_email TEXT;
BEGIN
    -- Get admin email from environment variable
    admin_email := current_setting('app.admin_email', true);

    IF admin_email IS NULL OR admin_email = '' THEN
        RAISE NOTICE 'Admin email not provided, skipping admin user creation';
        RETURN;
    END IF;

    -- Check if admin user already exists
    IF NOT EXISTS (SELECT 1 FROM users WHERE email = admin_email) THEN
        -- Insert admin user with empty password (user must reset password to login)
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
            true,
            NOW(),
            NOW()
        );

        RAISE NOTICE 'Admin user created (no password set): %', admin_email;
    ELSE
        RAISE NOTICE 'Admin user already exists: %', admin_email;
    END IF;
END $$;
