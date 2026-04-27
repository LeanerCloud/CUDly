// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/LeanerCloud/CUDly/internal/runtime"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Handler processes HTTP requests
type Handler struct {
	config             config.StoreInterface
	credStore          credentials.CredentialStore
	purchase           PurchaseManagerInterface
	scheduler          SchedulerInterface
	auth               AuthServiceInterface
	secretsARN         string
	apiKey             string // Cached API key
	corsAllowedOrigin  string // CORS allowed origin
	rateLimiter        RateLimiterInterface
	emailNotifier      email.SenderInterface       // Optional: purchase approval emails
	dashboardURL       string                      // Base URL for approval/cancel links
	analyticsClient    AnalyticsClientInterface    // Optional: analytics client (Postgres-backed in prod)
	analyticsCollector AnalyticsCollectorInterface // Optional: Hourly collector
	signer             oidc.Signer                 // Optional: OIDC issuer signer (backed by cloud KMS)
	issuerURL          string                      // Canonical OIDC issuer URL (falls back to dashboardURL / request domain)

	awsCfgOnce sync.Once  // guards one-time loading of the base AWS config
	awsCfg     aws.Config // cached base AWS config (no region override)
	awsCfgErr  error      // error from loading the base config, if any

	sourceIdentityOnce sync.Once       // guards one-time source identity resolution
	sourceID           *sourceIdentity // cached source cloud identity

	// Postgres-backed TTL cache for Cost Explorer
	// GetReservationUtilization. Dashboard + RI Exchange page hits
	// read from the shared cache table so Lambda containers don't each
	// fan out to a paid CE API call on every page load. See
	// ri_utilization_cache.go for the rationale; in-memory was ruled
	// out because Lambda's short container lifetime means each cold
	// start would bypass the cache entirely.
	riUtilizationCacheOnce sync.Once
	riUtilizationCache     *riUtilizationCache

	// Optional AWS-client injection points used by the reshape handler
	// integration test. When nil (the production default), the
	// handler falls back to the direct AWS SDK constructors
	// `awsprovider.NewEC2ClientDirect` and
	// `awsprovider.NewRecommendationsClientDirect`. Tests set these
	// to stubs that satisfy the narrow interfaces declared in
	// `handler_ri_exchange.go` (reshapeEC2Client / reshapeRecsClient)
	// so the test can exercise the handler end-to-end without live
	// AWS credentials. Prod behaviour is unchanged because both
	// fields stay nil.
	reshapeEC2Factory  func(aws.Config) reshapeEC2Client
	reshapeRecsFactory func(aws.Config) reshapeRecsClient

	// Optional account-resolver injection point used by the reshape
	// handler integration test. When nil (the production default), the
	// handler calls h.resolveAWSCloudAccountID which in turn invokes
	// sts.GetCallerIdentity — fine in Lambda but fails on dev machines
	// without AWS credentials. Tests set this to a fixed-result fake so
	// the integration suite runs hermetically.
	reshapeAccountResolver func(context.Context) (string, error)

	// commitmentOpts discovers which AWS (term, payment) combinations
	// each service actually sells and validates saves against that data.
	// Nil is valid: the endpoint returns unavailable and save-side
	// validation no-ops, deferring to the frontend's hardcoded rules.
	commitmentOpts CommitmentOptsInterface

	// encryptionKeySource is the env var name that resolved the credential
	// encryption key. Empty when no credStore is configured. Used by the
	// /health endpoint only — never logged outside that one place.
	encryptionKeySource string
}

