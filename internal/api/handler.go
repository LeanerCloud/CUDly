// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// Handler processes HTTP requests
type Handler struct {
	config             config.StoreInterface
	credStore          credentials.CredentialStore
	purchase           PurchaseManagerInterface
	scheduler          SchedulerInterface
	auth               AuthServiceInterface
	secretsARN         string
	azureCredsARN      string // Azure credentials secret ARN
	gcpCredsARN        string // GCP credentials secret ARN
	apiKey             string // Cached API key
	corsAllowedOrigin  string // CORS allowed origin
	rateLimiter        RateLimiterInterface
	emailNotifier      email.SenderInterface       // Optional: purchase approval emails
	dashboardURL       string                      // Base URL for approval/cancel links
	analyticsClient    AnalyticsClientInterface    // Optional: S3/Athena analytics client
	analyticsCollector AnalyticsCollectorInterface // Optional: Hourly collector
	signer             oidc.Signer                 // Optional: OIDC issuer signer (backed by cloud KMS)
	issuerURL          string                      // Canonical OIDC issuer URL (falls back to dashboardURL / request domain)

	awsCfgOnce sync.Once  // guards one-time loading of the base AWS config
	awsCfg     aws.Config // cached base AWS config (no region override)
	awsCfgErr  error      // error from loading the base config, if any
}

// NewHandler creates a new API handler
func NewHandler(cfg HandlerConfig) *Handler {
	corsOrigin := cfg.CORSAllowedOrigin
	if corsOrigin == "" {
		// Security: CORS must be explicitly configured
		// For local development, use CORS_ALLOWED_ORIGIN=http://localhost:3000
		// For production, use CORS_ALLOWED_ORIGIN=https://your-cloudfront-domain.com
		logging.Errorf("SECURITY WARNING: CORS_ALLOWED_ORIGIN not set. CORS will be disabled (no Access-Control-Allow-Origin header). Set this to your dashboard URL.")
		// Leave corsOrigin empty - the response will not include Access-Control-Allow-Origin header
		// This effectively disables CORS for browser-based clients
	}

	h := &Handler{
		config:             cfg.ConfigStore,
		credStore:          cfg.CredentialStore,
		purchase:           cfg.PurchaseManager,
		scheduler:          cfg.Scheduler,
		auth:               cfg.AuthService,
		secretsARN:         cfg.APIKeySecretARN,
		azureCredsARN:      cfg.AzureCredentialsSecretARN,
		gcpCredsARN:        cfg.GCPCredentialsSecretARN,
		corsAllowedOrigin:  corsOrigin,
		rateLimiter:        cfg.RateLimiter,
		emailNotifier:      cfg.EmailNotifier,
		dashboardURL:       cfg.DashboardURL,
		analyticsClient:    cfg.AnalyticsClient,
		analyticsCollector: cfg.AnalyticsCollector,
		signer:             cfg.OIDCSigner,
		issuerURL:          cfg.OIDCIssuerURL,
	}

	// Pre-load API key (with a 5s timeout to avoid stalling cold-start indefinitely)
	if cfg.APIKeySecretARN != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if key, err := h.loadAPIKey(ctx); err == nil {
			h.apiKey = key
		} else {
			logging.Warnf("Failed to pre-load API key from Secrets Manager: %v", err)
		}
	}

	// The OIDC signer is constructed once at app startup (see
	// internal/server/app.go) and passed in via cfg.OIDCSigner. Leave
	// it nil to disable the /.well-known/* endpoints.

	return h
}

// requirePermission validates authentication and checks if the user has the
// specified permission. Admin API keys and admin-role users bypass the check.
// Returns the session on success so callers can read session.UserID for
// account filtering.
func (h *Handler) requirePermission(ctx context.Context, req *events.LambdaFunctionURLRequest, action, resource string) (*Session, error) {
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

	if session.Role == "admin" {
		return session, nil
	}

	has, err := h.auth.HasPermissionAPI(ctx, session.UserID, action, resource)
	if err != nil {
		return nil, fmt.Errorf("permission check failed: %w", err)
	}
	if !has {
		return nil, NewClientError(403, fmt.Sprintf("permission denied: requires %s on %s", action, resource))
	}

	return session, nil
}

