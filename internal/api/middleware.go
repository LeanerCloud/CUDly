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
		"/api/auth/login",
		"/api/auth/check-admin",
		"/api/auth/setup-admin",
		"/api/auth/forgot-password",
		"/api/auth/reset-password",
		"/docs",
		"/api/docs",
	}
	for _, ep := range publicEndpoints {
		if strings.HasPrefix(path, ep) {
			return true
		}
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

	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
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
		"/api/purchases/approve/", // Token-based auth
		"/api/purchases/cancel/",  // Token-based auth
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

	// Check if request is using API key (no CSRF needed for API keys)
	apiKey := req.Headers["x-api-key"]
	if apiKey == "" {
		apiKey = req.Headers["X-API-Key"]
	}

	// If using API key, skip CSRF validation
	if apiKey != "" {
		// Validate it's a valid API key (admin or user)
		if h.apiKey != "" && subtle.ConstantTimeCompare([]byte(apiKey), []byte(h.apiKey)) == 1 {
			return nil // Admin API key
		}
		if h.auth != nil {
			_, _, err := h.auth.ValidateUserAPIKeyAPI(ctx, apiKey)
			if err == nil {
				return nil // Valid user API key
			}
		}
		// Invalid API key - fall through to require CSRF
	}

	// Get session token
	sessionToken := h.extractBearerToken(req)
	if sessionToken == "" {
		return fmt.Errorf("no session token for CSRF validation")
	}

	// Get CSRF token from header
	csrfToken := req.Headers["x-csrf-token"]
	if csrfToken == "" {
		csrfToken = req.Headers["X-CSRF-Token"]
	}

	return h.auth.ValidateCSRFToken(ctx, sessionToken, csrfToken)
}

// requireAdmin checks if the current user has admin role.
// Accepts both admin API-key auth and Bearer token auth.
func (h *Handler) requireAdmin(ctx context.Context, req *events.LambdaFunctionURLRequest) (*Session, error) {
	// Check admin API key first (stateless auth)
	apiKey := extractAPIKey(req)
	if h.checkAdminAPIKey(apiKey) {
		return &Session{Role: "admin"}, nil
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
