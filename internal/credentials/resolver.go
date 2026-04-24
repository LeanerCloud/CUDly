package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Credential type constants used as credType in CredentialStore.
const (
	CredTypeAWSAccessKeys     = "aws_access_keys"
	CredTypeAzureClientSecret = "azure_client_secret"
	CredTypeGCPServiceAccount = "gcp_service_account"
	CredTypeGCPWIFConfig      = "gcp_workload_identity_config"
)

const gcpCloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// awsAccessKeyPayload is the JSON structure stored for CredTypeAWSAccessKeys.
type awsAccessKeyPayload struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

// AWSCredentials holds resolved AWS access key credentials.
type AWSCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

// String returns a redacted representation to prevent accidental logging.
func (c *AWSCredentials) String() string { return "[REDACTED AWS CREDENTIALS]" }

// AzureCredentials holds resolved Azure service principal credentials.
type AzureCredentials struct {
	ClientSecret string
}

// String returns a redacted representation.
func (c *AzureCredentials) String() string { return "[REDACTED AZURE CREDENTIALS]" }

// STSClient is the STS interface required for role assumption and web identity federation.
// Satisfied by *sts.Client from github.com/aws/aws-sdk-go-v2/service/sts.
type STSClient interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
	AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

// AccountLookupFunc resolves a CloudAccount by ID. Used by the bastion path so
// the resolver can self-load the bastion account's credentials rather than
// trust a caller-supplied STS client.
type AccountLookupFunc func(ctx context.Context, id string) (*config.CloudAccount, error)

// STSClientFactory builds an STSClient from a credentials provider. Used by
// the bastion path to construct an STS client backed by the bastion account's
// own credentials before assuming the target role.
type STSClientFactory func(provider aws.CredentialsProvider) STSClient

// AWSResolveOptions holds optional dependencies for the AWS credential
// resolver. The bastion path needs both AccountLookup and STSClientFactory to
// self-resolve correctly; without them, bastion mode falls back to the
// pre-self-loading behaviour (trusts the caller-supplied STS client) for
// backward compatibility.
type AWSResolveOptions struct {
	AccountLookup    AccountLookupFunc
	STSClientFactory STSClientFactory
}

// ResolveAWSCredentialProvider is a back-compat wrapper that calls
// ResolveAWSCredentialProviderWithOpts with empty options. New callers should
// use the WithOpts variant so bastion mode can self-resolve.
func ResolveAWSCredentialProvider(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
	stsClient STSClient,
) (aws.CredentialsProvider, error) {
	return ResolveAWSCredentialProviderWithOpts(ctx, account, store, stsClient, AWSResolveOptions{})
}

// ResolveAWSCredentialProviderWithOpts returns an aws.CredentialsProvider for
// the account.
//
// Logic per auth mode:
//   - access_keys: load stored key pair → static credentials provider
//   - role_arn:    use ambient (instance role / env) → STS AssumeRole
//   - bastion:     load bastion account creds → STS AssumeRole into target
//     (requires opts.AccountLookup + opts.STSClientFactory)
//   - workload_identity_federation: token file → STS AssumeRoleWithWebIdentity
func ResolveAWSCredentialProviderWithOpts(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
	stsClient STSClient,
	opts AWSResolveOptions,
) (aws.CredentialsProvider, error) {
	switch account.AWSAuthMode {
	case "access_keys":
		return resolveAccessKeyProvider(ctx, account, store)
	case "role_arn":
		return resolveRoleARNProvider(ctx, account, stsClient, nil)
	case "bastion":
		return resolveBastionProvider(ctx, account, store, stsClient, opts)
	case "workload_identity_federation":
		return resolveWebIdentityProvider(account, stsClient)
	default:
		return nil, fmt.Errorf("credentials: unsupported aws_auth_mode %q for account %s", account.AWSAuthMode, account.ID)
	}
}