// getAllowedAccounts returns the list of account IDs the user is allowed to
// access. Empty slice means all access. Admin users always get all access.
func (h *Handler) getAllowedAccounts(ctx context.Context, session *Session) ([]string, error) {
	if session.Role == "admin" {
		return nil, nil // admin = all access
	}
	if h.auth == nil {
		return nil, nil
	}
	return h.auth.GetAllowedAccountsAPI(ctx, session.UserID)
}

// setSecurityHeaders adds comprehensive security headers to the response
func setSecurityHeaders(headers map[string]string) map[string]string {
	// Content Security Policy - restrictive for API responses
	// Only allow connections to same origin, block all other resources
	headers["Content-Security-Policy"] = "default-src 'none'; frame-ancestors 'none'"

	// Strict Transport Security - enforce HTTPS for 1 year including subdomains
	headers["Strict-Transport-Security"] = "max-age=31536000; includeSubDomains"

	// Permissions Policy - disable all browser features
	headers["Permissions-Policy"] = "geolocation=(), microphone=(), camera=()"

	// X-Content-Type-Options - prevent MIME sniffing
	headers["X-Content-Type-Options"] = "nosniff"

	// X-Frame-Options - prevent clickjacking
	headers["X-Frame-Options"] = "DENY"

	// Referrer-Policy - control referrer information
	headers["Referrer-Policy"] = "strict-origin-when-cross-origin"

	// Cache-Control - prevent caching of sensitive data
	headers["Cache-Control"] = "no-store, no-cache, must-revalidate"

	return headers
}

// HandleRequest processes a Lambda Function URL request
func (h *Handler) HandleRequest(ctx context.Context, req *events.LambdaFunctionURLRequest) (*events.LambdaFunctionURLResponse, error) {
	if req == nil {
		resp, _ := h.buildResponse(400, h.buildResponseHeaders(), map[string]string{"error": "nil request"}, nil)
		return resp, nil
	}
	corsHeaders := h.buildResponseHeaders()

	// Handle preflight
	method := req.RequestContext.HTTP.Method
	if method == "OPTIONS" {
		return h.buildResponse(200, corsHeaders, nil, nil)
	}

	path := req.RequestContext.HTTP.Path
	logging.Debugf("API Request: %s %s", method, path)

	// Validate request
	if response := h.validateRequest(ctx, req, method, path, corsHeaders); response != nil {
		return response, nil
	}

	// Route and execute request
	return h.executeRequest(ctx, method, path, req, corsHeaders)
}

// buildResponseHeaders creates response headers with security and CORS settings
func (h *Handler) buildResponseHeaders() map[string]string {
	corsHeaders := map[string]string{
		"Content-Type": "application/json",
	}

	corsHeaders = setSecurityHeaders(corsHeaders)

	if h.corsAllowedOrigin != "" {
		corsHeaders["Access-Control-Allow-Origin"] = h.corsAllowedOrigin
		corsHeaders["Access-Control-Allow-Methods"] = "GET, POST, PUT, DELETE, OPTIONS"
		corsHeaders["Access-Control-Allow-Headers"] = "Content-Type, X-API-Key, Authorization, X-Authorization, X-CSRF-Token"
		corsHeaders["Access-Control-Allow-Credentials"] = "true"
	}

	return corsHeaders
}

// validateRequest validates the incoming request and returns error response if validation fails
func (h *Handler) validateRequest(ctx context.Context, req *events.LambdaFunctionURLRequest, method, path string, corsHeaders map[string]string) *events.LambdaFunctionURLResponse {
	// Validate request body size
	if err := validateRequestBodySize(req.Body); err != nil {
		logging.Warnf("Request body size exceeded: %d bytes", len(req.Body))
		resp, _ := h.buildResponse(413, corsHeaders, map[string]string{"error": "Request body too large"}, nil)
		return resp
	}

	// Validate Content-Type
	if err := validateContentType(req); err != nil {
		resp, _ := h.buildResponse(415, corsHeaders, map[string]string{"error": err.Error()}, nil)
		return resp
	}

	// Validate authentication and CSRF
	if response := h.validateSecurity(ctx, req, method, path, corsHeaders); response != nil {
		return response
	}

	return nil
}

