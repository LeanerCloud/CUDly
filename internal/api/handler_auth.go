// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

// Auth handlers

func (h *Handler) login(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Rate limiting: 5 attempts per IP per 15 minutes
	if err := h.checkRateLimitStrict(ctx, req, "login"); err != nil {
		return nil, err
	}

	var loginReq LoginRequest
	if err := json.Unmarshal([]byte(req.Body), &loginReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Decode base64-encoded password
	if decoded, err := decodeBase64Password(loginReq.Password); err != nil {
		return nil, err
	} else {
		loginReq.Password = decoded
	}

	response, err := h.auth.Login(ctx, loginReq)
	if err != nil {
		// Map MFA sentinels to machine-readable error codes so the
		// frontend can detect "MFA required" / "invalid MFA code"
		// without substring-matching the human message (issue #497).
		// Both keep the 401 status — the password leg passed, but
		// the request is not authorised until the MFA leg is too.
		if errors.Is(err, auth.ErrMFARequired) {
			return nil, NewClientError(401, "mfa_required")
		}
		if errors.Is(err, auth.ErrInvalidMFACode) {
			return nil, NewClientError(401, "invalid_mfa_code")
		}
		// All other auth failures (wrong password, account not found, locked, etc.)
		// collapse to a single opaque 401. Never forward err.Error() verbatim - it
		// may reveal internal account state.
		return nil, NewClientError(401, "invalid credentials")
	}

	return response, nil
}

func (h *Handler) logout(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Get token from Authorization header
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}

	if err := h.auth.Logout(ctx, token); err != nil {
		return nil, NewClientError(401, "invalid session")
	}

	return map[string]string{"status": "logged out"}, nil
}

func (h *Handler) getCurrentUser(ctx context.Context, req *events.LambdaFunctionURLRequest) (*CurrentUserResponse, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Get token from Authorization header
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, NewClientError(401, "invalid session")
	}

	user, err := h.auth.GetUser(ctx, session.UserID)
	if err != nil {
		return nil, err
	}

	return &CurrentUserResponse{
		ID:         user.ID,
		Email:      user.Email,
		Groups:     user.Groups,
		MFAEnabled: user.MFAEnabled,
	}, nil
}

// getCurrentUserPermissions handles GET /api/auth/me/permissions.
// It returns the effective permission set for the authenticated user,
// derived from the union of all their group permissions. The same path
// the backend uses for enforcement (GetUserPermissionsAPI -> GetUserPermissions).
//
// Auth: the route is AuthUser, which admits admin API key, user API
// key, OR a bearer-token session (see router.go AuthLevel doc). The
// handler resolves the authenticated user from any of those three
// credentials rather than re-requiring a bearer session — otherwise a
// caller that authenticated upstream with an API key would still get a
// 401 here.
func (h *Handler) getCurrentUserPermissions(ctx context.Context, req *events.LambdaFunctionURLRequest) (*UserPermissionsResponse, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	userID, err := h.resolveAuthenticatedUserID(ctx, req)
	if err != nil {
		return nil, err
	}

	// The stateless admin API key has no backing user row, so the
	// per-user permission lookup would fail. It is a full-access
	// infrastructure credential — surface it as {admin, *} + is_admin
	// (matches the requireAdmin / requirePermission short-circuit).
	if userID == apiKeyAdminUserID {
		return &UserPermissionsResponse{
			Permissions: []PermissionEntry{{Action: auth.ActionAdmin, Resource: auth.ResourceAll}},
			IsAdmin:     true,
		}, nil
	}

	raw, err := h.auth.GetUserPermissionsAPI(ctx, userID)
	if err != nil {
		return nil, err
	}

	// GetUserPermissionsAPI returns []auth.APIPermission via any to avoid
	// an import cycle between the api and auth packages. Fail loudly on
	// an unexpected payload shape: a silent fall-through to an empty
	// slice would render as "user lost all access" in the frontend and
	// mask the real server bug.
	apiPerms, ok := raw.([]auth.APIPermission)
	if !ok {
		logging.Errorf("getCurrentUserPermissions: GetUserPermissionsAPI returned %T, want []auth.APIPermission", raw)
		return nil, fmt.Errorf("GetUserPermissionsAPI returned unexpected payload type %T", raw)
	}
	entries := make([]PermissionEntry, len(apiPerms))
	isAdmin := false
	for i, p := range apiPerms {
		entries[i] = PermissionEntry{Action: p.Action, Resource: p.Resource}
		if p.Action == auth.ActionAdmin && p.Resource == auth.ResourceAll {
			isAdmin = true
		}
	}

	return &UserPermissionsResponse{
		Permissions: entries,
		IsAdmin:     isAdmin,
	}, nil
}

