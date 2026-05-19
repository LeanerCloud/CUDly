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
	ErrInvalidRole    = errors.New("invalid role")
	ErrAdminExists    = errors.New("admin user already exists")
	ErrPasswordPolicy = errors.New("password does not meet policy")

	// MFA login-gate sentinels — used by the login API handler to map
	// to machine-readable response codes (mfa_required /
	// invalid_mfa_code) so the frontend can branch on the error class
	// without substring-matching the human message. See issue #497.
	ErrMFARequired    = errors.New("mfa_required")
	ErrInvalidMFACode = errors.New("invalid_mfa_code")
)
