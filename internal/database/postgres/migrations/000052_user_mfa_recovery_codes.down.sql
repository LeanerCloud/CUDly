-- Reverse 000051: drop MFA enrollment + recovery code columns.
ALTER TABLE users
    DROP COLUMN IF EXISTS mfa_pending_secret,
    DROP COLUMN IF EXISTS mfa_pending_secret_expires_at,
    DROP COLUMN IF EXISTS mfa_recovery_codes;
