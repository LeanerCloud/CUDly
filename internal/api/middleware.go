package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

// isPublicEndpoint returns true for endpoints that don't require authentication
func (h *Handler) isPublicEndpoint(path string) bool {
	publicEndpoints := []string{
		"/health",     // Root health endpoint (no /api prefix)
		"/api/health", // API health endpoint
		"/api/info",
		"/version", // Public build-version endpoint (version / git SHA / build time)
		"/api/purchases/approve/",
		"/api/purchases/cancel/",
		"/api/ri-exchange/approve/",
		"/api/ri-exchange/reject/",
		"/api/auth/login",
		"/api/auth/check-admin",
		"/api/auth/setup-admin",
		"/api/auth/forgot-password",
		"/api/auth/reset-password",
		"/api/register/", // GET /api/register/:token (trailing slash avoids matching /api/registrations)
		"/docs",
		"/api/docs",
	}
	for _, ep := range publicEndpoints {
		if strings.HasPrefix(path, ep) {
			return true
		}
	}
	// Exact match for POST /api/register (no trailing slash).
	if path == "/api/register" {
		return true
	}
	return false
}

// PrincipalKind identifies which credential type was used to authenticate.
type PrincipalKind string

const (
	// PrincipalAdminAPIKey is set when the request authenticated with the
	// shared admin API key (X-API-Key header matching h.apiKey).
	PrincipalAdminAPIKey PrincipalKind = "admin-api-key"
	// PrincipalUserAPIKey is set when the request authenticated with a
	// per-user API key issued via /api/api-keys.
	PrincipalUserAPIKey PrincipalKind = "user-api-key"
	// PrincipalSession is set when the request authenticated with a
	// bearer-token session (X-Authorization / Authorization header).
	PrincipalSession PrincipalKind = "session"
)

// Principal carries the resolved caller identity returned by
// authenticatePrincipal and requireAuth. Handlers that need the caller's
// identity read it from here rather than re-resolving it through a second
// ValidateSession / ValidateUserAPIKeyAPI call.
type Principal struct {
	Kind    PrincipalKind
	UserID  string   // empty for PrincipalAdminAPIKey
	Email   string   // empty for PrincipalAdminAPIKey; populated for session/user-api-key
	Role    string   // "admin" for admin-api-key; user's role otherwise
	Session *Session // non-nil only for PrincipalSession
}

// authenticate checks authentication via admin API key, user API key, or Bearer token
func (h *Handler) authenticate(ctx context.Context, req *events.LambdaFunctionURLRequest) bool {
	apiKey := extractAPIKey(req)

	if h.checkAdminAPIKey(apiKey) {
		return true
	}

	if h.checkUserAPIKey(ctx, apiKey) {
		return true
	}

	return h.checkBearerToken(ctx, req)
}

// authenticatePrincipal performs the same three-path credential check as
// authenticate but returns the fully resolved Principal so callers do not
// need to repeat the lookup. Returns a non-nil Principal on success; returns
// nil and a 401 ClientError when no valid credential is present.
func (h *Handler) authenticatePrincipal(ctx context.Context, req *events.LambdaFunctionURLRequest) (*Principal, error) {
	apiKey := extractAPIKey(req)

	if h.checkAdminAPIKey(apiKey) {
		return &Principal{Kind: PrincipalAdminAPIKey, Role: "admin"}, nil
	}

	if h.auth == nil {
		return nil, NewClientError(401, "authentication required")
	}

	if p := h.principalFromUserAPIKey(ctx, apiKey); p != nil {
		return p, nil
	}

	if p := h.principalFromBearerToken(ctx, req); p != nil {
		return p, nil
	}

	return nil, NewClientError(401, "authentication required")
}