// getRIUtilizationCache returns the Postgres-backed TTL cache for Cost
// Explorer results, lazy-initialised on first call so tests that never
// exercise the RI Exchange paths don't need to wire it up. Lambda
// detection happens here (once) via runtime.IsLambda so SWR is gated
// off on Lambda where background goroutines freeze between
// invocations.
func (h *Handler) getRIUtilizationCache() *riUtilizationCache {
	h.riUtilizationCacheOnce.Do(func() {
		h.riUtilizationCache = newRIUtilizationCache(h.config, runtime.IsLambda())
	})
	return h.riUtilizationCache
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
		config:              cfg.ConfigStore,
		credStore:           cfg.CredentialStore,
		purchase:            cfg.PurchaseManager,
		scheduler:           cfg.Scheduler,
		auth:                cfg.AuthService,
		secretsARN:          cfg.APIKeySecretARN,
		corsAllowedOrigin:   corsOrigin,
		rateLimiter:         cfg.RateLimiter,
		emailNotifier:       cfg.EmailNotifier,
		dashboardURL:        cfg.DashboardURL,
		analyticsClient:     cfg.AnalyticsClient,
		analyticsCollector:  cfg.AnalyticsCollector,
		signer:              cfg.OIDCSigner,
		issuerURL:           cfg.OIDCIssuerURL,
		commitmentOpts:      cfg.CommitmentOpts,
		encryptionKeySource: cfg.EncryptionKeySource,
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
	} else {
		// Nil-body success paths (e.g. DELETE /accounts/:id returning
		// (nil, nil)) previously emitted an empty string, which made the
		// frontend's `response.json()` throw SyntaxError on an otherwise-
		// successful request. Emit an explicit empty JSON object instead
		// so every 2xx response parses cleanly.
		bodyBytes = []byte("{}")
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

// sourceIdentity holds the auto-detected identity of the cloud account
// where CUDly itself runs. Resolved once at first use, cached for process lifetime.
type sourceIdentity struct {
	Provider       string `json:"provider"`
	AccountID      string `json:"account_id,omitempty"`      // AWS account number
	SubscriptionID string `json:"subscription_id,omitempty"` // Azure subscription
	TenantID       string `json:"tenant_id,omitempty"`       // Azure tenant
	ClientID       string `json:"client_id,omitempty"`       // Azure app client ID
	ProjectID      string `json:"project_id,omitempty"`      // GCP project
	// Partition is the AWS partition name (`aws`, `aws-cn`, `aws-us-gov`).
	// Populated only when Provider == "aws" and STS GetCallerIdentity
	// returned a parseable ARN; left empty on any failure path so the
	// frontend can default to the standard `aws` partition (issue #130c).
	Partition string `json:"partition,omitempty"`
}

// ExternalID returns the canonical external identifier for the source cloud.
func (s *sourceIdentity) ExternalID() string {
	switch s.Provider {
	case "aws":
		return s.AccountID
	case "azure":
		return s.SubscriptionID
	case "gcp":
		return s.ProjectID
	}
	return ""
}

// resolveSourceIdentity returns the auto-detected identity of CUDly's host
// account. All resolution is best-effort — returns an empty struct on failure.
func (h *Handler) resolveSourceIdentity(ctx context.Context) *sourceIdentity {
	h.sourceIdentityOnce.Do(func() {
		cloud := sourceCloud()
		id := &sourceIdentity{Provider: cloud}
		switch cloud {
		case "aws":
			// resolveSourceIdentity is best-effort and is consumed by
			// populateSourceAccountID, which fails loud on an empty
			// AccountID for the AWS-source case. STS errors are already
			// logged WARN inside resolveAWSCallerIdentity, so we drop
			// the error here explicitly — the consumer's empty-string
			// check is the security gate for federation rendering.
			// (Reshape uses resolveAWSAccountID directly which DOES
			// propagate the error for fail-closed multi-tenant safety.)
			id.AccountID, id.Partition, _ = h.resolveAWSCallerIdentity(ctx)
		case "azure":
			id.ClientID = os.Getenv("AZURE_CLIENT_ID")
			id.SubscriptionID = os.Getenv("AZURE_SUBSCRIPTION_ID")
			id.TenantID = os.Getenv("AZURE_TENANT_ID")
		case "gcp":
			id.ProjectID = os.Getenv("GCP_PROJECT_ID")
		}
		h.sourceID = id
	})
	return h.sourceID
}

// resolveSourceAccountID returns the AWS account ID where CUDly runs.
// Convenience wrapper for the federation IaC handler.
func (h *Handler) resolveSourceAccountID(ctx context.Context) string {
	return h.resolveSourceIdentity(ctx).AccountID
}

// resolveAWSAccountID returns the AWS account ID where CUDly runs by calling
// STS GetCallerIdentity. Convenience wrapper around resolveAWSCallerIdentity
// for callers that only need the account ID (and the security-paths-aware
// error propagation).
//
// Return shape distinguishes three cases:
//
//   - ("", nil)        — AWS SDK config could not load (deployment runs
//     without AWS context: e.g. Azure / GCP host).
//     This is the legitimate "no AWS account configured"
//     signal; callers may treat it as such.
//   - ("", err)        — AWS SDK config loaded but STS GetCallerIdentity
//     failed (transient API error, missing IAM permission,
//     token expiry). Callers in security-sensitive paths
//     (e.g. multi-tenant scope filters) MUST fail closed
//     on this — treating it like the legitimate empty
//     case can leak data across tenants.
//   - (accountID, nil) — success.
//
// WARNING: callers in user-facing flows must check the result and fail loud
// rather than rendering an empty account ID into a bundle — a bundle with
// an empty source_account_id produces a broken trust policy that silently
// fails at apply time.
func (h *Handler) resolveAWSAccountID(ctx context.Context) (string, error) {
	id, _, err := h.resolveAWSCallerIdentity(ctx)
	return id, err
}

// resolveAWSCallerIdentity returns (accountID, partition) parsed from STS
// GetCallerIdentity. The partition is taken from the returned ARN's second
// segment (e.g., `arn:aws-us-gov:iam::...` → "aws-us-gov") and is used by
// the trust-policy snippet renderer to emit the correct ARN prefix in
// AWS China and GovCloud deployments (issue #130c).
//
// Return shape:
//
//   - ("", "", nil)               — host is non-AWS (sourceCloud() != "aws")
//     AND AWS SDK config could not load. This is the legitimate
//     "no AWS context" path for Azure/GCP-hosted deployments.
//   - ("", "", err)               — host is AWS but the SDK config could
//     not load, OR config loaded but STS GetCallerIdentity failed.
//     Both are real failures; security-sensitive callers MUST fail
//     closed on this.
//   - (accountID, partition, nil) — success. partition may still be "" if the
//     ARN was malformed (frontend defaults to "aws").
//
// The sourceCloud() check on the config-load path prevents an
// AWS-hosted deployment from being silently treated as "no AWS context"
// when its own SDK config breaks — that would degrade the multi-tenant
// scope filter in resolveAWSCloudAccountID into an unscoped read.
func (h *Handler) resolveAWSCallerIdentity(ctx context.Context) (string, string, error) {
	h.awsCfgOnce.Do(func() {
		h.awsCfg, h.awsCfgErr = awsconfig.LoadDefaultConfig(ctx)
	})
	if h.awsCfgErr != nil {
		if sourceCloud() == "aws" {
			// AWS host but SDK config broken: real failure. Surface
			// the error so security-sensitive callers fail closed.
			return "", "", fmt.Errorf("aws sdk config load: %w", h.awsCfgErr)
		}
		// Azure/GCP host: AWS context is legitimately absent, not an
		// error from this caller's perspective.
		return "", "", nil
	}
	client := sts.NewFromConfig(h.awsCfg)
	identity, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		logging.Warnf("Failed to resolve source account ID via STS: %v", err)
		return "", "", fmt.Errorf("sts get-caller-identity: %w", err)
	}
	var accountID, partition string
	if identity.Account != nil {
		accountID = *identity.Account
	}
	if identity.Arn != nil {
		partition = parseArnPartition(*identity.Arn)
	}
	return accountID, partition, nil
}

// parseArnPartition extracts the partition segment from an AWS ARN.
// ARN format: arn:<partition>:<service>:<region>:<account>:<resource>.
// Returns "" for inputs that aren't recognisable ARNs so the caller can
// fall back to a default. Only the three known AWS partitions are
// accepted — anything else is treated as malformed to avoid forwarding
// attacker-controlled tokens into a JSON snippet the operator copy-
// pastes into IAM.
func parseArnPartition(arn string) string {
	const prefix = "arn:"
	if len(arn) <= len(prefix) || arn[:len(prefix)] != prefix {
		return ""
	}
	rest := arn[len(prefix):]
	end := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == ':' {
			end = i
			break
		}
	}
	if end <= 0 {
		return ""
	}
	switch rest[:end] {
	case "aws", "aws-cn", "aws-us-gov":
		return rest[:end]
	default:
		return ""
	}
}
