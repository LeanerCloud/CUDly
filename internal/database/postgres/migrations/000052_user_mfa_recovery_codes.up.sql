-- MFA enrollment + recovery codes (issue #497).
--
-- The TOTP secret + mfa_enabled flag already exist on users (added
-- alongside the verifier in service_mfa.go). This migration adds the
-- columns the enrollment + recovery-code flows need:
--
--   * mfa_pending_secret           - the proposed secret between
--                                    POST /api/auth/mfa/setup and
--                                    POST /api/auth/mfa/enable.
--                                    NULL when no enrollment is in
--                                    flight. Cleared on enable or
--                                    superseded by a fresh setup.
--   * mfa_pending_secret_expires_at- short-lived (5 min) expiry. An
--                                    abandoned enrollment expires
--                                    harmlessly without touching the
--                                    user's active mfa_secret /
--                                    mfa_enabled fields.
--   * mfa_recovery_codes           - bcrypt-hashed recovery codes
--                                    generated at enable + regenerate
--                                    time. Single-use: the matching
--                                    hash is removed from the array
--                                    when consumed during login or
--                                    disable.
--
-- All three are nullable / default-empty so existing rows are
-- unaffected by this migration.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS mfa_pending_secret TEXT,
    ADD COLUMN IF NOT EXISTS mfa_pending_secret_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS mfa_recovery_codes TEXT[] NOT NULL DEFAULT '{}';