// principalFromUserAPIKey resolves a Principal from a user API key.
// Returns nil when the key is empty, validation fails, or the user record
// cannot be retrieved.
func (h *Handler) principalFromUserAPIKey(ctx context.Context, apiKey string) *Principal {
	if apiKey == "" {
		return nil
	}
	_, userRaw, err := h.auth.ValidateUserAPIKeyAPI(ctx, apiKey)
	if err != nil {
		logging.Debugf("User API key validation failed: %v", err)
		return nil
	}
	if userRaw == nil {
		return nil
	}
	p := &Principal{Kind: PrincipalUserAPIKey, Role: "user"}
	// userRaw is returned as any from the interface. Extract fields
	// via a locally-scoped interface to avoid an import cycle.
	if uf, ok := userRaw.(interface {
		GetID() string
		GetEmail() string
		GetRole() string
	}); ok {
		p.UserID = uf.GetID()
		p.Email = uf.GetEmail()
		p.Role = uf.GetRole()
	}
	return p
}

// principalFromBearerToken resolves a Principal from a session bearer token.
// Returns nil when no token is present or the session is invalid.
func (h *Handler) principalFromBearerToken(ctx context.Context, req *events.LambdaFunctionURLRequest) *Principal {
	token := h.extractBearerToken(req)
	if token == "" {
		return nil
	}
	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil || session == nil {
		return nil
	}
	return &Principal{
		Kind:    PrincipalSession,
		UserID:  session.UserID,
		Email:   session.Email,
		Role:    session.Role,
		Session: session,
	}
}

func extractAPIKey(req *events.LambdaFunctionURLRequest) string {
	apiKey := req.Headers["x-api-key"]
	if apiKey == "" {
		apiKey = req.Headers["X-API-Key"]
	}
	return apiKey
}

func (h *Handler) checkAdminAPIKey(apiKey string) bool {
	if apiKey != "" && h.apiKey != "" && subtle.ConstantTimeCompare([]byte(apiKey), []byte(h.apiKey)) == 1 {
		return true
	}
	return false
}

func (h *Handler) checkUserAPIKey(ctx context.Context, apiKey string) bool {
	if apiKey != "" && h.auth != nil {
		_, _, err := h.auth.ValidateUserAPIKeyAPI(ctx, apiKey)
		if err == nil {
			return true
		}
		logging.Debugf("User API key validation failed: %v", err)
	}
	return false
}

func (h *Handler) checkBearerToken(ctx context.Context, req *events.LambdaFunctionURLRequest) bool {
	token := h.extractBearerToken(req)
	if token != "" && h.auth != nil {
		_, err := h.auth.ValidateSession(ctx, token)
		if err == nil {
			return true
		}
	}
	return false
}

// extractBearerToken extracts the token from the Authorization or X-Authorization header
// Note: X-Authorization is used by the frontend because CloudFront OAC signs requests
// with SigV4, which overwrites the standard Authorization header.
func (h *Handler) extractBearerToken(req *events.LambdaFunctionURLRequest) string {
	// First check X-Authorization (used by frontend with CloudFront OAC)
	auth := req.Headers["x-authorization"]
	if auth == "" {
		auth = req.Headers["X-Authorization"]
	}
	// Fall back to standard Authorization header (for direct API access)
	if auth == "" {
		auth = req.Headers["authorization"]
	}
	if auth == "" {
		auth = req.Headers["Authorization"]
	}

	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return auth[len("bearer "):]
	}

	return ""
}

