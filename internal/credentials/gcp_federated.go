package credentials

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google/externalaccount"

	"github.com/LeanerCloud/CUDly/internal/oidc"
)

// gcpFederatedSubject is the fixed JWT subject CUDly uses when the
// target GCP project has a Workload Identity Pool provider bound to
// CUDly's own OIDC issuer. Changing this string is an incompatible
// change that requires every existing WIF provider's
// attribute_condition to be recreated.
const gcpFederatedSubject = "cudly-controller"

// kmsSubjectTokenSupplier implements externalaccount.SubjectTokenSupplier
// by minting a fresh CUDly-signed JWT on each call. The externalaccount
// token source handles caching of the GCP access token it gets back,
// so we only re-mint when GCP STS asks us to.
type kmsSubjectTokenSupplier struct {
	signer    oidc.Signer
	issuerURL string
	audience  string
}

// SubjectToken satisfies externalaccount.SubjectTokenSupplier.
func (s *kmsSubjectTokenSupplier) SubjectToken(ctx context.Context, _ externalaccount.SupplierOptions) (string, error) {
	now := time.Now()
	claims := map[string]any{
		"iss": s.issuerURL,
		"sub": gcpFederatedSubject,
		"aud": s.audience,
		"jti": uuid.NewString(),
		"nbf": now.Unix(),
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	}
	jws, err := oidc.Mint(ctx, s.signer, claims)
	if err != nil {
		return "", fmt.Errorf("credentials: mint gcp subject token: %w", err)
	}
	return jws, nil
}

// BuildGCPFederatedCredential returns an oauth2.TokenSource that
// authenticates to GCP using a federated Workload Identity Pool
// provider bound to CUDly's OIDC issuer. Each call to Token() mints
// a fresh KMS-signed JWT, exchanges it at sts.googleapis.com for a
// GCP federated token, then impersonates the target service account
// via iamcredentials.googleapis.com to get an access token scoped
// to cloud-platform.
//
// No service-account key JSON or any other long-lived secret is
// stored or read by this path — the signing happens inside the
// cloud KMS the Signer wraps.
func BuildGCPFederatedCredential(
	ctx context.Context,
	signer oidc.Signer,
	issuerURL string,
	audience string,
	serviceAccountEmail string,
) (oauth2.TokenSource, error) {
	if signer == nil {
		return nil, fmt.Errorf("credentials: gcp federated credential requires a non-nil oidc signer")
	}
	if issuerURL == "" {
		return nil, fmt.Errorf("credentials: gcp federated credential requires an issuer URL")
	}
	if audience == "" {
		return nil, fmt.Errorf("credentials: gcp federated credential requires a WIF provider audience")
	}
	if serviceAccountEmail == "" {
		return nil, fmt.Errorf("credentials: gcp federated credential requires a target service account email")
	}

	cfg := externalaccount.Config{
		Audience:                       audience,
		SubjectTokenType:               "urn:ietf:params:oauth:token-type:jwt",
		TokenURL:                       "https://sts.googleapis.com/v1/token",
		ServiceAccountImpersonationURL: "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" + serviceAccountEmail + ":generateAccessToken",
		SubjectTokenSupplier: &kmsSubjectTokenSupplier{
			signer:    signer,
			issuerURL: issuerURL,
			audience:  audience,
		},
		Scopes: []string{gcpCloudPlatformScope},
	}

	ts, err := externalaccount.NewTokenSource(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("credentials: build gcp external account token source: %w", err)
	}
	return ts, nil
}

// GCPResolveOptions carries per-deployment wiring that
// ResolveGCPTokenSource needs to pick the secret-free federated path.
// A zero value selects the legacy stored-JSON path for backward
// compatibility with accounts registered before the redesign.
type GCPResolveOptions struct {
	// Signer is the OIDC issuer signer for this CUDly deployment.
	Signer oidc.Signer
	// IssuerURL is the base URL this deployment publishes OIDC at
	// (e.g. "https://<cudly>/oidc"). Must match what the target GCP
	// Workload Identity Pool provider's issuer-uri is registered as.
	IssuerURL string
}