func resolveAccessKeyProvider(ctx context.Context, account *config.CloudAccount, store CredentialStore) (aws.CredentialsProvider, error) {
	if store == nil {
		return nil, fmt.Errorf("credentials: credential store required for access_keys mode (account %s)", account.ID)
	}
	raw, err := store.LoadRaw(ctx, account.ID, CredTypeAWSAccessKeys)
	if err != nil {
		return nil, fmt.Errorf("credentials: load access keys for account %s: %w", account.ID, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("credentials: no access keys stored for account %s", account.ID)
	}
	var payload awsAccessKeyPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("credentials: parse access key payload for account %s: %w", account.ID, err)
	}
	return credentials.NewStaticCredentialsProvider(payload.AccessKeyID, payload.SecretAccessKey, ""), nil
}

// resolveRoleARNProvider returns an auto-refreshing credential provider that
// assumes account.AWSRoleARN using stsClient. stscreds.AssumeRoleProvider
// transparently refreshes credentials before they expire, avoiding the
// 1-hour STS token expiry problem of static credentials.
func resolveRoleARNProvider(
	_ context.Context,
	account *config.CloudAccount,
	stsClient STSClient,
	_ aws.CredentialsProvider,
) (aws.CredentialsProvider, error) {
	if account.AWSRoleARN == "" {
		return nil, fmt.Errorf("credentials: aws_role_arn is required for role_arn auth mode (account %s)", account.ID)
	}

	sessionSuffix := account.ID
	if len(sessionSuffix) > 8 {
		sessionSuffix = sessionSuffix[:8]
	}

	provider := stscreds.NewAssumeRoleProvider(stsClient, account.AWSRoleARN, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = "cudly-" + sessionSuffix
		if account.AWSExternalID != "" {
			o.ExternalID = aws.String(account.AWSExternalID)
		}
	})
	return provider, nil
}

// resolveBastionProvider implements the bastion auth mode: target account is
// reached via STS AssumeRole using credentials from a separate bastion account.
//
// Self-resolving path (preferred): when opts.AccountLookup and
// opts.STSClientFactory are both set, the resolver loads the bastion account,
// recursively resolves its credentials, builds a bastion-scoped STS client, and
// uses that to assume the target role. Bastion-of-bastion chaining is rejected
// at depth 1 to prevent loops.
//
// Legacy path: when either option is nil, the resolver falls back to the old
// behaviour and trusts that the caller-supplied stsClient already carries
// bastion credentials. This preserves backward compatibility with callers that
// have not yet been updated to wire the lookup/factory.
func resolveBastionProvider(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
	stsClient STSClient,
	opts AWSResolveOptions,
) (aws.CredentialsProvider, error) {
	if account.AWSBastionID == "" {
		return nil, fmt.Errorf("credentials: aws_bastion_id is required for bastion auth mode (account %s)", account.ID)
	}
	if opts.AccountLookup == nil || opts.STSClientFactory == nil {
		// Legacy fallback: trust caller's stsClient. Tracked in known_issues/03.
		return resolveRoleARNProvider(ctx, account, stsClient, nil)
	}
	bastion, err := opts.AccountLookup(ctx, account.AWSBastionID)
	if err != nil {
		return nil, fmt.Errorf("credentials: load bastion account %s: %w", account.AWSBastionID, err)
	}
	if bastion == nil {
		return nil, fmt.Errorf("credentials: bastion account %s not found", account.AWSBastionID)
	}
	if !bastion.Enabled {
		return nil, fmt.Errorf("credentials: bastion account %s is disabled", account.AWSBastionID)
	}
	if bastion.AWSAuthMode == "bastion" {
		return nil, fmt.Errorf("credentials: bastion chaining not allowed (account %s references bastion %s which is itself a bastion)", account.ID, bastion.ID)
	}
	// Recursively resolve the bastion's own credentials. Pass empty opts to
	// guarantee the recursive call cannot trigger bastion mode (already
	// guarded above by the AWSAuthMode check, but defence in depth).
	bastionCreds, err := ResolveAWSCredentialProviderWithOpts(ctx, bastion, store, stsClient, AWSResolveOptions{})
	if err != nil {
		return nil, fmt.Errorf("credentials: resolve bastion %s creds: %w", bastion.ID, err)
	}
	bastionSTS := opts.STSClientFactory(bastionCreds)
	return resolveRoleARNProvider(ctx, account, bastionSTS, nil)
}