// validateSecurity validates authentication and CSRF token
func (h *Handler) validateSecurity(ctx context.Context, req *events.LambdaFunctionURLRequest, method, path string, corsHeaders map[string]string) *events.LambdaFunctionURLResponse {
	if h.isPublicEndpoint(path) {
		return nil
	}

	if !h.authenticate(ctx, req) {
		resp, _ := h.buildResponse(401, corsHeaders, map[string]string{"error": "Unauthorized"}, nil)
		return resp
	}

	if h.requiresCSRFValidation(method, path) {
		if err := h.validateCSRF(ctx, req); err != nil {
			logging.Warnf("CSRF validation failed: %v", err)
			resp, _ := h.buildResponse(403, corsHeaders, map[string]string{"error": "CSRF validation failed"}, nil)
			return resp
		}
	}

	return nil
}

// executeRequest routes and executes the API request
func (h *Handler) executeRequest(ctx context.Context, method, path string, req *events.LambdaFunctionURLRequest, corsHeaders map[string]string) (*events.LambdaFunctionURLResponse, error) {
	response, err := h.routeRequest(ctx, method, path, req)

	statusCode := 200
	if err != nil {
		statusCode, response = h.handleRequestError(err)
	}

	return h.buildResponse(statusCode, corsHeaders, response, nil)
}

// handleRequestError converts an error to status code and response
func (h *Handler) handleRequestError(err error) (int, any) {
	if IsNotFoundError(err) {
		return 404, map[string]string{"error": "Not found"}
	}
	if ce, ok := IsClientError(err); ok {
		return ce.code, map[string]string{"error": ce.message}
	}

	logging.Errorf("API error: %v", err)
	return 500, map[string]string{"error": "Internal server error"}
}

// rawResponse allows handlers to return pre-formatted, non-JSON content
// (e.g. HTML, YAML). buildResponse will use the body and contentType directly
// instead of JSON-marshaling.
type rawResponse struct {
	contentType string
	body        string
}

// buildResponse creates a Lambda Function URL response
func (h *Handler) buildResponse(statusCode int, headers map[string]string, body any, err error) (*events.LambdaFunctionURLResponse, error) {
	if err != nil {
		return &events.LambdaFunctionURLResponse{
			StatusCode: 500,
			Headers:    headers,
			Body:       `{"error": "internal server error"}`,
		}, nil
	}

	// Handle raw (non-JSON) responses
	if raw, ok := body.(*rawResponse); ok {
		headers["Content-Type"] = raw.contentType
		return &events.LambdaFunctionURLResponse{
			StatusCode: statusCode,
			Headers:    headers,
			Body:       raw.body,
		}, nil
	}

	var bodyBytes []byte
	if body != nil {
		var marshalErr error
		bodyBytes, marshalErr = json.Marshal(body)
		if marshalErr != nil {
			logging.Errorf("Failed to marshal response: %v", marshalErr)
			return &events.LambdaFunctionURLResponse{
				StatusCode: 500,
				Headers:    headers,
				Body:       `{"error": "internal server error"}`,
			}, nil
		}
	}

	return &events.LambdaFunctionURLResponse{
		StatusCode: statusCode,
		Headers:    headers,
		Body:       string(bodyBytes),
	}, nil
}

// loadAPIKey retrieves the API key from Secrets Manager.
// It caches the base AWS config via awsCfgOnce to avoid redundant config loads.
func (h *Handler) loadAPIKey(ctx context.Context) (string, error) {
	if h.secretsARN == "" {
		return "", nil
	}

	h.awsCfgOnce.Do(func() {
		h.awsCfg, h.awsCfgErr = awsconfig.LoadDefaultConfig(ctx)
	})
	if h.awsCfgErr != nil {
		return "", h.awsCfgErr
	}

	client := secretsmanager.NewFromConfig(h.awsCfg)
	result, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &h.secretsARN,
	})
	if err != nil {
		return "", err
	}

	if result.SecretString == nil {
		return "", nil
	}

	return *result.SecretString, nil
}
