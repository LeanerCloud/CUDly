package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/aws/aws-lambda-go/events"
)

func TestOIDCHandlersDisabledWhenSignerMissing(t *testing.T) {
	h := &Handler{dashboardURL: "https://cudly.example.com"}
	ctx := context.Background()
	req := &events.LambdaFunctionURLRequest{}

	if _, err := h.getOpenIDConfiguration(ctx, req); err == nil {
		t.Error("expected 404 error when signer is nil, got nil")
	}
	if _, err := h.getJWKS(ctx, req); err == nil {
		t.Error("expected 404 error when signer is nil, got nil")
	}
}

func TestOIDCDiscoveryHandlerWithSigner(t *testing.T) {
	signer, err := oidc.NewLocalSigner()
	if err != nil {
		t.Fatalf("local signer: %v", err)
	}
	h := &Handler{
		dashboardURL: "https://cudly.example.com",
		signer:       signer,
	}
	ctx := context.Background()
	req := &events.LambdaFunctionURLRequest{}

	resp, err := h.getOpenIDConfiguration(ctx, req)
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	doc, ok := resp.(oidc.Discovery)
	if !ok {
		t.Fatalf("expected Discovery, got %T", resp)
	}
	if doc.Issuer != "https://cudly.example.com" {
		t.Errorf("issuer=%s", doc.Issuer)
	}
	if doc.JWKSURI != "https://cudly.example.com/.well-known/jwks.json" {
		t.Errorf("jwks_uri=%s", doc.JWKSURI)
	}
}

func TestOIDCJWKSHandlerWithSigner(t *testing.T) {
	signer, err := oidc.NewLocalSigner()
	if err != nil {
		t.Fatalf("local signer: %v", err)
	}
	h := &Handler{
		dashboardURL: "https://cudly.example.com",
		signer:       signer,
	}
	ctx := context.Background()

	resp, err := h.getJWKS(ctx, &events.LambdaFunctionURLRequest{})
	if err != nil {
		t.Fatalf("jwks: %v", err)
	}
	jwks, ok := resp.(oidc.JWKS)
	if !ok {
		t.Fatalf("expected JWKS, got %T", resp)
	}
	if len(jwks.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(jwks.Keys))
	}
	// Round-trip the JWKS through JSON to make sure it serializes cleanly.
	raw, err := json.Marshal(jwks)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rt oidc.JWKS
	if err := json.Unmarshal(raw, &rt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rt.Keys[0].Kid != jwks.Keys[0].Kid {
		t.Errorf("kid lost in round trip")
	}
}

func TestResolveIssuerURLPrefersDashboard(t *testing.T) {
	h := &Handler{dashboardURL: "https://cudly.example.com/"}
	req := &events.LambdaFunctionURLRequest{}
	req.RequestContext.DomainName = "lambda.aws"
	got := h.resolveIssuerURL(req)
	if got != "https://cudly.example.com" {
		t.Errorf("got %s, want https://cudly.example.com", got)
	}
}

func TestResolveIssuerURLFallsBackToRequestDomain(t *testing.T) {
	h := &Handler{}
	req := &events.LambdaFunctionURLRequest{}
	req.RequestContext.DomainName = "lambda.aws"
	got := h.resolveIssuerURL(req)
	if got != "https://lambda.aws" {
		t.Errorf("got %s, want https://lambda.aws", got)
	}
}
