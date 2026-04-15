package credentials

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/uuid"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/oidc"
)

// The federated-credential subject and audience are fixed strings that
// target Azure AD app registrations bind against. Changing either value
// is an incompatible change and requires operators to recreate every
// existing federated credential entry — treat them as a constant of the
// CUDly deployment contract.
const (
	azureFederatedSubject  = "cudly-controller"
	azureFederatedAudience = "api://AzureADTokenExchange"
)

// AzureResolveOptions carries per-deployment wiring that
// ResolveAzureTokenCredential needs to pick the federated (secret-free)
// path. A zero value selects the legacy cert-based path for backward
// compatibility with existing accounts.
type AzureResolveOptions struct {
	// Signer is the OIDC issuer signer for this CUDly deployment.
	// Non-nil signals that the deployment supports federated identity
	// credentials.
	Signer oidc.Signer
	// IssuerURL is the base URL at which this deployment publishes
	// /.well-known/openid-configuration. Must match what the Azure AD
	// federated credential is registered with.
	IssuerURL string
}

// resolveAzureWIFCredential handles the workload_identity_federation
// auth mode. Extracted from ResolveAzureTokenCredentialWithOpts to keep
// the top-level switch simple.
//
// Routing:
//   - opts.Signer + issuer URL available, no stored PEM → federated
//     path (BuildAzureFederatedCredential), secret-free.
//   - opts.Signer set but a stored PEM exists → legacy cert path, for
//     backward compatibility with accounts registered before the
//     redesign.
//   - opts.Signer not set → legacy cert path, requiring a stored PEM.
//
// The issuer URL comes from opts.IssuerURL if set, otherwise from the
// package-level oidc.IssuerURL() cache populated by the first inbound
// HTTP request — see internal/oidc/issuer_cache.go.
func resolveAzureWIFCredential(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
	opts AzureResolveOptions,
) (azcore.TokenCredential, error) {
	raw, _ := loadOptionalWIFKey(ctx, store, account.ID)

	issuerURL := opts.IssuerURL
	if issuerURL == "" {
		issuerURL = oidc.IssuerURL()
	}

	// Secret-free federated path — opt-in via signer+issuerURL, only
	// when the account has no legacy PEM stored.
	if opts.Signer != nil && issuerURL != "" && len(raw) == 0 {
		return BuildAzureFederatedCredential(opts.Signer, issuerURL, account.AzureTenantID, account.AzureClientID)
	}

	// Legacy cert-based path.
	if store == nil {
		return nil, fmt.Errorf("credentials: credential store required for azure wif account %s", account.ID)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("credentials: no wif key stored for account %s", account.ID)
	}
	return buildAzureWIFCredential(account, raw)
}

// loadOptionalWIFKey attempts to read the stored Azure WIF PEM blob for
// an account. Returns (nil, nil) when the store is absent, when the
// blob is unset, or when the load fails — callers use the absence of
// a blob to route, not the error.
func loadOptionalWIFKey(ctx context.Context, store CredentialStore, accountID string) ([]byte, error) {
	if store == nil {
		return nil, nil
	}
	return store.LoadRaw(ctx, accountID, CredTypeAzureWIF)
}

// BuildAzureFederatedCredential returns an azcore.TokenCredential that
// authenticates to Azure AD using a federated identity credential. On
// each call it mints a fresh JWT using the provided Signer, sets its
// issuer to the CUDly deployment URL, and hands it to
// azidentity.NewClientAssertionCredential which handles the actual
// exchange with the Azure token endpoint.
//
// The target Azure AD app registration must have a federated identity
// credential configured with:
//
//	issuer   = <issuerURL>
//	subject  = cudly-controller
//	audience = api://AzureADTokenExchange
//
// — all three are checked by Azure AD when validating the assertion.
//
// No private key, cert, or client secret is stored anywhere — the
// signing happens inside the cloud KMS the Signer wraps.
func BuildAzureFederatedCredential(
	signer oidc.Signer,
	issuerURL string,
	tenantID string,
	clientID string,
) (azcore.TokenCredential, error) {
	if signer == nil {
		return nil, fmt.Errorf("credentials: azure federated credential requires a non-nil oidc signer")
	}
	if issuerURL == "" {
		return nil, fmt.Errorf("credentials: azure federated credential requires an issuer URL")
	}
	if tenantID == "" || clientID == "" {
		return nil, fmt.Errorf("credentials: azure federated credential requires tenant_id and client_id")
	}

	assertionFunc := func(ctx context.Context) (string, error) {
		now := time.Now()
		claims := map[string]any{
			"iss": issuerURL,
			"sub": azureFederatedSubject,
			"aud": azureFederatedAudience,
			"jti": uuid.NewString(),
			"nbf": now.Unix(),
			"iat": now.Unix(),
			"exp": now.Add(5 * time.Minute).Unix(),
		}
		jws, err := oidc.Mint(ctx, signer, claims)
		if err != nil {
			return "", fmt.Errorf("credentials: mint azure client_assertion: %w", err)
		}
		return jws, nil
	}

	return azidentity.NewClientAssertionCredential(tenantID, clientID, assertionFunc, nil)
}