// resolveWebIdentityProvider returns a credential provider that exchanges an OIDC token
// for temporary AWS credentials via STS AssumeRoleWithWebIdentity.
// The token file path comes from account.AWSWebIdentityTokenFile, with a fallback to
// the AWS_WEB_IDENTITY_TOKEN_FILE environment variable (standard AKS/GKE projected token path).
func resolveWebIdentityProvider(account *config.CloudAccount, stsClient STSClient) (aws.CredentialsProvider, error) {
	if account.AWSRoleARN == "" {
		return nil, fmt.Errorf("credentials: aws_role_arn required for workload_identity_federation mode (account %s)", account.ID)
	}
	tokenFile := account.AWSWebIdentityTokenFile
	if tokenFile == "" {
		tokenFile = os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")
	}
	if tokenFile == "" {
		return nil, fmt.Errorf("credentials: aws_web_identity_token_file required for workload_identity_federation mode (account %s)", account.ID)
	}
	// Validate token file path to prevent directory traversal.
	if strings.Contains(tokenFile, "..") || !filepath.IsAbs(tokenFile) {
		return nil, fmt.Errorf("credentials: aws_web_identity_token_file must be an absolute path without '..' (account %s)", account.ID)
	}
	sessionSuffix := account.ID
	if len(sessionSuffix) > 8 {
		sessionSuffix = sessionSuffix[:8]
	}
	return stscreds.NewWebIdentityRoleProvider(stsClient, account.AWSRoleARN,
		stscreds.IdentityTokenFile(tokenFile),
		func(o *stscreds.WebIdentityRoleOptions) {
			o.RoleSessionName = "cudly-" + sessionSuffix
			// Note: WebIdentityRoleOptions does not have ExternalID;
			// OIDC identity is verified by the token subject claim instead.
		}), nil
}

