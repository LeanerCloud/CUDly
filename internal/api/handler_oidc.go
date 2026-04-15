package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/aws/aws-lambda-go/events"
)

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

// getOpenIDConfiguration handles GET /.well-known/openid-configuration.
// Public, no auth. Returns the minimal discovery document Azure AD (and
// other OIDC verifiers) need to locate the JWKS for client_assertion
// validation.
func (h *Handler) getOpenIDConfiguration(_ context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.signer == nil {
		return nil, NewClientError(404, "oidc issuer not configured")
	}
	issuer := h.resolveIssuerURL(req)
	if issuer == "" {
		return nil, fmt.Errorf("oidc: cannot resolve issuer URL (no dashboard URL or request context)")
	}
	return oidc.BuildDiscovery(issuer), nil
}

// getJWKS handles GET /.well-known/jwks.json. Public, no auth. Returns
// the public half of the signing key so Azure AD can verify JWTs minted
// by CUDly. The private key never leaves the backing cloud KMS.
func (h *Handler) getJWKS(ctx context.Context, _ *events.LambdaFunctionURLRequest) (any, error) {
	if h.signer == nil {
		return nil, NewClientError(404, "oidc issuer not configured")
	}
	jwks, err := oidc.BuildJWKS(ctx, h.signer)
	if err != nil {
		return nil, fmt.Errorf("oidc: build jwks: %w", err)
	}
	return jwks, nil
}