// resolveAuthenticatedUserID resolves the calling user's ID from any of
// the three auth modes admitted by AuthUser routes (admin API key, user
// API key, bearer-token session). Returns a 401 ClientError if no valid
// credential is present. The stateless admin API key returns the
// apiKeyAdminUserID sentinel — callers that need a real user row must
// special-case it (see getCurrentUserPermissions for the {admin, *}
// short-circuit).
func (h *Handler) resolveAuthenticatedUserID(ctx context.Context, req *events.LambdaFunctionURLRequest) (string, error) {
	// Admin API key first (stateless, no per-user lookup).
	apiKey := extractAPIKey(req)
	if h.checkAdminAPIKey(apiKey) {
		return apiKeyAdminUserID, nil
	}

	// User API key: resolves to the owning user row.
	if apiKey != "" {
		_, user, err := h.auth.ValidateUserAPIKeyAPI(ctx, apiKey)
		if err == nil {
			if u, ok := user.(*auth.User); ok && u != nil {
				return u.ID, nil
			}
		}
		// fall through to bearer-session if the API key didn't validate.
	}

	// Bearer-token session.
	token := h.extractBearerToken(req)
	if token == "" {
		return "", NewClientError(401, "no authorization token provided")
	}
	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil || session == nil {
		return "", NewClientError(401, "invalid session")
	}
	return session.UserID, nil
}

func (h *Handler) checkAdminExists(ctx context.Context, req *events.LambdaFunctionURLRequest) (*AdminExistsResponse, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	exists, err := h.auth.CheckAdminExists(ctx)
	if err != nil {
		return nil, err
	}

	return &AdminExistsResponse{AdminExists: exists}, nil
}

func (h *Handler) setupAdmin(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Rate limiting: 5 attempts per IP per 15 minutes for admin setup
	if err := h.checkRateLimitStrict(ctx, req, "setup_admin"); err != nil {
		return nil, err
	}

	var setupReq SetupAdminRequest
	if err := json.Unmarshal([]byte(req.Body), &setupReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	response, err := h.auth.SetupAdmin(ctx, setupReq)
	if err != nil {
		// Share the sentinel→ClientError mapping with /api/users (issue #349)
		// so bootstrap failures surface as the right 4xx instead of a 500.
		return nil, mapAuthError(err)
	}

	return response, nil
}

func (h *Handler) forgotPassword(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	var pwdReq PasswordResetRequest
	if err := json.Unmarshal([]byte(req.Body), &pwdReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Rate limiting: 10 attempts per email per 5 minutes to prevent enumeration attacks.
	// On rate-limiter error: emit a high-severity alert (02-M1) but continue with the
	// request. Failing closed here would reveal infrastructure state (503 vs 200) to
	// an enumerating attacker and harm legitimate users more than the enumeration risk.
	if h.rateLimiter != nil {
		allowed, err := h.rateLimiter.AllowWithEmail(ctx, pwdReq.Email, "forgot_password")
		if err != nil {
			logging.Errorf("ALERT: rate limiter error on forgot_password for %s; proceeding fail-open (02-M1): %v",
				redactEmail(pwdReq.Email), err)
		} else if !allowed {
			logging.Warnf("Rate limit exceeded for forgot password: %s", redactEmail(pwdReq.Email))
			// Always return success message to prevent email enumeration
			return map[string]string{"status": "if the email exists, a reset link has been sent"}, nil
		}
	}

	// Always return success to prevent email enumeration
	if err := h.auth.RequestPasswordReset(ctx, pwdReq.Email); err != nil {
		logging.Warnf("Password reset request error: %v", err)
	}

	return map[string]string{"status": "if the email exists, a reset link has been sent"}, nil
}

// isResetPasswordClientError reports whether err originates from a user-correctable
// condition during password reset. The substring list is intentionally narrow and
// is matched against the error messages produced by internal/auth/service_password.go.
// When adding a new client-correctable error in the service, add its substring here too.
func isResetPasswordClientError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "current password") ||
		strings.Contains(s, "used recently") ||
		strings.Contains(s, "invalid or expired reset token") ||
		strings.Contains(s, "reset token has expired") ||
		strings.Contains(s, "password must")
}

// resetPasswordStatus returns the runtime state of a reset token without
// consuming it. The frontend hits this before rendering the reset-
// password form so it can show an "expired" or "already used" view
// instead of a form that can never submit (issues #460, #461).
//
// Response shape is uniform: 200 with {state, flow} for every state,
// including "used" (covers both consumed and never-issued tokens; the
// row is wiped on consumption, so the store can't distinguish them).
// A non-200 means an infrastructure failure, not a token-state signal.
func (h *Handler) resetPasswordStatus(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Same rate-limit bucket as the submit endpoint: a token-probing
	// attacker would otherwise get a free oracle to test tokens.
	if err := h.checkRateLimitStrict(ctx, req, "reset_password"); err != nil {
		return nil, err
	}

	token := ""
	if req != nil {
		token = req.QueryStringParameters["token"]
	}
	if token == "" {
		return nil, NewClientError(400, "token is required")
	}

	state, flow, err := h.auth.ResetTokenStatus(ctx, token)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"state": state,
		"flow":  flow,
	}, nil
}

