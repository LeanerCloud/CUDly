-- Reverse of 000058: remove the deferred constraint trigger and its
-- supporting function that enforce at least one Administrators-group member.
DROP TRIGGER IF EXISTS trg_min_one_admin ON users;
DROP FUNCTION IF EXISTS check_min_one_admin();