// requiresCSRFValidation returns true for state-changing requests that need CSRF protection.
//
// The req parameter is used to tighten the CSRF exemption for token-based
// approve/cancel/reject paths (issue #404): when the caller supplies both an
// in-URL approval token AND a valid session bearer token, the request is
// session-authenticated and CSRF protection must apply. When only the
// in-URL token is present (pure email-link flow, no session), CSRF is skipped.
func (h *Handler) requiresCSRFValidation(method, path string, req *events.LambdaFunctionURLRequest) bool {
	// Only POST, PUT, DELETE need CSRF protection
	if method != "POST" && method != "PUT" && method != "DELETE" {
		return false
	}

	// Auth endpoints that don't have a session yet are exempt unconditionally.
	// Prefix matching is safe for these /api/auth/* paths because no admin
	// sub-paths share those prefixes.
	csrfExemptAlwaysPrefix := []string{
		"/api/auth/login",
		"/api/auth/setup-admin",
		"/api/auth/forgot-password",
		"/api/auth/reset-password",
	}
	for _, exempt := range csrfExemptAlwaysPrefix {
		if strings.HasPrefix(path, exempt) {
			return false
		}
	}
	// Exact match for the public self-registration endpoint (issue #1017).
	// HasPrefix would also exempt /api/registrations/<id>/approve|reject|delete,
	// which are AuthAdmin state-changing routes that MUST be CSRF-protected.
	if path == "/api/register" {
		return false
	}

	// Token-based approve/cancel/reject paths are exempt ONLY when the request
	// carries no session bearer token. If a session is present, the request is
	// session-authenticated and standard CSRF protection applies (issue #404).
	csrfExemptWhenTokenOnly := []string{
		"/api/purchases/approve/",
		"/api/purchases/cancel/",
		"/api/ri-exchange/approve/",
		"/api/ri-exchange/reject/",
	}
	for _, prefix := range csrfExemptWhenTokenOnly {
		if strings.HasPrefix(path, prefix) {
			// Only skip CSRF when there is no session token on the request.
			return h.extractBearerToken(req) != ""
		}
	}

	return true
}

// validateCSRF validates the CSRF token from the request header
func (h *Handler) validateCSRF(ctx context.Context, req *events.LambdaFunctionURLRequest) error {
	if h.auth == nil {
		return fmt.Errorf("authentication service not configured")
	}

	// API-key-authenticated requests bypass CSRF (no cookie-based session).
	if h.apiKeyBypassCSRF(ctx, req) {
		return nil
	}

	sessionToken := h.extractBearerToken(req)
	if sessionToken == "" {
		return fmt.Errorf("no session token for CSRF validation")
	}

	csrfToken := extractCSRFToken(req)
	logMissingCSRFToken(req, csrfToken)
	return h.auth.ValidateCSRFToken(ctx, sessionToken, csrfToken)
}

// apiKeyBypassCSRF returns true when the request presents a valid admin or
// user API key. API-key auth is stateless and not cookie-based, so CSRF
// protection doesn't apply. An invalid API key returns false — the caller
// then falls through to session-based CSRF validation.
func (h *Handler) apiKeyBypassCSRF(ctx context.Context, req *events.LambdaFunctionURLRequest) bool {
	apiKey := req.Headers["x-api-key"]
	if apiKey == "" {
		apiKey = req.Headers["X-API-Key"]
	}
	if apiKey == "" {
		return false
	}
	if h.apiKey != "" && subtle.ConstantTimeCompare([]byte(apiKey), []byte(h.apiKey)) == 1 {
		return true
	}
	if _, _, err := h.auth.ValidateUserAPIKeyAPI(ctx, apiKey); err == nil {
		return true
	}
	return false
}

// extractCSRFToken pulls the CSRF token from the case-insensitive header.
func extractCSRFToken(req *events.LambdaFunctionURLRequest) string {
	if t := req.Headers["x-csrf-token"]; t != "" {
		return t
	}
	return req.Headers["X-CSRF-Token"]
}

// logMissingCSRFToken emits a defensive warn-level log when a session-
// authenticated request reaches CSRF validation with no token header.
// Almost always indicates a frontend regression (sessionStorage cleared, or
// a new fetch site forgot to include the token). ValidateCSRFToken still
// rejects the request — this just makes the cause obvious in logs.
func logMissingCSRFToken(req *events.LambdaFunctionURLRequest, csrfToken string) {
	if csrfToken != "" {
		return
	}
	logging.Warnf("CSRF validation: empty header on session-authenticated %s %s from %s — frontend regression?",
		req.RequestContext.HTTP.Method, req.RequestContext.HTTP.Path, req.RequestContext.HTTP.SourceIP)
}

// requireAuth verifies the request carries a valid authentication credential
// of any kind (admin API key, user API key, or session bearer token).
//
// Used as a defence-in-depth check by Router.Route for AuthUser routes:
// validateSecurity → authenticate already runs before dispatch, but if a
// future refactor reorders middleware or a new route bypasses
// validateSecurity, this check still rejects unauthenticated requests at
// the router level. Returns the resolved Principal on success, a 401
// ClientError otherwise. Callers should use the returned Principal rather
// than re-resolving the caller's identity through a second ValidateSession
// or ValidateUserAPIKeyAPI call.
func (h *Handler) requireAuth(ctx context.Context, req *events.LambdaFunctionURLRequest) (*Principal, error) {
	return h.authenticatePrincipal(ctx, req)
}

