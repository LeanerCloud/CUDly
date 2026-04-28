package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

// isPublicEndpoint returns true for endpoints that don't require authentication
func (h *Handler) isPublicEndpoint(path string) bool {
	publicEndpoints := []string{
		"/health",     // Root health endpoint (no /api prefix)
		"/api/health", // API health endpoint
		"/api/info",
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

// requiresCSRFValidation returns true for state-changing requests that need CSRF protection
func (h *Handler) requiresCSRFValidation(method, path string) bool {
	// Only POST, PUT, DELETE need CSRF protection
	if method != "POST" && method != "PUT" && method != "DELETE" {
		return false
	}

	// Auth endpoints that don't have a session yet are exempt
	csrfExemptPaths := []string{
		"/api/auth/login",
		"/api/auth/setup-admin",
		"/api/auth/forgot-password",
		"/api/auth/reset-password",
		"/api/purchases/approve/",   // Token-based auth
		"/api/purchases/cancel/",    // Token-based auth
		"/api/ri-exchange/approve/", // Token-based auth
		"/api/ri-exchange/reject/",  // Token-based auth
		"/api/register",             // Public registration (no session)
	}

	for _, exempt := range csrfExemptPaths {
		if strings.HasPrefix(path, exempt) {
			return false
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

// requireUser checks that the request carries a valid authentication
// (admin API key OR any valid Bearer-token session) and returns the
// resolved Session. Returns 401 on absence/invalid credentials. Used by
// Router.Route to enforce AuthUser routes at the router layer rather
// than relying solely on validateSecurity middleware ordering — see #60.
//
// Unlike requireAdmin, this does NOT require admin role. Any
// authenticated identity passes.
func (h *Handler) requireUser(ctx context.Context, req *events.LambdaFunctionURLRequest) (*Session, error) {
	// Admin API key first (stateless).
	apiKey := extractAPIKey(req)
	if h.checkAdminAPIKey(apiKey) {
		return &Session{Role: "admin", UserID: "admin-api-key"}, nil
	}

	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil || session == nil {
		return nil, NewClientError(401, "invalid session")
	}
	return session, nil
}

// requireAdmin checks if the current user has admin role.
// Accepts both admin API-key auth and Bearer token auth.
func (h *Handler) requireAdmin(ctx context.Context, req *events.LambdaFunctionURLRequest) (*Session, error) {
	// Check admin API key first (stateless auth)
	apiKey := extractAPIKey(req)
	if h.checkAdminAPIKey(apiKey) {
		return &Session{Role: "admin", UserID: "admin-api-key"}, nil
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

	if session.Role != "admin" {
		return nil, NewClientError(403, "admin access required")
	}

	return session, nil
}

// checkRateLimit checks if the request is allowed based on IP-based rate limiting.
// Returns nil if allowed, or an error if rate limited.
//
// Note: This function uses a fail-open design - if the rate limiter encounters
// an error (e.g., Redis unavailable), the request is allowed through rather than
// blocking legitimate users. This is intentional to ensure availability.
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