func (h *Handler) resetPassword(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Rate limiting: 10 attempts per IP per 15 minutes
	if err := h.checkRateLimitStrict(ctx, req, "reset_password"); err != nil {
		return nil, err
	}

	var pwdResetReq PasswordResetConfirm
	if err := json.Unmarshal([]byte(req.Body), &pwdResetReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// The frontend base64-encodes new_password (see frontend/src/api/auth.ts:
	// resetPassword) — same pattern as login / change-password / update-profile.
	// Decode it back to plaintext before handing to the service or the bcrypt
	// hash stored represents the base64 string, leaving the user unable to log
	// in with the password they thought they set. See issue #356.
	decoded, err := decodeBase64Password(pwdResetReq.NewPassword)
	if err != nil {
		return nil, err
	}
	pwdResetReq.NewPassword = decoded

	if err := h.auth.ConfirmPasswordReset(ctx, pwdResetReq); err != nil {
		if isResetPasswordClientError(err) {
			return nil, NewClientError(400, err.Error())
		}
		return nil, err
	}

	return map[string]string{"status": "password reset successful"}, nil
}

func (h *Handler) updateProfile(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Get current user from token
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, NewClientError(401, "invalid session")
	}

	// Parse request body
	var profileReq ProfileUpdateRequest
	if err := json.Unmarshal([]byte(req.Body), &profileReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Validate email format before decoding passwords (cheap check first).
	if err := validateEmailFormat(profileReq.Email); err != nil {
		return nil, err
	}

	// Decode base64-encoded passwords if provided
	currentPassword, newPassword, err := decodeProfilePasswords(profileReq)
	if err != nil {
		return nil, err
	}

	// Update profile through auth service
	if err := h.auth.UpdateUserProfile(ctx, session.UserID, profileReq.Email, currentPassword, newPassword); err != nil {
		return nil, mapProfileUpdateError(err)
	}

	return map[string]string{"status": "profile updated"}, nil
}

// mapProfileUpdateError converts profile-update service errors to the correct
// HTTP status code and a user-facing message.
//
//   - ErrCurrentPasswordIncorrect -> 401 with a precise message (the acting
//     user is verifying their own credential, so specificity is safe).
//   - ErrEmailInUse -> 409 with a neutral message that does NOT confirm whether
//     another account holds the address; prevents account enumeration via the
//     profile-update path (issue #929).
//   - All other errors pass through unchanged for handleRequestError to render
//     as 500.
func mapProfileUpdateError(err error) error {
	switch {
	case errors.Is(err, auth.ErrCurrentPasswordIncorrect):
		return NewClientError(401, err.Error())
	case errors.Is(err, auth.ErrEmailInUse):
		return NewClientError(409, "Unable to update email")
	}
	return err
}

// decodeProfilePasswords decodes the optional base64-encoded current and new
// passwords from a ProfileUpdateRequest. Pulled out of updateProfile to keep
// that function under the cyclomatic limit.
func decodeProfilePasswords(req ProfileUpdateRequest) (current, next string, err error) {
	if req.CurrentPassword != "" {
		current, err = decodeBase64Password(req.CurrentPassword)
		if err != nil {
			return "", "", err
		}
	}
	if req.NewPassword != "" {
		next, err = decodeBase64Password(req.NewPassword)
		if err != nil {
			return "", "", err
		}
	}
	return current, next, nil
}

// decodeChangePasswordRequest validates and decodes both passwords from a ChangePasswordRequest.
func decodeChangePasswordRequest(pwdReq ChangePasswordRequest) (current, next string, err error) {
	if pwdReq.CurrentPassword == "" || pwdReq.NewPassword == "" {
		return "", "", NewClientError(400, "current password and new password are required")
	}
	current, err = decodeBase64Password(pwdReq.CurrentPassword)
	if err != nil {
		return "", "", err
	}
	next, err = decodeBase64Password(pwdReq.NewPassword)
	if err != nil {
		return "", "", err
	}
	return current, next, nil
}

// changePassword handles POST /api/auth/change-password
func (h *Handler) changePassword(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Rate limiting: 10 attempts per IP per 15 minutes
	if err := h.checkRateLimitStrict(ctx, req, "change_password"); err != nil {
		return nil, err
	}

	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, NewClientError(401, "invalid session")
	}

	var pwdReq ChangePasswordRequest
	if err := json.Unmarshal([]byte(req.Body), &pwdReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	currentPassword, newPassword, err := decodeChangePasswordRequest(pwdReq)
	if err != nil {
		return nil, err
	}

	if err := h.auth.ChangePasswordAPI(ctx, session.UserID, currentPassword, newPassword); err != nil {
		return nil, err
	}

	return map[string]string{"status": "password changed"}, nil
}

// Local aliases for the auth-package MFA sentinels so test files in
// this package can reference them without re-importing the auth
// package. The login handler maps these via errors.Is() to the
// machine-readable response codes "mfa_required" / "invalid_mfa_code".
var (
	mfaRequiredSentinel = auth.ErrMFARequired
	mfaInvalidSentinel  = auth.ErrInvalidMFACode
)

// mapMFAServiceError maps a service-layer MFA error to the right
// client-facing status + message. The auth package exports typed
// sentinel errors for each user-correctable condition; we match via
// errors.Is so a renamed error message in the service never silently
// drifts the HTTP status. See issue #512.
func mapMFAServiceError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, auth.ErrMFAInvalidPassword),
		errors.Is(err, auth.ErrMFAInvalidCode),
		errors.Is(err, auth.ErrMFACodeRequired),
		errors.Is(err, auth.ErrMFANoEnrollmentInProgress),
		errors.Is(err, auth.ErrMFAEnrollmentExpired),
		errors.Is(err, auth.ErrMFANotEnabled):
		return NewClientError(400, err.Error())
	case errors.Is(err, auth.ErrMFAAuthFailed):
		// Opaque 401 to prevent user enumeration: the service returns this
		// for both "user not found" and "DB lookup failed" paths so callers
		// cannot distinguish the two.
		return NewClientError(401, err.Error())
	}
	return err
}