// requireAdmin gates the coarse admin-only routes (AuthAdmin). "Admin" is now
// defined as holding the full-access {admin, *} capability, i.e. membership in
// the Administrators group. Accepts both the stateless admin API key (which
// bypasses the per-user lookup) and a Bearer-token session whose group-derived
// permissions include {admin, *}. Fail closed: a missing auth service, an
// invalid session, or a permission-lookup error denies access.
func (h *Handler) requireAdmin(ctx context.Context, req *events.LambdaFunctionURLRequest) (*Session, error) {
	// Check admin API key first (stateless auth)
	apiKey := extractAPIKey(req)
	if h.checkAdminAPIKey(apiKey) {
		return &Session{UserID: apiKeyAdminUserID}, nil
	}

	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, NewClientError(401, "invalid session")
	}

	// HasPermissionAPI(admin, *) returns true only for users who hold the
	// full-access capability, i.e. Administrators-group members. Any other
	// user (including zero-group users) is denied.
	isAdmin, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionAdmin, auth.ResourceAll)
	if err != nil {
		return nil, fmt.Errorf("admin permission check failed: %w", err)
	}
	if !isAdmin {
		return nil, NewClientError(403, "admin access required")
	}

	return session, nil
}

// checkRateLimit checks if the request is allowed based on IP-based rate limiting.
// Returns nil if allowed, or an error if rate limited.
//
// This function uses a fail-open design: if the rate limiter encounters an error
// (e.g. DB pool exhaustion, transient unavailability), the request is allowed
// through. Use checkRateLimitStrict for endpoints where availability of the
// rate limiter is itself a security requirement (credential brute-force surface).
func (h *Handler) checkRateLimit(ctx context.Context, req *events.LambdaFunctionURLRequest, endpoint string) error {
	if h.rateLimiter == nil {
		return nil
	}

	clientIP := req.RequestContext.HTTP.SourceIP
	allowed, err := h.rateLimiter.AllowWithIP(ctx, clientIP, endpoint)
	if err != nil {
		logging.Warnf("Rate limiter error for IP %s: %v", clientIP, err)
		// Continue on rate limiter errors to avoid blocking legitimate requests
		return nil
	}
	if !allowed {
		logging.Warnf("Rate limit exceeded for %s from IP: %s", endpoint, clientIP)
		return NewClientError(429, "too many requests, please try again later")
	}
	return nil
}

// checkRateLimitStrict is the fail-closed variant of checkRateLimit for
// credential endpoints (login, setup_admin, reset_password, change_password).
//
// When the rate limiter returns an error (DB unavailable, pool exhaustion), this
// function returns 503 instead of allowing the request through. An attacker who
// can induce DB pressure would otherwise unlock unlimited password guessing on
// these endpoints (02-M1). A high-severity error log is emitted so monitoring
// can detect and alert on the fail-closed window.
func (h *Handler) checkRateLimitStrict(ctx context.Context, req *events.LambdaFunctionURLRequest, endpoint string) error {
	if h.rateLimiter == nil {
		return nil
	}

	clientIP := req.RequestContext.HTTP.SourceIP
	allowed, err := h.rateLimiter.AllowWithIP(ctx, clientIP, endpoint)
	if err != nil {
		// High-severity alert: rate limiter unavailable on a credential endpoint.
		// Fail closed to preserve brute-force protection.
		logging.Errorf("ALERT: rate limiter error on credential endpoint %s for IP %s; request denied (02-M1): %v",
			endpoint, clientIP, err)
		return NewClientError(503, "service temporarily unavailable, please try again later")
	}
	if !allowed {
		logging.Warnf("Rate limit exceeded for %s from IP: %s", endpoint, clientIP)
		return NewClientError(429, "too many requests, please try again later")
	}
	return nil
}
