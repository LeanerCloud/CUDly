package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/aws/aws-lambda-go/events"
)

// OIDC discovery paths served by this handler. Treated as an opaque
// set by the transport layer (lambda.go / http.go) which checks paths
// against HandleOIDC before the main router runs — that way the
// router and its auth middleware never see these requests and the
// paths don't leak into internal/api routing tables or public-endpoint
// allowlists.
const (
	oidcDiscoveryPath = "/.well-known/openid-configuration"
	oidcJWKSPath      = "/.well-known/jwks.json"
)

// IsOIDCDiscoveryPath returns true if path is one of the well-known
// OIDC issuer endpoints. The server transport layer uses this to
// route requests directly to HandleOIDC before the main API router.
func IsOIDCDiscoveryPath(path string) bool {
	return path == oidcDiscoveryPath || path == oidcJWKSPath
}

// HandleOIDC serves the two OIDC issuer endpoints directly, without
// going through the API router. Both are always public (no auth, no
// CSRF). Returns nil if path is not an OIDC discovery path so the
// caller can fall through to the main router.
//
// The Azure federated credential path also reads the resolved issuer
// URL via oidc.IssuerURL(), so calling this endpoint once populates
// the shared cache — which is how the purchase manager (no HTTP
// context) learns what iss claim to put in its client_assertion JWTs.
func (h *Handler) HandleOIDC(ctx context.Context, req *events.LambdaFunctionURLRequest) (*events.LambdaFunctionURLResponse, bool) {
	path := req.RequestContext.HTTP.Path
	if !IsOIDCDiscoveryPath(path) {
		return nil, false
	}

	if h.signer == nil {
		return h.oidcResponse(404, map[string]string{"error": "oidc issuer not configured"}), true
	}

	issuer := h.resolveIssuerURL(req)
	if issuer == "" {
		return h.oidcResponse(500, map[string]string{"error": "issuer url unavailable"}), true
	}

	switch path {
	case oidcDiscoveryPath:
		return h.oidcResponse(200, oidc.BuildDiscovery(issuer)), true
	case oidcJWKSPath:
		jwks, err := oidc.BuildJWKS(ctx, h.signer)
		if err != nil {
			return h.oidcResponse(500, map[string]string{"error": err.Error()}), true
		}
		return h.oidcResponse(200, jwks), true
	}
	return nil, false
}

func (h *Handler) oidcResponse(statusCode int, body any) *events.LambdaFunctionURLResponse {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		bodyBytes = []byte(`{"error":"marshal failed"}`)
		statusCode = 500
	}
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Cache-Control": "public, max-age=300", // 5 min — matches Azure AD JWKS refresh
	}
	// Reuse the standard security headers so responses look consistent
	// with the main API. CORS is intentionally *not* set: OIDC consumers
	// fetch these server-to-server and don't need CORS headers.
	setSecurityHeaders(headers)
	return &events.LambdaFunctionURLResponse{
		StatusCode: statusCode,
		Headers:    headers,
		Body:       string(bodyBytes),
	}
}

// resolveIssuerURL returns the base URL at which this CUDly deployment
// publishes its OIDC issuer. Preference order:
//  1. h.issuerURL (CUDLY_ISSUER_URL env var, if the operator set one)
//  2. h.dashboardURL (operator-configured dashboard URL)
//  3. the trusted Function URL context domain on the current request
//     (this is the common case for AWS Lambda deployments — it's the
//     only stable issuer value available since the Lambda env vars
//     cannot reference the Function URL without a Terraform cycle)
//
// Whatever we resolve here is also persisted via oidc.SetIssuerURL so
// the purchase manager's Azure federated credential path mints JWTs
// with a matching iss claim.
func (h *Handler) resolveIssuerURL(req *events.LambdaFunctionURLRequest) string {
	if url := h.pickIssuerURL(req); url != "" {
		oidc.SetIssuerURL(url)
		return url
	}
	return ""
}

func (h *Handler) pickIssuerURL(req *events.LambdaFunctionURLRequest) string {
	if h.issuerURL != "" {
		return strings.TrimRight(h.issuerURL, "/")
	}
	if h.dashboardURL != "" {
		return strings.TrimRight(h.dashboardURL, "/")
	}
	if req != nil && req.RequestContext.DomainName != "" {
		return "https://" + req.RequestContext.DomainName
	}
	return ""
}

// Ensure fmt is retained — resolveIssuerURL uses it indirectly via
// oidc.SetIssuerURL. The explicit reference keeps goimports happy.
var _ = fmt.Sprintf
