package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/aws/aws-lambda-go/events"
)

func newOIDCRequest(path string) *events.LambdaFunctionURLRequest {
	req := &events.LambdaFunctionURLRequest{}
	req.RequestContext.HTTP.Path = path
	return req
}

func TestHandleOIDCIgnoresUnrelatedPaths(t *testing.T) {
	h := &Handler{dashboardURL: "https://cudly.example.com"}
	for _, p := range []string{"/api/health", "/.well-known/acme-challenge", "/login", ""} {
		if _, handled := h.HandleOIDC(context.Background(), newOIDCRequest(p)); handled {
			t.Errorf("path %q should not be handled by HandleOIDC", p)
		}
	}
}

func TestHandleOIDCReturns404WhenSignerMissing(t *testing.T) {
	h := &Handler{dashboardURL: "https://cudly.example.com"}
	for _, p := range []string{oidcDiscoveryPath, oidcJWKSPath} {
		resp, handled := h.HandleOIDC(context.Background(), newOIDCRequest(p))
		if !handled {
			t.Fatalf("path %q should be handled", p)
		}
		if resp.StatusCode != 404 {
			t.Errorf("path %q: status=%d want 404", p, resp.StatusCode)
		}
	}
}

func TestHandleOIDCDiscovery(t *testing.T) {
	signer, err := oidc.NewLocalSigner()
	if err != nil {
		t.Fatalf("local signer: %v", err)
	}
	h := &Handler{
		dashboardURL: "https://cudly.example.com",
		signer:       signer,
	}
	resp, handled := h.HandleOIDC(context.Background(), newOIDCRequest(oidcDiscoveryPath))
	if !handled || resp.StatusCode != 200 {
		t.Fatalf("discovery: handled=%v status=%d", handled, resp.StatusCode)
	}
	var doc oidc.Discovery
	if err := json.Unmarshal([]byte(resp.Body), &doc); err != nil {
		t.Fatalf("unmarshal discovery: %v", err)
	}
	if doc.Issuer != "https://cudly.example.com" {
		t.Errorf("issuer=%s", doc.Issuer)
	}
	if doc.JWKSURI != "https://cudly.example.com/.well-known/jwks.json" {
		t.Errorf("jwks_uri=%s", doc.JWKSURI)
	}
	if resp.Headers["Content-Type"] != "application/json" {
		t.Errorf("content-type=%s", resp.Headers["Content-Type"])
	}
}

func TestHandleOIDCJWKS(t *testing.T) {
	signer, err := oidc.NewLocalSigner()
	if err != nil {
		t.Fatalf("local signer: %v", err)
	}
	h := &Handler{
		dashboardURL: "https://cudly.example.com",
		signer:       signer,
	}
	resp, handled := h.HandleOIDC(context.Background(), newOIDCRequest(oidcJWKSPath))
	if !handled || resp.StatusCode != 200 {
		t.Fatalf("jwks: handled=%v status=%d", handled, resp.StatusCode)
	}
	var jwks oidc.JWKS
	if err := json.Unmarshal([]byte(resp.Body), &jwks); err != nil {
		t.Fatalf("unmarshal jwks: %v", err)
	}
	if len(jwks.Keys) != 1 {
		t.Fatalf("keys=%d want 1", len(jwks.Keys))
	}
	k := jwks.Keys[0]
	if k.Kty != "RSA" || k.Alg != "RS256" || k.Kid == "" || k.N == "" {
		t.Errorf("jwk malformed: %+v", k)
	}
}

func TestHandleOIDCPopulatesIssuerCache(t *testing.T) {
	signer, err := oidc.NewLocalSigner()
	if err != nil {
		t.Fatalf("local signer: %v", err)
	}
	h := &Handler{
		dashboardURL: "https://cudly.example.com",
		signer:       signer,
	}
	// Reset by setting a marker first, since the package-level cache is
	// process-wide and other tests may have populated it.
	oidc.SetIssuerURL("https://overridden.example.com")
	_, _ = h.HandleOIDC(context.Background(), newOIDCRequest(oidcDiscoveryPath))
	if got := oidc.IssuerURL(); got != "https://cudly.example.com" {
		t.Errorf("HandleOIDC should have overwritten the issuer cache, got %s", got)
	}
}

func TestResolveIssuerURLPrefersConfiguredIssuer(t *testing.T) {
	h := &Handler{
		issuerURL:    "https://from-env.example.com/",
		dashboardURL: "https://dashboard.example.com",
	}
	req := &events.LambdaFunctionURLRequest{}
	req.RequestContext.DomainName = "lambda.aws"
	got := h.resolveIssuerURL(req)
	if got != "https://from-env.example.com" {
		t.Errorf("got %s, want https://from-env.example.com", got)
	}
}

func TestResolveIssuerURLFallsBackToDashboard(t *testing.T) {
	h := &Handler{dashboardURL: "https://dashboard.example.com/"}
	req := &events.LambdaFunctionURLRequest{}
	req.RequestContext.DomainName = "lambda.aws"
	if got := h.resolveIssuerURL(req); got != "https://dashboard.example.com" {
		t.Errorf("got %s", got)
	}
}

func TestResolveIssuerURLFallsBackToRequestDomain(t *testing.T) {
	h := &Handler{}
	req := &events.LambdaFunctionURLRequest{}
	req.RequestContext.DomainName = "lambda.aws"
	if got := h.resolveIssuerURL(req); got != "https://lambda.aws" {
		t.Errorf("got %s", got)
	}
}
