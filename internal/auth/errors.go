package auth

import "errors"

// Validation sentinels — wrapped by service_user.go's validators so the
// API handler can map each to a precise HTTP status code. Plain
// fmt.Errorf returns fall through to a generic 500 in
// internal/api/handler.go's handleRequestError, which hides the real
// cause from the user (see issue #349).
//
// Callers use errors.Is to detect the category; the wrapped message
// (when set via fmt.Errorf("%w: %s", ...)) carries the specific user-
// facing detail (e.g. "invalid role: guest", "password does not meet
// policy: must be at least 12 characters").
var (
	ErrInvalidEmail   = errors.New("invalid email format")
	ErrEmailInUse     = errors.New("email already in use")
	ErrAdminExists    = errors.New("admin user already exists")
	ErrPasswordPolicy = errors.New("password does not meet policy")

	// ErrNoGroups is returned when a create/update would leave a user with
	// zero group memberships. Authorization derives entirely from groups, so
	// a zero-group user can do nothing; the API rejects it as a 400 rather
	// than silently creating an inert account (issue #907).
	ErrNoGroups = errors.New("user must belong to at least one group")

	// ErrLastAdmin is returned when an update or delete would remove the last
	// remaining member of the Administrators group, which would lock everyone
	// out of admin-gated functionality. Mapped to 409 (issue #907).
	ErrLastAdmin = errors.New("cannot remove the last administrator")

	// ErrSelfEscalation is returned when a user attempts to grant themselves
	// a group they are not already a member of without holding the manage-users
	// permission. Mapped to 403 (issue #907).
	ErrSelfEscalation = errors.New("cannot escalate your own group membership")

	// ErrCurrentPasswordIncorrect is returned by UpdateUserProfile when the
	// caller-supplied current password does not match the stored hash. Mapped
	// to 401 at the API layer (the acting user is verifying their own
	// credential, so a precise message is safe -- issue #929).
	ErrCurrentPasswordIncorrect = errors.New("Current password is incorrect")

	// MFA login-gate sentinels — used by the login API handler to map
	// to machine-readable response codes (mfa_required /
	// invalid_mfa_code) so the frontend can branch on the error class
	// without substring-matching the human message. See issue #497.
	ErrMFARequired    = errors.New("mfa_required")
	ErrInvalidMFACode = errors.New("invalid_mfa_code")

	// MFA service-operation sentinels — returned (wrapped via fmt.Errorf
	// "%w") by the MFA lifecycle methods in service_mfa.go so the API
	// handler can map each error class to the right HTTP status code via
	// errors.Is rather than brittle substring matching. See issue #512.
	//
	// ErrMFAInvalidPassword        — wrong current password on setup/disable.
	// ErrMFAInvalidCode            — wrong TOTP or recovery code.
	// ErrMFACodeRequired           — MFA-enabled user supplied no code on disable.
	// ErrMFANoEnrollmentInProgress — MFAEnable called before MFASetup.
	// ErrMFAEnrollmentExpired      — pending enrollment window elapsed.
	// ErrMFANotEnabled             — regenerate/disable called when MFA is off.
	// ErrMFAAuthFailed             — generic opaque auth failure (user not found or
	//                               DB error; maps to 401 to prevent user enumeration).
	//
	// Message strings are intentionally identical to the pre-sentinel
	// fmt.Errorf literals so that existing tests relying on err.Error()
	// substrings continue to pass unchanged. See issue #512.
	ErrMFAInvalidPassword        = errors.New("invalid password")
	ErrMFAInvalidCode            = errors.New("invalid MFA code")
	ErrMFACodeRequired           = errors.New("MFA code or recovery code required")
	ErrMFANoEnrollmentInProgress = errors.New("no MFA enrollment in progress")
	ErrMFAEnrollmentExpired      = errors.New("MFA enrollment expired")
	ErrMFANotEnabled             = errors.New("MFA is not enabled")
	ErrMFAAuthFailed             = errors.New("authentication failed")
)