// mfaSetup handles POST /api/auth/mfa/setup. Begins a fresh MFA
// enrollment for the current user. Requires the current password in
// the body (base64-encoded, same convention as login). Returns the
// generated secret + otpauth provisioning URI for QR display.
func (h *Handler) mfaSetup(ctx context.Context, req *events.LambdaFunctionURLRequest) (*MFASetupResponse, error) {
	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, err
	}
	var body MFASetupRequest
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	password, err := decodeBase64Password(body.Password)
	if err != nil {
		return nil, err
	}
	secret, uri, err := h.auth.MFASetupAPI(ctx, session.UserID, password)
	if err != nil {
		return nil, mapMFAServiceError(err)
	}
	return &MFASetupResponse{Secret: secret, ProvisioningURI: uri}, nil
}

// mfaEnable handles POST /api/auth/mfa/enable. Validates the supplied
// TOTP code against the pending secret + flips MFAEnabled=true on
// success. Returns the plaintext recovery codes once.
func (h *Handler) mfaEnable(ctx context.Context, req *events.LambdaFunctionURLRequest) (*MFAEnableResponse, error) {
	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, err
	}
	var body MFAEnableRequest
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	codes, err := h.auth.MFAEnableAPI(ctx, session.UserID, body.Code)
	if err != nil {
		return nil, mapMFAServiceError(err)
	}
	return &MFAEnableResponse{RecoveryCodes: codes}, nil
}

// mfaDisable handles POST /api/auth/mfa/disable. Requires both the
// current password AND a fresh proof-of-possession (TOTP or recovery
// code). On success: clears the secret + recovery codes, flips
// MFAEnabled=false.
func (h *Handler) mfaDisable(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, err
	}
	var body MFADisableRequest
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	password, err := decodeBase64Password(body.Password)
	if err != nil {
		return nil, err
	}
	if err := h.auth.MFADisableAPI(ctx, session.UserID, password, body.Code); err != nil {
		return nil, mapMFAServiceError(err)
	}
	return &StatusResponse{Status: "mfa disabled"}, nil
}

// mfaRegenerateRecoveryCodes handles POST
// /api/auth/mfa/regenerate-recovery-codes. Replaces all stored
// recovery codes with a fresh batch. Requires a current TOTP code
// (NOT a recovery code).
func (h *Handler) mfaRegenerateRecoveryCodes(ctx context.Context, req *events.LambdaFunctionURLRequest) (*MFARegenerateResponse, error) {
	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, err
	}
	var body MFARegenerateRequest
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	codes, err := h.auth.MFARegenerateRecoveryCodesAPI(ctx, session.UserID, body.Code)
	if err != nil {
		return nil, mapMFAServiceError(err)
	}
	return &MFARegenerateResponse{RecoveryCodes: codes}, nil
}

// redactEmail returns a redacted version of an email address for safe logging.
// e.g. "user@example.com" -> "us***@example.com"
func redactEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at:] // includes the '@'
	if len(local) <= 2 {
		return "***" + domain
	}
	return local[:2] + "***" + domain
}