// ResolveAzureCredentials decrypts and returns the Azure client secret for the account.
func ResolveAzureCredentials(ctx context.Context, account *config.CloudAccount, store CredentialStore) (*AzureCredentials, error) {
	raw, err := store.LoadRaw(ctx, account.ID, CredTypeAzureClientSecret)
	if err != nil {
		return nil, fmt.Errorf("credentials: load azure secret for account %s: %w", account.ID, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("credentials: no client secret stored for account %s", account.ID)
	}
	var payload struct {
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("credentials: parse azure secret for account %s: %w", account.ID, err)
	}
	return &AzureCredentials{ClientSecret: payload.ClientSecret}, nil
}

// ResolveGCPCredentials decrypts and returns the GCP service account JSON for the account.
func ResolveGCPCredentials(ctx context.Context, account *config.CloudAccount, store CredentialStore) ([]byte, error) {
	raw, err := store.LoadRaw(ctx, account.ID, CredTypeGCPServiceAccount)
	if err != nil {
		return nil, fmt.Errorf("credentials: load gcp service account for account %s: %w", account.ID, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("credentials: no service account stored for account %s", account.ID)
	}
	return raw, nil
}

// ResolveAzureTokenCredential is a convenience wrapper that passes
// zero-valued options to ResolveAzureTokenCredentialWithOpts. Since
// the legacy cert-based WIF path has been removed, this entry point
// can only resolve client_secret and managed_identity accounts — WIF
// accounts require opts.Signer + IssuerURL via the With-opts variant.
func ResolveAzureTokenCredential(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
) (azcore.TokenCredential, error) {
	return ResolveAzureTokenCredentialWithOpts(ctx, account, store, AzureResolveOptions{})
}

// ResolveAzureTokenCredentialWithOpts is like ResolveAzureTokenCredential
// but accepts per-deployment options. For workload_identity_federation
// accounts, AzureResolveOptions.Signer and .IssuerURL must both be set
// so BuildAzureFederatedCredential can mint the KMS-signed client
// assertion Azure AD expects — there is no fallback path.
//
// Routes by AzureAuthMode:
//   - managed_identity  → ManagedIdentityCredential (no stored cred needed)
//   - workload_identity_federation → federated credential via opts.Signer
//   - client_secret (default) → loads stored secret and returns ClientSecretCredential
func ResolveAzureTokenCredentialWithOpts(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
	opts AzureResolveOptions,
) (azcore.TokenCredential, error) {
	switch account.AzureAuthMode {
	case "managed_identity":
		return azidentity.NewManagedIdentityCredential(nil)
	case "workload_identity_federation":
		return resolveAzureWIFCredential(ctx, account, store, opts)
	default: // "client_secret" or empty
		if store == nil {
			return nil, fmt.Errorf("credentials: credential store required for azure client_secret account %s", account.ID)
		}
		if account.AzureTenantID == "" || account.AzureClientID == "" {
			return nil, fmt.Errorf("credentials: azure_tenant_id and azure_client_id required for client_secret mode (account %s)", account.ID)
		}
		creds, err := ResolveAzureCredentials(ctx, account, store)
		if err != nil {
			return nil, err
		}
		return azidentity.NewClientSecretCredential(account.AzureTenantID, account.AzureClientID, creds.ClientSecret, nil)
	}
}

// ResolveGCPTokenSource is the legacy entry point — calls
// ResolveGCPTokenSourceWithOpts with zero-valued options so existing
// cert-based / stored-JSON accounts keep working.
func ResolveGCPTokenSource(ctx context.Context, account *config.CloudAccount, store CredentialStore) (oauth2.TokenSource, error) {
	return ResolveGCPTokenSourceWithOpts(ctx, account, store, GCPResolveOptions{})
}

// ResolveGCPTokenSourceWithOpts is like ResolveGCPTokenSource but
// accepts per-deployment options. When opts.Signer and opts.IssuerURL
// are both set, accounts in workload_identity_federation mode with a
// gcp_wif_audience field and no stored JSON config are routed through
// BuildGCPFederatedCredential (the secret-free path). Existing
// stored-JSON accounts keep working.
//
// Routes by GCPAuthMode:
//   - application_default → returns (nil, nil); caller uses ADC.
//   - workload_identity_federation → federated (if no stored cred and
//     signer+issuer+audience present) or legacy stored-JSON.
//   - service_account_key (or empty) → stored-JSON path.
func ResolveGCPTokenSourceWithOpts(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
	opts GCPResolveOptions,
) (oauth2.TokenSource, error) {
	if account.GCPAuthMode == "application_default" {
		return nil, nil
	}
	if account.GCPAuthMode == "workload_identity_federation" {
		return resolveGCPWIFCredential(ctx, account, store, opts)
	}
	return loadStoredGCPTokenSource(ctx, account, store, CredTypeGCPServiceAccount)
}

// resolveGCPWIFCredential handles the workload_identity_federation
// auth mode for GCP. Mirrors the Azure split in azure_federated.go.
func resolveGCPWIFCredential(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
	opts GCPResolveOptions,
) (oauth2.TokenSource, error) {
	// Peek at the stored WIF JSON first — absent means this is a
	// federated (secret-free) account.
	var raw []byte
	if store != nil {
		raw, _ = store.LoadRaw(ctx, account.ID, CredTypeGCPWIFConfig)
	}

	issuer := opts.IssuerURL
	if issuer == "" {
		issuer = oidc.IssuerURL()
	}

	if opts.Signer != nil && issuer != "" && len(raw) == 0 && account.GCPWIFAudience != "" {
		return BuildGCPFederatedCredential(ctx, opts.Signer, issuer, account.GCPWIFAudience, account.GCPClientEmail)
	}

	// Legacy stored-JSON path.
	return loadStoredGCPTokenSource(ctx, account, store, CredTypeGCPWIFConfig)
}

// loadStoredGCPTokenSource reads a stored JSON credential of the
// given type and returns a TokenSource. Shared by both the
// service_account_key and legacy workload_identity_federation paths.
func loadStoredGCPTokenSource(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
	credType string,
) (oauth2.TokenSource, error) {
	if store == nil {
		return nil, fmt.Errorf("credentials: credential store required for gcp account %s", account.ID)
	}
	raw, err := store.LoadRaw(ctx, account.ID, credType)
	if err != nil {
		return nil, fmt.Errorf("credentials: load gcp credentials for account %s: %w", account.ID, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("credentials: no gcp credentials stored for account %s", account.ID)
	}
	creds, err := google.CredentialsFromJSON(ctx, raw, gcpCloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("credentials: parse gcp credentials for account %s: %w", account.ID, err)
	}
	return creds.TokenSource, nil
}
